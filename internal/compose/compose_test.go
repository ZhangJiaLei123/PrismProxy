package compose

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"prismproxy/internal/capture"
	"prismproxy/internal/store"
)

// M9 回归：Sender.Gen 非 nil 时重发流必须打上项目代际——否则项目切换后
// onPersistEvent 的代际比对（flow.Gen != writer.gen）会把 Composer 流永久排除在落盘外。
func TestSenderStampsProjectGen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := store.New(100)
	genCalls := 0
	s := &Sender{
		NewID: func() string { return "compose-gen-1" },
		Store: st,
		Gen:   func() uint64 { genCalls++; return 42 },
	}
	flow, err := s.Send(context.Background(), Request{URL: srv.URL + "/a"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if genCalls != 1 {
		t.Fatalf("Gen 应恰好被调用 1 次，实际 %d 次", genCalls)
	}
	if flow.Gen != 42 {
		t.Fatalf("重发流应打标代际 42，实际 %d", flow.Gen)
	}
	if _, ok := st.Get("compose-gen-1"); !ok {
		t.Fatal("重发流应已入库")
	}
}

// Gen 为 nil（测试/单项目场景）不打标，Gen 保持零值
func TestSenderNilGenKeepsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := store.New(100)
	s := &Sender{NewID: func() string { return "compose-gen-2" }, Store: st}
	flow, err := s.Send(context.Background(), Request{URL: srv.URL + "/a"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if flow.Gen != 0 {
		t.Fatalf("Gen 为 nil 时不应打标，实际 %d", flow.Gen)
	}
	if flow.Source != capture.SourceComposer {
		t.Fatalf("来源应为 composer，实际 %q", flow.Source)
	}
}
