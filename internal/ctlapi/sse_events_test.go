package ctlapi

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// openEvents 打开一条 SSE 连接，返回响应体与已读到的事件文本（到 stop 为止的累积读取器在 r.Body）。
func openEvents(t *testing.T, addr, token, query string) *http.Response {
	t.Helper()
	url := fmt.Sprintf("http://%s/api/v1/events%s", addr, query)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("打开事件流失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("事件流应 200，得 %d", resp.StatusCode)
	}
	return resp
}

// readEvent 阻塞读取一个 event 块（返回 event 名 + data 行拼接），心跳块跳过。
func readEvent(t *testing.T, r *bufio.Reader) (event, data string) {
	t.Helper()
	for {
		var ev, d []string
		deadline := time.After(2 * time.Second)
		for {
			lineCh := make(chan string, 1)
			errCh := make(chan error, 1)
			go func() {
				l, e := r.ReadString('\n')
				if e != nil {
					errCh <- e
					return
				}
				lineCh <- l
			}()
			var line string
			select {
			case line = <-lineCh:
			case e := <-errCh:
				t.Fatalf("读事件帧失败: %v", e)
			case <-deadline:
				t.Fatal("读事件帧超时")
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				if len(ev) > 0 {
					return strings.Join(ev, ","), strings.Join(d, "\n")
				}
				break // 心跳后的空行，继续
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
}

// open 帧携带 instanceId；订阅含 status 时紧随 seed 帧
func TestEventsOpenAndStatusSeed(t *testing.T) {
	svc := &fakeService{uiOn: true}
	srv, addr, token := startTestServer(t, svc)

	resp := openEvents(t, addr, token, "?channels=flows,status")
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)

	ev, data := readEvent(t, r)
	if ev != "open" {
		t.Fatalf("首帧应为 open，得 %s", ev)
	}
	if !strings.Contains(data, srv.InstanceID()) || !strings.Contains(data, `"instanceId"`) {
		t.Fatalf("open 帧应含 instanceId=%s: %s", srv.InstanceID(), data)
	}
	ev, data = readEvent(t, r)
	if ev != "status" {
		t.Fatalf("第二帧应为 status seed，得 %s", ev)
	}
	if !strings.Contains(data, `"running"`) {
		t.Fatalf("seed 帧应为状态快照: %s", data)
	}
}

// flows upsert/evict 实时推送
func TestEventsFlowsPush(t *testing.T) {
	svc := &fakeService{}
	srv, addr, token := startTestServer(t, svc)

	resp := openEvents(t, addr, token, "")
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if ev, _ := readEvent(t, r); ev != "open" {
		t.Fatalf("首帧应为 open，得 %s", ev)
	}

	srv.Hub().Publish("flows", FlowsUpsert{Type: "upsert", Flows: []Filterable{
		fakeFlow{Host: "push.example.com", URL: "https://push.example.com/a", Path: "/a"},
	}})
	ev, data := readEvent(t, r)
	if ev != "flows" || !strings.Contains(data, "upsert") || !strings.Contains(data, "push.example.com") {
		t.Fatalf("应收 upsert 帧: ev=%s data=%s", ev, data)
	}

	srv.Hub().Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"id-1"}})
	ev, data = readEvent(t, r)
	if ev != "flows" || !strings.Contains(data, "evict") || !strings.Contains(data, "id-1") {
		t.Fatalf("应收 evict 帧: ev=%s data=%s", ev, data)
	}
}

// filter：服务端过滤 upsert（不匹配的流不推）
func TestEventsFilter(t *testing.T) {
	svc := &fakeService{}
	srv, addr, token := startTestServer(t, svc)

	resp := openEvents(t, addr, token, "?filter=keep.com")
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	readEvent(t, r) // open

	// 全不匹配 → 该帧被跳过（读下一帧验证匹配项）
	srv.Hub().Publish("flows", FlowsUpsert{Type: "upsert", Flows: []Filterable{
		fakeFlow{Host: "drop.com", URL: "https://drop.com", Path: "/"},
	}})
	srv.Hub().Publish("flows", FlowsUpsert{Type: "upsert", Flows: []Filterable{
		fakeFlow{Host: "keep.com", URL: "https://keep.com/p", Path: "/p"},
	}})
	ev, data := readEvent(t, r)
	if ev != "flows" || !strings.Contains(data, "keep.com") || strings.Contains(data, "drop.com") {
		t.Fatalf("filter 应只投递匹配流: %s", data)
	}
}

// 未知频道 → 400；订阅者满 → 503；query token 仅 /events 接受
func TestEventsErrorsAndQueryToken(t *testing.T) {
	svc := &fakeService{}
	srv, addr, token := startTestServer(t, svc)
	// 等待服务端订阅名额收敛（连接关闭后 handler 检测 ctx.Done → unsubscribe 是异步的）
	waitSubs := func(want int) {
		for i := 0; i < 50; i++ {
			if srv.Hub().SubscriberCount() == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("订阅者数未收敛到 %d（当前 %d）", want, srv.Hub().SubscriberCount())
	}

	// 未知频道
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/events?channels=bogus", addr), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知频道应 400，得 %d", resp.StatusCode)
	}

	// query token 在 /events 可用
	req2, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/events", addr), nil)
	req2.URL.RawQuery = "token=" + token
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		resp2.Body.Close()
		t.Fatalf("query token 应被 /events 接受，得 %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// query token 在其他端点无效（401）
	req3, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/status?token=%s", addr, token), nil)
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token 在 /status 应 401，得 %d", resp3.StatusCode)
	}

	// 订阅者上限 → 503：先等前面的事件连接全部回收名额
	waitSubs(0)
	var conns []*http.Response
	for i := 0; i < MaxSubscribers; i++ {
		conns = append(conns, openEvents(t, addr, token, ""))
	}
	waitSubs(MaxSubscribers) // 等 32 条连接全部完成 Subscribe
	// 第 MaxSubscribers+1 个订阅（独立 client 不复用连接）
	limClient := &http.Client{}
	req4, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/v1/events", addr), nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	resp4, err := limClient.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("订阅者满应 503，得 %d", resp4.StatusCode)
	}
	for _, c := range conns {
		c.Body.Close()
	}
}
