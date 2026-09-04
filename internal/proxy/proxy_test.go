package proxy

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/mitm"
	"prismproxy/internal/store"
)

// newTestProxy 在 127.0.0.1 随机端口起代理（ca=nil 纯透传），返回存储、代理 URL 与服务端
func newTestProxy(t *testing.T, ca *mitm.CA) (*store.Store, *url.URL, *Server) {
	t.Helper()
	st := store.New(100)
	rec := capture.NewRecorder(st)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := NewServer(ln.Addr().String(), rec, ca)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	return st, proxyURL, srv
}

// waitFlows 轮询等够 n 条流且全部到达终态（M3 起 Begin/Update 会提前落库中间态）
func waitFlows(t *testing.T, st *store.Store, n int) []*capture.Flow {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		flows := st.List()
		if len(flows) >= n {
			allFinal := true
			for _, f := range flows {
				if f.State != capture.StateDone && f.State != capture.StateError {
					allFinal = false
					break
				}
			}
			if allFinal {
				return flows
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return st.List()
}

func TestPlainHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		io.WriteString(w, "hello")
	}))
	defer upstream.Close()

	st, proxyURL, _ := newTestProxy(t, nil)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	resp, err := client.Get(upstream.URL + "/path?q=1")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if resp.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("upstream header lost")
	}

	flows := waitFlows(t, st, 1)
	if len(flows) == 0 {
		t.Fatal("no flow recorded")
	}
	f := flows[0]
	if f.State != capture.StateDone {
		t.Errorf("state = %s, want done (err=%q)", f.State, f.Err)
	}
	if f.Scheme != "http" {
		t.Errorf("scheme = %s, want http", f.Scheme)
	}
	if f.Response == nil || f.Response.StatusCode != 200 {
		t.Errorf("response status = %+v", f.Response)
	}
	if string(f.Response.Body) != "hello" {
		t.Errorf("captured body = %q", f.Response.Body)
	}
	// 进程归因：测试进程自己发起的连接，PID 应等于自身
	if f.Process == nil {
		t.Error("process attribution: got nil")
	} else if f.Process.PID != uint32(os.Getpid()) {
		t.Errorf("process pid = %d, want %d (name=%s)", f.Process.PID, os.Getpid(), f.Process.Name)
	} else if f.Process.Name == "" {
		t.Error("process name empty")
	}
}

func TestConnectTunnel(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls-hello")
	}))
	defer upstream.Close()

	st, proxyURL, _ := newTestProxy(t, nil)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			// 关掉 keep-alive，让隧道在响应结束后关闭、Flow 及时落库
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("HTTPS via CONNECT: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "tls-hello" {
		t.Fatalf("body = %q", body)
	}

	flows := waitFlows(t, st, 1)
	if len(flows) == 0 {
		t.Fatal("no flow recorded")
	}
	f := flows[0]
	if f.Scheme != "https" {
		t.Errorf("scheme = %s, want https", f.Scheme)
	}
	if f.State != capture.StateDone {
		t.Errorf("state = %s (err=%q)", f.State, f.Err)
	}
	if f.BytesUp == 0 || f.BytesDown == 0 {
		t.Errorf("byte counters: up=%d down=%d", f.BytesUp, f.BytesDown)
	}
	if f.Process == nil || f.Process.PID != uint32(os.Getpid()) {
		t.Errorf("tunnel process attribution: %+v", f.Process)
	}
}

func TestUpstreamError(t *testing.T) {
	st, proxyURL, _ := newTestProxy(t, nil)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	// 不可达上游：预期 502 + 错误流落库
	resp, err := client.Get("http://127.0.0.1:1/unreachable")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}

	flows := waitFlows(t, st, 1)
	if len(flows) == 0 {
		t.Fatal("no flow recorded")
	}
	f := flows[0]
	if f.State != capture.StateError {
		t.Errorf("state = %s, want error", f.State)
	}
	if f.Err == "" {
		t.Error("err empty")
	}
}
