package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"prismproxy/internal/proxy"
)

func TestSendComposed_Basic(t *testing.T) {
	var gotMethod, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	d, err := a.SendComposed(&ComposedRequest{
		Method:  "POST",
		URL:     srv.URL + "/v1/test?x=1",
		Headers: []ComposedHeader{{Key: "Authorization", Value: "Bearer tok"}, {Key: "Connection", Value: "close"}},
		Body:    `{"k":"v"}`,
	})
	if err != nil {
		t.Fatalf("SendComposed 失败: %v", err)
	}
	if gotMethod != "POST" || gotAuth != "Bearer tok" || gotBody != `{"k":"v"}` {
		t.Fatalf("上游收到的请求异常: method=%q auth=%q body=%q", gotMethod, gotAuth, gotBody)
	}
	if d.Status != 201 || d.Source != "composer" {
		t.Fatalf("响应/来源异常: status=%d source=%q", d.Status, d.Source)
	}
	if ct := d.RespHeader["Content-Type"]; len(ct) != 1 || ct[0] != "application/json" {
		t.Fatalf("响应首部缺失: %#v", d.RespHeader)
	}
	// 已入 store（可被 GetFlowBody/GetFlowDetail 复用）
	body, err := a.GetFlowBody(d.ID, "resp")
	if err != nil {
		t.Fatal(err)
	}
	if string(body.Body) != `{"ok":true}` {
		t.Fatalf("响应体异常: %q", body.Body)
	}
	reqBody, err := a.GetFlowBody(d.ID, "req")
	if err != nil {
		t.Fatal(err)
	}
	if string(reqBody.Raw) != `{"k":"v"}` {
		t.Fatalf("请求体应原样落库: %q", reqBody.Raw)
	}
}

// TestSendComposed_Gzip 保真透传 Content-Encoding，展示层解压
func TestSendComposed_Gzip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gw := gzip.NewWriter(w)
		gw.Write([]byte(`{"gzip":true}`))
		gw.Close()
	}))
	defer srv.Close()

	a := newTestApp(t)
	d, err := a.SendComposed(&ComposedRequest{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.GetFlowBody(d.ID, "resp")
	if err != nil {
		t.Fatal(err)
	}
	if p.Encoding != "gzip" || string(p.Body) != `{"gzip":true}` {
		t.Fatalf("gzip 保真/解压异常: encoding=%q body=%q", p.Encoding, p.Body)
	}
}

// TestSendComposed_NetworkError 网络失败落 error 态 Flow 且返回 err
func TestSendComposed_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // 关闭端口制造连接拒绝

	a := newTestApp(t)
	before := len(a.st.List())
	_, err := a.SendComposed(&ComposedRequest{URL: srv.URL + "/x"})
	if err == nil {
		t.Fatal("连接拒绝应返回错误")
	}
	if len(a.st.List()) != before+1 {
		t.Fatalf("网络失败也应落一条 Flow: before=%d after=%d", before, len(a.st.List()))
	}
	f := a.st.List()[0]
	if f.State != "error" || f.Source != "composer" || f.Err == "" {
		t.Fatalf("error 态 composer 流异常: state=%s source=%s err=%q", f.State, f.Source, f.Err)
	}
}

func TestSendComposed_InvalidRequest(t *testing.T) {
	a := newTestApp(t)
	// 非 http/https
	if _, err := a.SendComposed(&ComposedRequest{URL: "ftp://example.com"}); err == nil {
		t.Fatal("非法 scheme 应报错")
	}
	// 空请求
	if _, err := a.SendComposed(nil); err == nil {
		t.Fatal("空请求应报错")
	}
	if len(a.st.List()) != 0 {
		t.Fatalf("参数校验失败不应落库: %d", len(a.st.List()))
	}
}

// TestGuardUpstreamLoop_Loopback 上游指向本工具时降级直连（compose 与 proxy 共用防护）
func TestGuardUpstreamLoop_Loopback(t *testing.T) {
	if got := proxy.GuardUpstreamLoop("127.0.0.1:9090", "127.0.0.1", 9090); got != "" {
		t.Fatalf("指向自身的上游应被环路防护清空: %q", got)
	}
	if got := proxy.GuardUpstreamLoop("127.0.0.1:1080", "127.0.0.1", 9090); got != "127.0.0.1:1080" {
		t.Fatalf("非自身上游应保留: %q", got)
	}
}
