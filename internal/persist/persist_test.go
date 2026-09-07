package persist

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prismproxy/internal/capture"
)

func testFlow(id, host string, state capture.FlowState, respBody string) *capture.Flow {
	f := capture.NewFlow(id)
	f.State = state
	f.Scheme = "https"
	f.ServerAddr = host
	f.Process = &capture.ProcessInfo{PID: 123, Name: "curl.exe", Path: `C:\curl.exe`}
	f.Request = &capture.Message{
		Method: "GET",
		URL:    "https://" + host + "/path?q=1",
		Proto:  "HTTP/1.1",
		Header: http.Header{"User-Agent": {"test-agent"}},
	}
	f.Response = &capture.Message{
		StatusCode: 200,
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
	if respBody != "" {
		f.Response.Body = []byte(respBody)
	}
	return f
}

// waitWritten 轮询等待 writer 落盘达 n 条（批量异步刷盘）
func waitWritten(t *testing.T, w *Writer, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w.Written() >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待落盘 %d 条超时，实际 %d（丢弃 %d）", n, w.Written(), w.Dropped())
}

func TestWriteAndLoadRecent(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "prism.db"), 0, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Start()
	defer w.Close()

	body := strings.Repeat(`{"k":"v"}`, 100) // >256B 触发 zstd
	f := testFlow("f1", "example.com", capture.StateDone, body)
	f.Request.Body = []byte("request-body")
	w.Enqueue(f)
	// 非终态流不应落盘
	w.Enqueue(testFlow("f-pending", "pending.com", capture.StateStreaming, ""))
	waitWritten(t, w, 1)

	hist, err := w.LoadRecent(10)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("历史流数 = %d，期望 1（pending 不落盘）", len(hist))
	}
	h := hist[0]
	if h.ID != "f1" || h.Source != capture.SourceHistory {
		t.Fatalf("历史流标记错误: id=%s source=%s", h.ID, h.Source)
	}
	if h.Pinned {
		t.Fatal("历史流不应带置顶（置顶为会话内状态）")
	}
	if h.Request == nil || h.Request.Method != "GET" || h.Process == nil || h.Process.Name != "curl.exe" {
		t.Fatalf("元数据 JSON 还原不完整: %+v", h)
	}
	// body 不入 JSON（BodyLen 记录长度，Body 为 nil）
	if h.Response.Body != nil {
		t.Fatal("历史流元数据不应含 body 字节")
	}
	if h.Response.BodyLen != len(body) {
		t.Fatalf("BodyLen = %d，期望 %d", h.Response.BodyLen, len(body))
	}

	// body 惰性回查：resp 应 zstd 解压还原，req 原样
	gotResp, err := w.LoadBody("f1", "resp")
	if err != nil {
		t.Fatalf("LoadBody resp: %v", err)
	}
	if string(gotResp) != body {
		t.Fatalf("resp body 解压不匹配，长度 %d vs %d", len(gotResp), len(body))
	}
	gotReq, err := w.LoadBody("f1", "req")
	if err != nil {
		t.Fatalf("LoadBody req: %v", err)
	}
	if string(gotReq) != "request-body" {
		t.Fatalf("req body = %q", gotReq)
	}
	if _, err := w.LoadBody("f1", "nope"); err != nil {
		t.Fatalf("不存在 kind 应返回 nil,nil: %v", err)
	}
}

func TestUpsertUpdates(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "prism.db"), 0, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Start()
	defer w.Close()

	// 同 ID 两次入队（先 error 态后 done 态），upsert 后应为最终态
	f1 := testFlow("upd", "upd.com", capture.StateError, "")
	f1.Err = "boom"
	w.Enqueue(f1)
	waitWritten(t, w, 1)
	f2 := testFlow("upd", "upd.com", capture.StateDone, "final")
	w.Enqueue(f2)
	waitWritten(t, w, 2)

	hist, err := w.LoadRecent(10)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("upsert 后流数 = %d，期望 1", len(hist))
	}
	if hist[0].State != capture.StateDone {
		t.Fatalf("最终态 = %s，期望 done", hist[0].State)
	}
	body, _ := w.LoadBody("upd", "resp")
	if string(body) != "final" {
		t.Fatalf("upsert 后 body = %q，期望 final", body)
	}
}

func TestQueueDropWhenFull(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "prism.db"), 0, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Start 后暂停消费（不排空队列），灌满 queueCap 后再入队应丢弃、不阻塞
	w.Start()
	w.setPaused(true)
	for i := 0; i < queueCap+50; i++ {
		w.Enqueue(testFlow(fmt.Sprintf("q%d", i), "x.com", capture.StateDone, ""))
	}
	if d := w.Dropped(); d < 50 {
		t.Fatalf("丢弃数 = %d，期望 >=50", d)
	}
	w.setPaused(false)
	w.Close()
}

func TestRetentionByDays(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "prism.db"), 1, 0) // 保留 1 天
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Start()
	defer w.Close()

	old := testFlow("old", "old.com", capture.StateDone, "old-body")
	old.Timing.Start = time.Now().Add(-72 * time.Hour) // 3 天前
	fresh := testFlow("fresh", "fresh.com", capture.StateDone, "fresh-body")
	w.Enqueue(old)
	w.Enqueue(fresh)
	waitWritten(t, w, 2)

	if err := w.Retention(); err != nil {
		t.Fatalf("Retention: %v", err)
	}
	hist, err := w.LoadRecent(10)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(hist) != 1 || hist[0].ID != "fresh" {
		ids := []string{}
		for _, h := range hist {
			ids = append(ids, h.ID)
		}
		t.Fatalf("按天清理后剩余 %v，期望仅 fresh", ids)
	}
	// 旧流 body 应作为孤儿被清
	if b, _ := w.LoadBody("old", "resp"); b != nil {
		t.Fatalf("旧流 body 应被清理，仍有 %d 字节", len(b))
	}
}

func TestRetentionBySize(t *testing.T) {
	dir := t.TempDir()
	// maxMB 极小（1MB 下限以下），制造足够多流后 enforceSize 应删最旧
	w, err := Open(filepath.Join(dir, "prism.db"), 0, 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Start()
	defer w.Close()

	big := make([]byte, 200*1024) // 200KB 不可压缩随机体（zstd 压不动，保证 DB 体积真实增长）
	if _, err := rand.Read(big); err != nil {
		t.Fatalf("rand: %v", err)
	}
	for i := 0; i < 20; i++ { // 20 × 200KB ≈ 4MB > 1MB 上限
		f := testFlow(fmt.Sprintf("sz%d", i), "sz.com", capture.StateDone, string(big))
		w.Enqueue(f)
		time.Sleep(time.Millisecond) // 保证 started_at 递增
	}
	waitWritten(t, w, 20)
	// 触发一次 checkpoint 让 WAL 并入主库，得到"峰值"主文件大小（此时应已超 1MB）
	if _, err := w.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	peak := fileSize(t, w.Path())
	if peak < int64(1<<20) {
		t.Fatalf("峰值主文件 %d 字节未超 1MB，测试数据不足", peak)
	}

	if err := w.Retention(); err != nil {
		t.Fatalf("Retention: %v", err)
	}
	hist, err := w.LoadRecent(200)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(hist) >= 20 {
		t.Fatalf("体积清理后仍有 %d 条，期望删除部分最旧流", len(hist))
	}
	if len(hist) > 0 && hist[len(hist)-1].ID == "" {
		t.Fatal("最后一条应为最新流")
	}
	// 关键回归：auto_vacuum=INCREMENTAL + incremental_vacuum 后，主文件（+WAL）必须真回落到阈值
	// 以下（曾因 DELETE 不缩小文件导致 enforceSize 一路删到表空）。
	after := fileSize(t, w.Path())
	if after > int64(1<<20) {
		t.Fatalf("体积清理后 DB 仍 %d 字节（>1MB），未回落；峰值 %d", after, peak)
	}
	t.Logf("体积上限生效：峰值 %d → 清理后 %d 字节（保留 %d 条最新流）", peak, after, len(hist))
}

// fileSize 主文件 + -wal 合计字节数
func fileSize(t *testing.T, main string) int64 {
	t.Helper()
	var n int64
	if fi, err := os.Stat(main); err == nil {
		n += fi.Size()
	}
	if fi, err := os.Stat(main + "-wal"); err == nil {
		n += fi.Size()
	}
	return n
}

func TestCloseFlushesQueue(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(filepath.Join(dir, "prism.db"), 0, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Start()
	// 入队 3 条后立即 Close（不足批量阈值），Close 应同步刷盘
	for i := 0; i < 3; i++ {
		w.Enqueue(testFlow("c"+string(rune('a'+i)), "c.com", capture.StateDone, "x"))
	}
	w.Close()

	// 重开只读验证已落盘
	w2, err := Open(filepath.Join(dir, "prism.db"), 0, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w2.Close()
	hist, err := w2.LoadRecent(10)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("Close 刷盘后重开流数 = %d，期望 3", len(hist))
	}
}
