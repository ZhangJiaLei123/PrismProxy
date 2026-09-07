package app

import (
	"path/filepath"
	"testing"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/settings"
)

// persistWait 轮询等待历史流回查就绪（异步批量落盘）
func persistWait(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", msg)
}

// 端到端：开启持久化 → 流量落盘 → 模拟重启 → 历史加载 + body 惰性回查
func TestPersistEndToEnd(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "prism.db")

	// ---- 会话一：开启持久化并抓一条流 ----
	a1 := newTestApp(t)
	a1.cfgDir = dir
	if err := a1.applyPersist(settings.PersistConfig{Enabled: true, DBPath: dbPath, RetainDays: 7, MaxMB: 100}); err != nil {
		t.Fatalf("applyPersist: %v", err)
	}

	f := capture.NewFlow("persist-1")
	f.State = capture.StateDone
	f.Scheme = "https"
	f.ServerAddr = "example.com"
	f.Process = &capture.ProcessInfo{PID: 7, Name: "curl.exe"}
	f.Request = &capture.Message{Method: "GET", URL: "https://example.com/a?b=1", Proto: "HTTP/1.1"}
	f.Response = &capture.Message{StatusCode: 200, Proto: "HTTP/1.1", Body: []byte(`{"hello":"history"}`)}
	a1.st.Add(f)

	// 等待异步落盘
	persistWait(t, func() bool { return a1.currentPersist().Written() >= 1 }, "流落盘")
	a1.stopPersist()

	// ---- 会话二：新 App 指向同一 DB，initPersist 应加载历史 ----
	a2 := newTestApp(t)
	a2.cfgDir = dir
	a2.cfg.Persist = settings.PersistConfig{Enabled: true, DBPath: dbPath, RetainDays: 7, MaxMB: 100}
	a2.initPersist()
	defer a2.stopPersist()

	meta := a2.ListFlows()
	var found bool
	for _, m := range meta {
		if m.ID == "persist-1" {
			found = true
			if !m.Historical || m.Source != capture.SourceHistory {
				t.Fatalf("历史流标记错误: historical=%v source=%q", m.Historical, m.Source)
			}
			if m.Host != "example.com" || m.Status != 200 {
				t.Fatalf("历史流元数据异常: host=%q status=%d", m.Host, m.Status)
			}
		}
	}
	if !found {
		t.Fatalf("重启后未加载到历史流 persist-1，共 %d 条", len(meta))
	}

	// body 惰性回查：内存流 body 为空，GetFlowBody 经 DB 还原
	detail, err := a2.GetFlowDetail("persist-1")
	if err != nil {
		t.Fatalf("GetFlowDetail: %v", err)
	}
	if detail.RespBodySize != len(`{"hello":"history"}`) {
		t.Fatalf("历史流响应正文大小 = %d（应来自 BodyLen）", detail.RespBodySize)
	}
	body, err := a2.GetFlowBody("persist-1", "resp")
	if err != nil {
		t.Fatalf("GetFlowBody 惰性回查: %v", err)
	}
	if string(body.Body) != `{"hello":"history"}` {
		t.Fatalf("历史流 body 回查异常: %q", body.Body)
	}
}

// 关闭持久化不应影响内存 store；DB 文件保留
func TestPersistDisableKeepsStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "prism.db")

	a := newTestApp(t)
	a.cfgDir = dir
	if err := a.applyPersist(settings.PersistConfig{Enabled: true, DBPath: dbPath}); err != nil {
		t.Fatalf("applyPersist enable: %v", err)
	}
	f := capture.NewFlow("persist-off-1")
	f.State = capture.StateDone
	f.ServerAddr = "x.com"
	f.Request = &capture.Message{Method: "GET", URL: "http://x.com/"}
	a.st.Add(f)
	persistWait(t, func() bool { return a.currentPersist().Written() >= 1 }, "流落盘")

	// 关闭
	if err := a.applyPersist(settings.PersistConfig{Enabled: false}); err != nil {
		t.Fatalf("applyPersist disable: %v", err)
	}
	if a.currentPersist() != nil {
		t.Fatal("关闭后 writer 应为 nil")
	}
	// 内存流仍在
	if _, ok := a.st.Get("persist-off-1"); !ok {
		t.Fatal("关闭持久化不应清除内存流")
	}
	a.stopPersist()
}

// 回归：已运行持久化 + 历史流不在内存（清空/淘汰）后再次 applyPersist，不得死锁。
// 根因曾为 applyPersist 持 pmu 调 st.Add → emit 同步回调 onPersistEvent 再取 pmu（不可重入）。
func TestPersistReapplyNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "prism.db")
	pc := settings.PersistConfig{Enabled: true, DBPath: dbPath, RetainDays: 7, MaxMB: 100}

	a := newTestApp(t)
	a.cfgDir = dir
	if err := a.applyPersist(pc); err != nil {
		t.Fatalf("applyPersist 首次: %v", err)
	}
	f := capture.NewFlow("reapply-1")
	f.State = capture.StateDone
	f.ServerAddr = "example.com"
	f.Request = &capture.Message{Method: "GET", URL: "https://example.com/a"}
	f.Response = &capture.Message{StatusCode: 200, Body: []byte(`{"k":"v"}`)}
	a.st.Add(f)
	persistWait(t, func() bool { return a.currentPersist().Written() >= 1 }, "落盘")

	// 清空内存（DB 保留），制造"历史不在内存"；再关闭 writer（模拟用户关→开持久化，
	// 走"由关到开"重连路径，会 LoadRecent 补载历史 → st.Add，是死锁触发点）
	a.st.Clear()
	if err := a.applyPersist(settings.PersistConfig{Enabled: false}); err != nil {
		t.Fatalf("applyPersist 关闭: %v", err)
	}

	// 重新开启（重连 + 补载历史）：必须在有限时间内返回，不得重入死锁
	done := make(chan error, 1)
	go func() { done <- a.applyPersist(pc) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("applyPersist 重新开启: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("applyPersist 死锁：3s 未返回（pmu 重入）")
	}

	// 补载后历史流应回到内存
	meta := a.ListFlows()
	var found bool
	for _, m := range meta {
		if m.ID == "reapply-1" && m.Historical {
			found = true
		}
	}
	if !found {
		t.Fatal("补载历史后应在列表中看到 reapply-1（历史标记）")
	}
	a.stopPersist()
}

// 回归：DB 路径不变、仅保留策略变化时不应重连 writer（复用同一实例）。
func TestPersistRetentionUpdateNoReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "prism.db")
	pc := settings.PersistConfig{Enabled: true, DBPath: dbPath, RetainDays: 7, MaxMB: 100}

	a := newTestApp(t)
	a.cfgDir = dir
	if err := a.applyPersist(pc); err != nil {
		t.Fatalf("applyPersist: %v", err)
	}
	w1 := a.currentPersist()
	if w1 == nil {
		t.Fatal("writer 应已开启")
	}
	// 同路径、改 retainDays/maxMB：不应重连
	pc2 := pc
	pc2.RetainDays = 3
	pc2.MaxMB = 200
	if err := a.applyPersist(pc2); err != nil {
		t.Fatalf("applyPersist 更新保留策略: %v", err)
	}
	if a.currentPersist() != w1 {
		t.Fatal("DB 路径不变时应复用同一 writer（不重连）")
	}
	a.stopPersist()
}
