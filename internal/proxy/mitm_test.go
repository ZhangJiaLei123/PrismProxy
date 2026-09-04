package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/mitm"
)

func testCA(t *testing.T) *mitm.CA {
	t.Helper()
	ca, err := mitm.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("test ca: %v", err)
	}
	return ca
}

// trustedClient 信任测试 CA 的 HTTPS 客户端（走代理）
func trustedClient(t *testing.T, proxyURL *url.URL, ca *mitm.CA) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
		Timeout: 5 * time.Second,
	}
}

// TestMITMDecrypt 验收 #4：装 CA 的客户端全链路明文捕获
func TestMITMDecrypt(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls-hello:"+r.URL.Path)
	}))
	defer upstream.Close()

	ca := testCA(t)
	st, proxyURL, _ := newTestProxy(t, ca)
	client := trustedClient(t, proxyURL, ca)

	// keep-alive 同连接两请求 → 两条明文 flow
	for _, path := range []string{"/a", "/b"} {
		resp, err := client.Get(upstream.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "tls-hello:"+path {
			t.Fatalf("body = %q", body)
		}
	}

	flows := waitFlows(t, st, 2)
	if len(flows) != 2 {
		t.Fatalf("flows = %d, want 2", len(flows))
	}
	for _, f := range flows {
		if f.State != capture.StateDone {
			t.Errorf("state = %s (err=%q)", f.State, f.Err)
		}
		if f.Scheme != "https" {
			t.Errorf("scheme = %s", f.Scheme)
		}
		if f.Request == nil || !strings.HasPrefix(f.Request.URL, "https://") {
			t.Errorf("request url = %+v", f.Request)
		}
		if f.Response == nil || !strings.HasPrefix(string(f.Response.Body), "tls-hello:") {
			t.Errorf("response body = %+v", f.Response)
		}
		if f.TLS == nil {
			t.Fatal("TLSInfo missing")
		}
		if f.TLS.ClientVersion == "" || f.TLS.ServerVersion == "" {
			t.Errorf("tls versions: client=%q server=%q", f.TLS.ClientVersion, f.TLS.ServerVersion)
		}
		if len(f.TLS.PeerCerts) == 0 {
			t.Error("upstream peer certs not captured")
		}
	}
}

// TestMITMUntrustedThenBypass 客户端不信任 CA → 握手失败 → host 入 bypass → 重连走盲透传
func TestMITMUntrustedThenBypass(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls-hello")
	}))
	defer upstream.Close()

	ca := testCA(t)
	_, proxyURL, srv := newTestProxy(t, ca)

	// 空根池：不信任 Prism CA，握手必失败
	distrust := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			TLSClientConfig:   &tls.Config{RootCAs: x509.NewCertPool()},
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}
	if _, err := distrust.Get(upstream.URL); err == nil {
		t.Fatal("expected tls error for untrusted client")
	}

	// 等服务端把 host 记入 bypass（服务端 addBypass 可能晚于客户端收到 alert）
	host := hostOnly(upstream.URL[len("https://"):])
	deadline := time.Now().Add(2 * time.Second)
	for !srv.isBypass(host) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !srv.isBypass(host) {
		t.Fatal("host not bypassed after handshake failure")
	}

	// 重连：走盲透传，客户端自行与上游握手（InsecureSkipVerify 跳过上游证书校验）
	tunnelClient := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}
	resp, err := tunnelClient.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET via bypassed tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "tls-hello" {
		t.Fatalf("body = %q", body)
	}
}

// TestCAPage 验收 #5：/ca 下载页
func TestCAPage(t *testing.T) {
	ca := testCA(t)
	_, proxyURL, _ := newTestProxy(t, ca)

	// 直连代理端口（不走代理）
	direct := &http.Client{Timeout: 5 * time.Second}

	resp, err := direct.Get(proxyURL.String() + "/ca.pem")
	if err != nil {
		t.Fatalf("GET /ca.pem: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != string(ca.CertPEM) {
		t.Error("/ca.pem body mismatch")
	}

	resp2, err := direct.Get(proxyURL.String() + "/ca")
	if err != nil {
		t.Fatalf("GET /ca: %v", err)
	}
	html, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(html), "Prism Root CA") {
		t.Error("/ca page missing CA name")
	}
}
