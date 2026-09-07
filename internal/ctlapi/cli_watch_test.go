package ctlapi

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureStdout 临时把 os.Stdout 重定向到管道，返回读取累积文本的函数
func captureStdout(t *testing.T) (stopAndRead func() string) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			buf.WriteString(sc.Text())
			buf.WriteByte('\n')
		}
		close(done)
	}()
	return func() string {
		os.Stdout = orig
		w.Close()
		<-done
		return buf.String()
	}
}

// watchService fakeService 基础上让 ListFlows 返回一条快照流
type watchService struct {
	fakeService
	filterSeen string
}

func (f *watchService) ListFlows(limit int, filter string) any {
	f.mu.Lock()
	f.filterSeen = filter
	f.mu.Unlock()
	// 与真实 ctlService.ListFlows 一致：直接返回流数组
	return []map[string]any{{"host": "snap.example.com", "url": "https://snap.example.com/"}}
}

// 先订阅后快照：ndjson 输出首行 snapshot，随后收到 upsert/evict/status 增量帧
func TestWatchOnceNdjsonSnapshotThenEvents(t *testing.T) {
	svc := &watchService{}
	srv, addr, token := startTestServer(t, svc)
	dir := filepath.Dir(srv.endpoint)

	read := captureStdout(t)
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		runErr <- watchOnce(ctx, dir, addr, token, watchOpts{filter: "snap", status: true, format: "ndjson"})
	}()

	// 等快照与 seed 帧输出
	time.Sleep(400 * time.Millisecond)
	// 推送增量帧（host 含 snap 以匹配 --filter snap；evict 不受 filter 影响）
	srv.Hub().Publish("flows", FlowsUpsert{Type: "upsert", Flows: []Filterable{
		fakeFlow{Host: "inc.snap.example.com", URL: "https://inc.snap.example.com/p", Path: "/p"},
	}})
	srv.Hub().Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"id-99"}})
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-runErr
	out := read()

	if !strings.Contains(out, `"event":"snapshot"`) || !strings.Contains(out, "snap.example.com") {
		t.Fatalf("应先输出 snapshot 快照帧: %s", out)
	}
	if !strings.Contains(out, "inc.snap.example.com") {
		t.Fatalf("应收到 upsert 增量帧: %s", out)
	}
	if !strings.Contains(out, "id-99") || !strings.Contains(out, `"evict"`) {
		t.Fatalf("应收到 evict 帧: %s", out)
	}
	// --status 订阅：seed 帧（启动即推）+ 后续 status
	if !strings.Contains(out, `"event":"status"`) {
		t.Fatalf("--status 应输出 status 帧（seed）: %s", out)
	}
	// 快照带同一 filter（基线与增量口径一致）
	if svc.filterSeen != "snap" {
		t.Fatalf("快照请求应带 filter=snap，得 %q", svc.filterSeen)
	}
	// snapshot 必须在增量之前（先订阅后快照的衔接）
	if strings.Index(out, "snapshot") > strings.Index(out, "inc.snap.example.com") {
		t.Fatalf("snapshot 应在增量帧之前输出: %s", out)
	}
}

// sse 格式：整段为合法 SSE 文本（event: snapshot + 透传服务端帧）
func TestWatchOnceSSEFormat(t *testing.T) {
	svc := &watchService{}
	srv, addr, token := startTestServer(t, svc)
	dir := filepath.Dir(srv.endpoint)

	read := captureStdout(t)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- watchOnce(ctx, dir, addr, token, watchOpts{format: "sse"}) }()

	time.Sleep(400 * time.Millisecond)
	srv.Hub().Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"e1"}})
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-runErr
	out := read()

	if !strings.Contains(out, "event: open") {
		t.Fatalf("sse 格式应透传 open 帧: %s", out)
	}
	if !strings.Contains(out, "event: snapshot") {
		t.Fatalf("sse 格式应有 CLI 合成的 snapshot 帧: %s", out)
	}
	if !strings.Contains(out, "event: flows") {
		t.Fatalf("sse 格式应透传 flows 帧: %s", out)
	}
}

// 非法 --format 报错（退出码 2）
func TestWatchBadFormat(t *testing.T) {
	if _, err := parseWatchOpts([]string{"flows", "watch", "--format", "xml"}); err == nil {
		t.Fatal("非法 format 应报错")
	}
}

// 重连重读 endpoint：watchOnce 失败后由 runWatch 重读配置；此处验证 token 变更场景
// （新实例 endpoint 文件覆盖新 token）watchOnce 用新凭证能连通。
func TestWatchReconnectWithNewToken(t *testing.T) {
	svc := &watchService{}
	srv, addr, _ := startTestServer(t, svc)
	dir := filepath.Dir(srv.endpoint)

	// 用错误 token 直连应失败
	if err := watchOnce(context.Background(), dir, addr, "wrong-token", watchOpts{}); err == nil {
		t.Fatal("错误 token 应导致订阅失败")
	}
	// 重读 endpoint 文件（不传 token）应能成功连通
	ctx, cancel := context.WithCancel(context.Background())
	read := captureStdout(t)
	runErr := make(chan error, 1)
	go func() { runErr <- watchOnce(ctx, dir, "", "", watchOpts{format: "ndjson"}) }()
	time.Sleep(400 * time.Millisecond)
	cancel()
	<-runErr
	out := read()
	if !strings.Contains(out, "snapshot") {
		t.Fatalf("重读 endpoint 应用新 token 连通并输出 snapshot: %s", out)
	}
}
