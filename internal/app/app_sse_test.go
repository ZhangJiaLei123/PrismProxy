package app

// app_sse_test.go：SSE 实时推送在 app 层的接线回归（CLI实时推送设计.md §4.2）。
// 关键防线：headless 下 a.ctx 恒 nil，flush() 若把 hub fan-out 放在 ctx 早退之后，
// 推送通道会完全无数据——且 GUI 测试发现不了。这里在 ctx==nil 下直接验证。

import (
	"bufio"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/store"
)

// readSSEEvent 从事件流读下一个 event 块（跳过心跳），返回 event 名与 data
func readSSEEvent(t *testing.T, r *bufio.Reader, timeout time.Duration) (event, data string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var ev, d []string
	for {
		if time.Now().After(deadline) {
			t.Fatalf("读 SSE 帧超时（event=%v data=%v）", ev, d)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("读 SSE 帧失败: %v", err)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			if len(ev) > 0 {
				return strings.Join(ev, ","), strings.Join(d, "\n")
			}
			continue
		}
		if strings.HasPrefix(trimmed, ":") || strings.HasPrefix(trimmed, "id:") {
			continue
		}
		field, value, _ := strings.Cut(trimmed, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev = append(ev, value)
		case "data":
			d = append(d, value)
		}
	}
}

// 启动 ctlapi（复用 app 的 ctlService）并打开事件流
func startCtlWithEvents(t *testing.T, a *App) (srv *ctlapi.Server, r *bufio.Reader, closeFn func()) {
	t.Helper()
	epFile := filepath.Join(t.TempDir(), "ctl-endpoint.json")
	srv = ctlapi.NewServer("127.0.0.1:0", "test-token", epFile, newCtlService(a), nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("启动控制 API 失败: %v", err)
	}
	a.ctl = srv

	url := fmt.Sprintf("http://%s/api/v1/events?channels=flows,status", srv.Addr())
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("打开事件流失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("事件流应 200，得 %d", resp.StatusCode)
	}
	r = bufio.NewReader(resp.Body)
	// open 帧
	if ev, _ := readSSEEvent(t, r, 2*time.Second); ev != "open" {
		t.Fatalf("首帧应为 open，得 %s", ev)
	}
	// status seed 帧
	if ev, _ := readSSEEvent(t, r, 2*time.Second); ev != "status" {
		t.Fatalf("第二帧应为 status seed，得 %s", ev)
	}
	return srv, r, func() {
		resp.Body.Close()
		srv.Close()
	}
}

// headless（ctx==nil）下 store 产生流量 → SSE 仍能收到 flows upsert（flush fan-out 不被 ctx 早退丢弃）
func TestHeadlessSSEFlowsFanOut(t *testing.T) {
	a := newTestApp(t)
	a.st.Subscribe(a.onStoreEvent) // 复刻 NewApp 的事件订阅
	// 关键：不设置 a.ctx（模拟 headless）

	_, r, closeFn := startCtlWithEvents(t, a)
	defer closeFn()

	// 制造一条流入 store（Add 触发 store 事件 → onStoreEvent → 50ms 合帧 → flush fan-out）
	f := testFlow("sse-flow-1")
	f.ServerAddr = "sse.example.com:443" // toMeta 的 Host 取自 ServerAddr
	a.st.Add(f)

	ev, data := readSSEEvent(t, r, 3*time.Second)
	if ev != "flows" || !strings.Contains(data, "upsert") || !strings.Contains(data, "sse.example.com") {
		t.Fatalf("headless 下应收到 flows upsert 帧: ev=%s data=%s", ev, data)
	}
}

// evict 帧同样送达（store 清空/淘汰）
func TestHeadlessSSEEvict(t *testing.T) {
	a := newTestApp(t)
	a.st.Subscribe(a.onStoreEvent)
	_, r, closeFn := startCtlWithEvents(t, a)
	defer closeFn()

	f := testFlow("sse-evict-1")
	a.st.Add(f)
	if ev, _ := readSSEEvent(t, r, 3*time.Second); ev != "flows" {
		t.Fatalf("应先收到 upsert，得 %s", ev)
	}

	// 清空触发 evict
	a.st.Clear()
	ev, data := readSSEEvent(t, r, 3*time.Second)
	if ev != "flows" || !strings.Contains(data, "evict") || !strings.Contains(data, "sse-evict-1") {
		t.Fatalf("应收到 evict 帧含被清 ID: ev=%s data=%s", ev, data)
	}
}

// status 发布点：StopProxy/StartProxy 后推送 status 帧
func TestSSEStatusPushOnProxyChange(t *testing.T) {
	a := newTestApp(t)
	// 手动塞一个假 srv 让 StopProxy 有东西可停？StartProxy 需要真实端口。
	// 这里直接验证 publishStatus 无 ctl 时不 panic、有 ctl 时推帧：
	// 用 ctlStatusSnapshot 构造（不实际启停代理，避免端口依赖）
	_, r, closeFn := startCtlWithEvents(t, a)
	defer closeFn()

	// seed 已消费；手动发布一帧 status 模拟发布点
	a.publishStatus()
	ev, data := readSSEEvent(t, r, 2*time.Second)
	if ev != "status" || !strings.Contains(data, `"proxy"`) {
		t.Fatalf("应收到 status 帧: ev=%s data=%s", ev, data)
	}

	// 无控制 API 时 publishStatus/fanOut 必须安全（降级 no-op）
	a2 := newTestApp(t)
	a2.publishStatus() // a2.ctl == nil
	a2.fanOutFlows([]FlowMeta{{ID: "x"}}, nil)
	// 不 panic 即通过
	_ = store.Event{}
}
