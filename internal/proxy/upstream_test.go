package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/mitm"
	"prismproxy/internal/store"
)

// fakeUpstream 伪装上游 HTTP 代理：明文按编程响应（不真转发），CONNECT 记录鉴权头并可编程 407 或真转发
type fakeUpstream struct {
	addr        string
	marker      string // 明文响应体标识（区分多个 fake）
	ln          net.Listener
	plainHits   atomic.Int32
	connectHits atomic.Int32
	lastAuth    atomic.Value // string：最近一次请求的 Proxy-Authorization
	plain407    atomic.Bool  // 明文恒 407
	connect407  atomic.Bool  // CONNECT 恒 407
	forward     bool         // CONNECT 真转发到目标
}

func startFakeUpstream(t *testing.T, marker string) *fakeUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake upstream listen: %v", err)
	}
	f := &fakeUpstream{addr: ln.Addr().String(), marker: marker, ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeUpstream) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	f.lastAuth.Store(req.Header.Get("Proxy-Authorization"))

	if req.Method == http.MethodConnect {
		f.connectHits.Add(1)
		if f.connect407.Load() {
			io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
			return
		}
		if !f.forward {
			io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
			return
		}
		target, err := net.DialTimeout("tcp", req.Host, 5*time.Second)
		if err != nil {
			io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		defer target.Close()
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		c.SetDeadline(time.Time{})
		target.SetDeadline(time.Time{})
		go func() { io.Copy(target, c); target.Close() }()
		io.Copy(c, target)
		return
	}

	f.plainHits.Add(1)
	io.Copy(io.Discard, req.Body)
	req.Body.Close()
	if f.plain407.Load() {
		io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	body := "via " + f.marker
	fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}

func (f *fakeUpstream) auth() string {
	if v := f.lastAuth.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// newTestProxyOpts 同 newTestProxy 但走 NewServerOpts 注入 Options
func newTestProxyOpts(t *testing.T, ca *mitm.CA, opts *Options) (*store.Store, *url.URL, *Server) {
	t.Helper()
	st := store.New(100)
	rec := capture.NewRecorder(st)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := NewServerOpts(ln.Addr().String(), rec, ca, opts)
	if err != nil {
		t.Fatalf("NewServerOpts: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	return st, proxyURL, srv
}

func httpGetViaProxy(t *testing.T, proxyURL *url.URL, target string) *http.Response {
	t.Helper()
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:              http.ProxyURL(proxyURL),
			TLSClientConfig:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			DisableKeepAlives:  true,
			DisableCompression: true,
		},
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	return resp
}

// badEntrySpy 坏点/轮换回调记录
type badEntrySpy struct {
	mu      chan struct{}
	bads    []string
	rotates int
}

func newBadEntrySpy() *badEntrySpy { return &badEntrySpy{mu: make(chan struct{}, 1)} }

func (s *badEntrySpy) onBad(addr string) {
	s.mu <- struct{}{}
	s.bads = append(s.bads, addr)
	<-s.mu
}

func (s *badEntrySpy) onRotate(context.Context) {
	s.mu <- struct{}{}
	s.rotates++
	<-s.mu
}

func (s *badEntrySpy) snapshot() (bads []string, rotates int) {
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	return append([]string(nil), s.bads...), s.rotates
}

// TestStaticUpstreamRegression 静态上游旧路径行为不变：明文经上游转发、无鉴权头
func TestStaticUpstreamRegression(t *testing.T) {
	up := startFakeUpstream(t, "static")
	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{UpstreamProxy: up.addr})

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "via static" {
		t.Fatalf("body = %q, want %q", body, "via static")
	}
	if up.plainHits.Load() != 1 {
		t.Fatalf("plainHits = %d, want 1", up.plainHits.Load())
	}
	if up.auth() != "" {
		t.Fatalf("static upstream must not carry auth, got %q", up.auth())
	}
}

// TestResolverDynamicSwitch resolver 闭包动态切换：不重启代理，两次请求分别落到不同上游
func TestResolverDynamicSwitch(t *testing.T) {
	upA := startFakeUpstream(t, "A")
	upB := startFakeUpstream(t, "B")
	cur := &UpstreamEndpoint{Addr: upA.addr}
	var mu atomic.Pointer[UpstreamEndpoint]
	mu.Store(cur)

	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) { return mu.Load(), nil },
	})

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/1")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "via A" {
		t.Fatalf("first body = %q, want via A", body)
	}

	mu.Store(&UpstreamEndpoint{Addr: upB.addr})
	resp = httpGetViaProxy(t, proxyURL, "http://example.com/2")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "via B" {
		t.Fatalf("second body = %q, want via B", body)
	}
}

// TestResolverUserinfoPreset 账密经 userinfo 预置 Proxy-Authorization（S2：首请求即带，非 407 质询重发）
func TestResolverUserinfoPreset(t *testing.T) {
	up := startFakeUpstream(t, "auth")
	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) {
			return &UpstreamEndpoint{Addr: up.addr, Username: "u1", Password: "p1"}, nil
		},
	})

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	resp.Body.Close()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u1:p1"))
	if up.auth() != want {
		t.Fatalf("Proxy-Authorization = %q, want %q", up.auth(), want)
	}
	if up.plainHits.Load() != 1 {
		t.Fatalf("plainHits = %d, want 1（首请求即带凭据，无质询重发）", up.plainHits.Load())
	}
}

// TestPlain407RetryOnce 明文 407：记坏点 + 触发换 IP + 新 entry 自动重试一次成功
func TestPlain407RetryOnce(t *testing.T) {
	upA := startFakeUpstream(t, "bad")
	upA.plain407.Store(true)
	upB := startFakeUpstream(t, "good")

	var mu atomic.Pointer[UpstreamEndpoint]
	mu.Store(&UpstreamEndpoint{Addr: upA.addr})
	spy := newBadEntrySpy()

	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) { return mu.Load(), nil },
		OnBadEntry:       spy.onBad,
		RotateUpstream: func(ctx context.Context) {
			spy.onRotate(ctx)
			mu.Store(&UpstreamEndpoint{Addr: upB.addr}) // App 侧 Manager.Rotate 后 resolver 返回新 IP
		},
	})

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK || string(body) != "via good" {
		t.Fatalf("status=%d body=%q, want 200 via good", resp.StatusCode, body)
	}
	bads, rotates := spy.snapshot()
	if len(bads) != 1 || bads[0] != upA.addr {
		t.Fatalf("bads = %v, want [%s]", bads, upA.addr)
	}
	if rotates != 1 {
		t.Fatalf("rotates = %d, want 1", rotates)
	}
	if upA.plainHits.Load() != 1 || upB.plainHits.Load() != 1 {
		t.Fatalf("hits A=%d B=%d, want 1/1", upA.plainHits.Load(), upB.plainHits.Load())
	}
}

// TestPlain407TwicePassthrough 重试仍 407：透传客户端，不无限重试
func TestPlain407TwicePassthrough(t *testing.T) {
	upA := startFakeUpstream(t, "always407")
	upA.plain407.Store(true)
	spy := newBadEntrySpy()

	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) {
			return &UpstreamEndpoint{Addr: upA.addr}, nil
		},
		OnBadEntry:     spy.onBad,
		RotateUpstream: spy.onRotate,
	})

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407 透传", resp.StatusCode)
	}
	if got := upA.plainHits.Load(); got != 2 {
		t.Fatalf("plainHits = %d, want 2（首次+重试一次）", got)
	}
}

// TestResolverLoopGuard 逐次动态防环：resolver 返回本工具监听地址判坏点并取下一个
func TestResolverLoopGuard(t *testing.T) {
	upB := startFakeUpstream(t, "B")
	spy := newBadEntrySpy()

	var proxyURLRef atomic.Value // *url.URL
	var first atomic.Bool
	first.Store(true)
	opts := &Options{
		OnBadEntry: spy.onBad,
	}
	opts.UpstreamResolver = func(context.Context) (*UpstreamEndpoint, error) {
		if first.CompareAndSwap(true, false) {
			return &UpstreamEndpoint{Addr: proxyURLRef.Load().(*url.URL).Host}, nil // 自环
		}
		return &UpstreamEndpoint{Addr: upB.addr}, nil
	}

	_, proxyURL, _ := newTestProxyOpts(t, nil, opts)
	proxyURLRef.Store(proxyURL)

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "via B" {
		t.Fatalf("body = %q, want via B", body)
	}
	bads, _ := spy.snapshot()
	if len(bads) != 1 || bads[0] != proxyURL.Host {
		t.Fatalf("bads = %v, want 自环地址 %s", bads, proxyURL.Host)
	}
}

// TestDialBadEntryReport dial 失败坏点归因：拨代理失败计坏点；直连目标站失败不误杀
func TestDialBadEntryReport(t *testing.T) {
	spy := newBadEntrySpy()
	var ep atomic.Pointer[UpstreamEndpoint]
	ep.Store(&UpstreamEndpoint{Addr: "127.0.0.1:1"}) // 未监听地址

	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) { return ep.Load(), nil },
		OnBadEntry:       spy.onBad,
	})

	// 走代理：拨代理地址失败 → 计坏点
	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	bads, _ := spy.snapshot()
	if len(bads) != 1 || bads[0] != "127.0.0.1:1" {
		t.Fatalf("bads = %v, want [127.0.0.1:1]", bads)
	}

	// 直连（resolver 返回 nil）：目标站不可达 → 不计坏点
	ep.Store(&UpstreamEndpoint{}) // Addr 空 = 直连
	resp = httpGetViaProxy(t, proxyURL, "http://127.0.0.1:1/x")
	resp.Body.Close()
	bads, _ = spy.snapshot()
	if len(bads) != 1 {
		t.Fatalf("直连目标站失败误判坏点: bads = %v", bads)
	}
}

// TestResolverError502 resolver 返回 err（OnEmpty=error）：回 502 且错误进 Flow
func TestResolverError502(t *testing.T) {
	st, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) {
			return nil, errors.New("代理池无货（OnEmpty=error）")
		},
	})

	resp := httpGetViaProxy(t, proxyURL, "http://example.com/x")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(string(body), "代理池无货") {
		t.Fatalf("body = %q, want 含池错误信息", body)
	}
	flows := waitFlows(t, st, 1)
	if len(flows) == 0 || flows[0].State != capture.StateError {
		t.Fatalf("flow 未记录错误: %+v", flows)
	}
}

// TestTunnelAuthHeader 盲隧道 CONNECT 带 Proxy-Authorization；上游 407 → 502 + 坏点 + 换 IP
func TestTunnelAuthHeader(t *testing.T) {
	up := startFakeUpstream(t, "tunnel")
	up.connect407.Store(true)
	spy := newBadEntrySpy()

	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{
		UpstreamResolver: func(context.Context) (*UpstreamEndpoint, error) {
			return &UpstreamEndpoint{Addr: up.addr, Username: "tu", Password: "tp"}, nil
		},
		OnBadEntry:     spy.onBad,
		RotateUpstream: spy.onRotate,
	})

	// Go 客户端对 CONNECT 的非 2xx 响应返回 error（Bad Gateway=代理侧 502）
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
		Timeout: 5 * time.Second,
	}
	_, err := client.Get("https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("err = %v, want 含 Bad Gateway（隧道 407 → 502）", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("tu:tp"))
	if up.auth() != want {
		t.Fatalf("CONNECT Proxy-Authorization = %q, want %q", up.auth(), want)
	}
	bads, rotates := spy.snapshot()
	if len(bads) != 1 || bads[0] != up.addr {
		t.Fatalf("bads = %v, want [%s]", bads, up.addr)
	}
	if rotates != 1 {
		t.Fatalf("rotates = %d, want 1（407 触发换 IP）", rotates)
	}
}

// TestTunnelViaUpstreamOK 静态上游隧道级联旧路径：CONNECT 无鉴权头、数据真转发
func TestTunnelViaUpstreamOK(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls-hello")
	}))
	defer target.Close()

	up := startFakeUpstream(t, "relay")
	up.forward = true

	_, proxyURL, _ := newTestProxyOpts(t, nil, &Options{UpstreamProxy: up.addr})

	resp := httpGetViaProxy(t, proxyURL, target.URL)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "tls-hello" {
		t.Fatalf("body = %q, want tls-hello", body)
	}
	if up.connectHits.Load() != 1 {
		t.Fatalf("connectHits = %d, want 1", up.connectHits.Load())
	}
	if up.auth() != "" {
		t.Fatalf("static tunnel must not carry auth, got %q", up.auth())
	}
}
