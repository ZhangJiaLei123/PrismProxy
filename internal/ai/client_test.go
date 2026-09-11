package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sseHandler 返回一个按帧序列输出 SSE 的 handler；frames 每项作为一行 data 帧体，
// "[DONE]" 用常量 doneFrame 表示；记录收到的请求体与关键头供断言。
const doneFrame = "[DONE]"

func sseHandler(frames []string, gotReq *chatRequest, gotAuth, gotAccept *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if gotReq != nil {
			_ = json.NewDecoder(r.Body).Decode(gotReq)
		}
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		if gotAccept != nil {
			*gotAccept = r.Header.Get("Accept")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, fr := range frames {
			fmt.Fprintf(w, "data: %s\n\n", fr)
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

// TestStreamFrames 帧序解析：reasoning 增量 + 正文增量 + 杂帧跳过 + [DONE] 收尾；请求体契约断言。
func TestStreamFrames(t *testing.T) {
	var gotReq chatRequest
	var gotAuth, gotAccept string
	frames := []string{
		`{"choices":[{"delta":{"reasoning_content":"思考中"}}]}`,
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"delta":{"content":"，世界"}}]}`,
		`{"event":"ping"}`, // 无法按 SSE 帧解析的杂帧应跳过
		doneFrame,
	}
	srv := httptest.NewServer(sseHandler(frames, &gotReq, &gotAuth, &gotAccept))
	defer srv.Close()

	var deltas []Delta
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "sk-test", Model: "m1", Temperature: 0.1})
	err := c.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, func(d Delta) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []Delta{{Reason: "思考中"}, {Text: "你好"}, {Text: "，世界"}}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas=%v want %v", deltas, want)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization=%q", gotAuth)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept=%q", gotAccept)
	}
	if !gotReq.Stream || gotReq.Model != "m1" || gotReq.Temperature != 0.1 {
		t.Fatalf("req=%+v", gotReq)
	}
	if gotReq.StreamOptions == nil || gotReq.StreamOptions.IncludeUsage {
		t.Fatalf("stream_options=%+v", gotReq.StreamOptions)
	}
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != RoleUser || gotReq.Messages[0].Content != "hi" {
		t.Fatalf("messages=%+v", gotReq.Messages)
	}
}

// TestStreamNoDone 上游不发 [DONE] 直接关连接：视为正常完成。
func TestStreamNoDone(t *testing.T) {
	frames := []string{`{"choices":[{"delta":{"content":"尾帧"}}]}`}
	srv := httptest.NewServer(sseHandler(frames, nil, nil, nil))
	defer srv.Close()

	var deltas []Delta
	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	if err := c.Stream(context.Background(), nil, func(d Delta) { deltas = append(deltas, d) }); err != nil {
		t.Fatalf("无 [DONE] 应正常完成，got %v", err)
	}
	if len(deltas) != 1 || deltas[0].Text != "尾帧" {
		t.Fatalf("deltas=%v", deltas)
	}
}

// TestStreamHTTPError 非 200：error.message 提取与原文退化。
func TestStreamHTTPError(t *testing.T) {
	mk := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(body))
		}))
	}

	srv := mk(`{"error":{"message":"Invalid API key"}}`)
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	err := c.Stream(context.Background(), nil, func(Delta) {})
	if err == nil || !strings.Contains(err.Error(), "服务商返回 401") || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("got %v", err)
	}

	srv2 := mk("plain upstream failure")
	defer srv2.Close()
	c2 := NewClient(Config{BaseURL: srv2.URL, Model: "m"})
	err2 := c2.Stream(context.Background(), nil, func(Delta) {})
	if err2 == nil || !strings.Contains(err2.Error(), "服务商返回 401：plain upstream failure") {
		t.Fatalf("got %v", err2)
	}
}

// TestStreamErrorFrame 上游 200 + error 帧：识别为业务错误。
func TestStreamErrorFrame(t *testing.T) {
	frames := []string{`{"error":{"message":"配额不足","type":"quota_exceeded"}}`}
	srv := httptest.NewServer(sseHandler(frames, nil, nil, nil))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	err := c.Stream(context.Background(), nil, func(Delta) {})
	if err == nil || !strings.Contains(err.Error(), "服务商返回错误：配额不足") {
		t.Fatalf("got %v", err)
	}
}

// TestStreamFirstChunkTimeout 首块超时：Timeout=4s → 阈值 1s；服务器 3s 后才发数据。
func TestStreamFirstChunkTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // 读掉 body，使 server 能监测客户端断开
		select {
		case <-time.After(3 * time.Second):
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n")
		case <-r.Context().Done(): // 客户端超时断开，handler 及时退出
		}
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m", Timeout: 4 * time.Second})
	start := time.Now()
	err := c.Stream(context.Background(), nil, func(Delta) {})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "连接服务商超时") || !strings.Contains(err.Error(), "1 秒内未收到首个响应") {
		t.Fatalf("got %v", err)
	}
	if elapsed < 900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("elapsed=%v，应在首块阈值 1s 附近", elapsed)
	}
}

// TestStreamCtxCancel 用户主动停止：返回 ctx.Err()（编排层转为「已停止」），而非超时/连接错误。
func TestStreamCtxCancel(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // 读掉 body，使 server 能监测客户端断开
		select {                           // done 兜底：部分平台上断开监测有延迟
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	c := NewClient(Config{BaseURL: srv.URL, Model: "m"}) // 默认 ft=30s，不会先触发
	err := c.Stream(ctx, nil, func(Delta) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled，got %v", err)
	}
	close(done) // 放行 handler，确保 srv.Close() 不阻塞
}

// TestProbe 非流式探测：请求契约（stream=false、max_tokens=8）、回复文本、鉴权头与错误分支。
func TestProbe(t *testing.T) {
	var gotReq chatRequest
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"}}]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "sk-x", Model: "m", Temperature: 0.7})
	text, err := c.Probe(context.Background(), []Message{{Role: RoleUser, Content: "ping"}})
	if err != nil || text != "pong" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if gotReq.Stream || gotReq.MaxTokens != 8 || gotReq.Temperature != 0.7 {
		t.Fatalf("req=%+v", gotReq)
	}
	if gotAuth != "Bearer sk-x" {
		t.Fatalf("Authorization=%q", gotAuth)
	}

	// APIKey 空：不发 Authorization（Ollama 场景）
	c2 := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := c2.Probe(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("空 APIKey 不应发 Authorization，got %q", gotAuth)
	}

	// 200 + error 帧
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"无可用渠道"}}`)
	}))
	defer srvErr.Close()
	if _, err := NewClient(Config{BaseURL: srvErr.URL, Model: "m"}).Probe(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "服务商返回错误：无可用渠道") {
		t.Fatalf("got %v", err)
	}

	// 空结果
	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srvEmpty.Close()
	if _, err := NewClient(Config{BaseURL: srvEmpty.URL, Model: "m"}).Probe(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "服务商返回空结果") {
		t.Fatalf("got %v", err)
	}
}

// TestNormalizeBaseURL 地址归一化镜像 settings.NormalizeAIBaseURL 语义。
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"  http://a.com/  ", "http://a.com/v1"},
		{"http://a.com/", "http://a.com/v1"},
		{"http://a.com", "http://a.com/v1"},
		{"http://a.com/v1", "http://a.com/v1"},
		{"http://a.com/v1/", "http://a.com/v1"},
		{"http://a.com/api/v1", "http://a.com/api/v1"},
		{"http://a.com/api/paas/v4", "http://a.com/api/paas/v4"}, // 智谱 GLM v4 形态
		{"http://a.com/v2", "http://a.com/v2"},                   // 其他版本段保留
		{"http://a.com/v1beta", "http://a.com/v1beta/v1"},        // 非纯数字版本段仍补 /v1
	}
	for _, tc := range cases {
		if got := normalizeBaseURL(tc.in); got != tc.want {
			t.Errorf("normalizeBaseURL(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	c := NewClient(Config{BaseURL: "http://a.com/"})
	if got := c.endpoint(); got != "http://a.com/v1/chat/completions" {
		t.Fatalf("endpoint=%q", got)
	}
}

// TestProxyPassthrough 出站代理透传：请求经代理服务器中转到达目标。
func TestProxyPassthrough(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"via-proxy"}}]}`)
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		resp, err := http.DefaultTransport.RoundTrip(r) // 正向代理：绝对 URI 直转目标
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	c := NewClient(Config{BaseURL: target.URL, Model: "m", ProxyURL: proxy.URL})
	text, err := c.Probe(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err != nil || text != "via-proxy" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("代理未被经过，hits=%d", hits.Load())
	}
}

// TestListModels 模型列表：GET 路径与归一化（补 /v1、版本段保留）、Bearer 头（空 key 不发）、
// 去空白去重、非 200 错误包装、空列表报错。
func TestListModels(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"a"},{"id":"a"},{"id":"  "},{"id":"b"}]}`)
	}))
	defer srv.Close()

	// BaseURL 不带 /v1：自动补 /v1/models；key 非空发 Bearer；重复与空白项剔除
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "sk-x", Model: "m"})
	got, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("models=%v want [a b]", got)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotAuth != "Bearer sk-x" {
		t.Fatalf("Authorization=%q", gotAuth)
	}

	// BaseURL 末段 /v<数字>（Ollama /v1、火山 Ark /api/v3）：保留不补 /v1
	c2 := NewClient(Config{BaseURL: "http://ark.example/api/v3", Model: "m"})
	if got := normalizeBaseURL(c2.cfg.BaseURL) + "/models"; got != "http://ark.example/api/v3/models" {
		t.Fatalf("versioned base=%q", got)
	}

	// 空 key：不发 Authorization（Ollama 场景）
	c3 := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := c3.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("空 APIKey 不应发 Authorization，got %q", gotAuth)
	}

	// 非 200：error.message 提取
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srvErr.Close()
	if _, err := NewClient(Config{BaseURL: srvErr.URL, Model: "m"}).ListModels(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "服务商返回 401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("got %v", err)
	}

	// 空列表报错
	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer srvEmpty.Close()
	if _, err := NewClient(Config{BaseURL: srvEmpty.URL, Model: "m"}).ListModels(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "服务商返回空模型列表") {
		t.Fatalf("got %v", err)
	}
}
