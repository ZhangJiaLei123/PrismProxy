package store

import (
	"fmt"
	"testing"

	"prismproxy/internal/capture"
)

func flow(id string) *capture.Flow {
	return &capture.Flow{ID: id, State: capture.StatePending}
}

// collect 订阅并收集事件
func collect(s *Store) *[]Event {
	var events []Event
	s.Subscribe(func(ev Event) { events = append(events, ev) })
	return &events
}

func TestAddNewAndGet(t *testing.T) {
	s := New(3)
	events := collect(s)

	s.Add(flow("a"))
	s.Add(flow("b"))

	if got := len(s.List()); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("Get(a) miss")
	}
	if len(*events) != 2 || (*events)[0].Type != "new" || (*events)[1].Type != "new" {
		t.Fatalf("events = %+v", *events)
	}
}

func TestAddUpsertInPlace(t *testing.T) {
	s := New(3)
	events := collect(s)

	f := flow("a")
	s.Add(f)
	f.State = capture.StateDone
	f.BytesDown = 123
	s.Add(f) // 同 ID → update

	if got := len(s.List()); got != 1 {
		t.Fatalf("len = %d, want 1（upsert 不应追加）", got)
	}
	got, _ := s.Get("a")
	if got.State != capture.StateDone || got.BytesDown != 123 {
		t.Fatalf("flow = %+v", got)
	}
	if len(*events) != 2 || (*events)[1].Type != "update" || (*events)[1].Flow.ID != "a" {
		t.Fatalf("events = %+v", *events)
	}
}

func TestRingEvict(t *testing.T) {
	s := New(2)
	events := collect(s)

	s.Add(flow("a"))
	s.Add(flow("b"))
	s.Add(flow("c")) // 淘汰 a

	if _, ok := s.Get("a"); ok {
		t.Fatal("a 应被淘汰")
	}
	if _, ok := s.Get("c"); !ok {
		t.Fatal("c 应存在")
	}
	// 事件流：new(a) new(b) evict(a) new(c)
	if len(*events) != 4 || (*events)[2].Type != "evict" || (*events)[2].IDs[0] != "a" {
		t.Fatalf("events = %+v", *events)
	}
	// 淘汰后继续 upsert 旧 ID 视为新流
	s.Add(flow("a"))
	if len(s.List()) != 2 {
		t.Fatalf("len = %d, want 2", len(s.List()))
	}
}

func TestClearEmitsEvictAll(t *testing.T) {
	s := New(5)
	for i := 0; i < 4; i++ {
		s.Add(flow(fmt.Sprintf("f%d", i)))
	}
	events := collect(s)

	s.Clear()
	if len(s.List()) != 0 {
		t.Fatal("Clear 后应为空")
	}
	if len(*events) != 1 || (*events)[0].Type != "evict" || len((*events)[0].IDs) != 4 {
		t.Fatalf("events = %+v", *events)
	}
}

func TestCallbackOutsideLock(t *testing.T) {
	s := New(2)
	// 回调内再调 Store 方法：若在锁内执行会死锁
	s.Subscribe(func(ev Event) { _ = len(s.List()) })
	done := make(chan struct{})
	go func() { s.Add(flow("x")); close(done) }()
	<-done
}

func flowWithBody(id string, n int) *capture.Flow {
	f := flow(id)
	f.Request = &capture.Message{Body: make([]byte, n)}
	return f
}

func TestByteBudgetEvictOldest(t *testing.T) {
	s := New(100)
	s.SetLimits(100, 100) // 100 字节预算

	s.Add(flowWithBody("a", 60))
	s.Add(flowWithBody("b", 60)) // 合计 120 > 100 → 淘汰 a
	if _, ok := s.Get("a"); ok {
		t.Fatal("超预算应淘汰最旧的 a")
	}
	if _, ok := s.Get("b"); !ok {
		t.Fatal("b 应保留")
	}
	// 单条大于预算也保留（keepMin=1）
	s.Add(flowWithBody("c", 500))
	if got := len(s.List()); got != 1 {
		t.Fatalf("超大单条应淘汰其余后仅剩它自己，len = %d", got)
	}
	if _, ok := s.Get("c"); !ok {
		t.Fatal("c 应保留")
	}
}

func TestByteBudgetDeltaOnUpdate(t *testing.T) {
	s := New(100)
	s.SetLimits(100, 100)

	f := flowWithBody("a", 40)
	s.Add(f)
	s.Add(flowWithBody("b", 40)) // 合计 80，无淘汰
	if len(s.List()) != 2 {
		t.Fatal("未超预算不应淘汰")
	}
	// update 使 a 体变大 → 合计超预算 → 淘汰最旧（a 自己）
	f.Request.Body = make([]byte, 80)
	s.Add(f)
	if _, ok := s.Get("a"); ok {
		t.Fatal("a 膨胀后应作为最旧被淘汰")
	}
	if _, ok := s.Get("b"); !ok {
		t.Fatal("b 应保留")
	}
}

func TestSetLimitsShrinkCapacity(t *testing.T) {
	s := New(10)
	for _, id := range []string{"a", "b", "c", "d"} {
		s.Add(flow(id))
	}
	events := collect(s)
	s.SetLimits(2, 0) // 容量收缩到 2，字节不限
	if got := len(s.List()); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("收缩应淘汰最旧的 a")
	}
	if _, ok := s.Get("d"); !ok {
		t.Fatal("d 应保留")
	}
	if len(*events) != 1 || (*events)[0].Type != "evict" || len((*events)[0].IDs) != 2 {
		t.Fatalf("events = %+v", *events)
	}
	// 收缩后继续写入不丢不变量
	s.Add(flow("e"))
	s.Add(flow("f")) // 淘汰 c
	if _, ok := s.Get("c"); ok {
		t.Fatal("环形覆盖应淘汰 c")
	}
	if _, ok := s.Get("f"); !ok {
		t.Fatal("f 应存在")
	}
}

func TestPinnedSkippedByRingEvict(t *testing.T) {
	s := New(3)
	events := collect(s)

	s.Add(flow("a"))
	s.Add(flow("b"))
	s.Add(flow("c"))
	if _, err := s.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	s.Add(flow("d")) // 满环：最旧非置顶是 b（a 置顶跳过）

	if _, ok := s.Get("a"); !ok {
		t.Fatal("置顶的 a 不应被淘汰")
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("b 应被淘汰")
	}
	if _, ok := s.Get("d"); !ok {
		t.Fatal("d 应存在")
	}
	// 淘汰事件应指向 b 而非 a
	var evicted string
	for _, ev := range *events {
		if ev.Type == "evict" {
			evicted = ev.IDs[0]
		}
	}
	if evicted != "b" {
		t.Fatalf("evict 事件应淘汰 b，实际 %s（事件 %+v）", evicted, *events)
	}

	s.Add(flow("e")) // 下次淘汰 c
	if _, ok := s.Get("c"); ok {
		t.Fatal("c 应被淘汰")
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("置顶的 a 仍应保留")
	}
	if _, ok := s.Get("e"); !ok {
		t.Fatal("e 应存在")
	}
}

func TestPinnedAllFullDropsNew(t *testing.T) {
	s := New(2)
	s.Add(flow("a"))
	s.Add(flow("b"))
	if _, err := s.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPinned("b", true); err != nil {
		t.Fatal(err)
	}
	events := collect(s)

	s.Add(flow("c")) // 全部置顶 → 丢弃
	if got := len(s.List()); got != 2 {
		t.Fatalf("全置顶满环时新流应丢弃，len = %d", got)
	}
	if _, ok := s.Get("c"); ok {
		t.Fatal("c 应被丢弃")
	}
	if len(*events) != 0 {
		t.Fatalf("丢弃新流不应产生事件，events = %+v", *events)
	}

	// 取消 a 置顶后新流可入库，淘汰 a
	if _, err := s.SetPinned("a", false); err != nil {
		t.Fatal(err)
	}
	s.Add(flow("c"))
	if _, ok := s.Get("a"); ok {
		t.Fatal("取消置顶的 a 应可被淘汰")
	}
	if _, ok := s.Get("c"); !ok {
		t.Fatal("c 应存在")
	}
}

func TestClearKeepsPinned(t *testing.T) {
	s := New(5)
	s.Add(flow("a"))
	s.Add(flow("b"))
	s.Add(flow("c"))
	if _, err := s.SetPinned("b", true); err != nil {
		t.Fatal(err)
	}
	events := collect(s)

	s.Clear()
	if got := len(s.List()); got != 1 {
		t.Fatalf("Clear 应仅保留 1 条置顶流，len = %d", got)
	}
	if _, ok := s.Get("b"); !ok {
		t.Fatal("置顶的 b 应在 Clear 后保留")
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("非置顶的 a 应被清除")
	}
	if len(*events) != 1 || (*events)[0].Type != "evict" {
		t.Fatalf("events = %+v", *events)
	}
	gotIDs := map[string]bool{}
	for _, id := range (*events)[0].IDs {
		gotIDs[id] = true
	}
	if !gotIDs["a"] || !gotIDs["c"] || gotIDs["b"] {
		t.Fatalf("evict 应含 a,c 不含 b，实际 %v", gotIDs)
	}

	// Clear 后继续写入正常
	s.Add(flow("d"))
	if got := len(s.List()); got != 2 {
		t.Fatalf("Clear 后追加应正常，len = %d", got)
	}
}

func TestSetPinned(t *testing.T) {
	s := New(MaxPinned + 10)
	if _, err := s.SetPinned("nope", true); err == nil {
		t.Fatal("置顶不存在的流应报错")
	}

	s.Add(flow("a"))
	events := collect(s)

	f, err := s.SetPinned("a", true)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Pinned {
		t.Fatal("返回的 flow 应 Pinned=true")
	}
	if len(*events) != 1 || (*events)[0].Type != "update" || (*events)[0].Flow.ID != "a" {
		t.Fatalf("置顶应发 update 事件，events = %+v", *events)
	}

	// 重复置顶 = no-op
	if _, err := s.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 {
		t.Fatalf("重复置顶不应再发事件，events = %+v", *events)
	}

	// 取消置顶发 update
	if _, err := s.SetPinned("a", false); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 2 {
		t.Fatalf("取消置顶应再发 update 事件，events = %+v", *events)
	}

	// 上限
	for i := 0; i < MaxPinned; i++ {
		s.Add(flow(fmt.Sprintf("p%d", i)))
		if _, err := s.SetPinned(fmt.Sprintf("p%d", i), true); err != nil {
			t.Fatalf("置顶第 %d 条失败：%v", i+1, err)
		}
	}
	s.Add(flow("overflow"))
	if _, err := s.SetPinned("overflow", true); err == nil {
		t.Fatalf("置顶达 %d 上限应报错", MaxPinned)
	}
}

func TestUpdatePreservesPinned(t *testing.T) {
	s := New(3)
	s.Add(flow("a"))
	if _, err := s.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}

	// recorder 重发同 ID 的 flow（Pinned 位为 false），store 不应丢失置顶状态
	f := flow("a")
	f.State = capture.StateDone
	s.Add(f)

	got, _ := s.Get("a")
	if !got.Pinned {
		t.Fatal("upsert 更新不应清掉置顶位")
	}
	if got.State != capture.StateDone {
		t.Fatal("upsert 更新应生效")
	}
}

func TestByteBudgetRingInteraction(t *testing.T) {
	s := New(3)
	s.SetLimits(3, 50)
	// 写满触发环形覆盖（每条 10B，合计不超预算）
	s.Add(flowWithBody("a", 10))
	s.Add(flowWithBody("b", 10))
	s.Add(flowWithBody("c", 10))
	s.Add(flowWithBody("d", 10)) // 环形淘汰 a（next 旋到 1），合计 40B
	if _, ok := s.Get("a"); ok {
		t.Fatal("环形应淘汰 a")
	}
	// 加 e(40B)：先环形覆盖最旧的 b（next=1），再发现 60B>50 预算，淘汰现最旧的 c
	s.Add(flowWithBody("e", 40))
	for _, id := range []string{"b", "c"} {
		if _, ok := s.Get(id); ok {
			t.Fatalf("%s 应被淘汰", id)
		}
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("len = %d, want 2（剩 d,e）", got)
	}
	if _, ok := s.Get("d"); !ok {
		t.Fatal("d 应保留")
	}
	if _, ok := s.Get("e"); !ok {
		t.Fatal("e 应保留")
	}
	// 淘汰压实后继续写入不丢不变量
	s.Add(flowWithBody("f", 10)) // len=2<cap → 追加，合计 60B>50 → 淘汰 d
	if _, ok := s.Get("d"); ok {
		t.Fatal("超预算应淘汰 d")
	}
}
