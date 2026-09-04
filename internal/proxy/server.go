package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/mitm"
	"prismproxy/internal/rules"
)

type connContextKey struct{}

// Options 可选项（M4：上游代理 + 规则引擎）
type Options struct {
	UpstreamProxy string        // 上游 HTTP 代理 host:port；空=直连（方案 §4.5）
	Engine        *rules.Holder // 规则引擎（热更新容器）；nil=全默认动作
}

// Server HTTP 代理：明文转发 + CONNECT（ca != nil 时 MITM 解密，否则盲透传）
type Server struct {
	addr      string
	port      uint16
	rec       *capture.Recorder
	srv       *http.Server
	transport *http.Transport
	certs     *mitm.CertCache // nil 时 CONNECT 一律透传（M1 行为）
	caPEM     []byte          // /ca 下载页用
	caDER     []byte
	upstream  string // 上游代理地址（环路防护后；空=直连）
	eng       *rules.Holder

	bypassMu sync.Mutex
	bypass   map[string]struct{} // MITM 握手失败/Upgrade 的 host，后续 CONNECT 直接透传
}

// NewServer 等价 NewServerOpts(addr, rec, ca, nil)（测试与 M1-M3 调用点兼容）
func NewServer(addr string, rec *capture.Recorder, ca *mitm.CA) (*Server, error) {
	return NewServerOpts(addr, rec, ca, nil)
}

func NewServerOpts(addr string, rec *capture.Recorder, ca *mitm.CA, opts *Options) (*Server, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, err
	}

	s := &Server{addr: addr, port: uint16(p), rec: rec, bypass: make(map[string]struct{})}
	if opts != nil {
		s.eng = opts.Engine
		s.upstream = guardUpstreamLoop(opts.UpstreamProxy, host, uint16(p))
	}
	if ca != nil {
		s.certs = mitm.NewCertCache(ca)
		s.caPEM = ca.CertPEM
		s.caDER = ca.CertDER
	}
	s.transport = &http.Transport{
		// 保真：不解压、不改写 Accept-Encoding，原始字节透传（压缩体后续按 Content-Encoding 展示层解压）
		DisableCompression: true,
		DialContext:        (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		// 抓包工具惯例：不校验上游证书（避免上游证书问题导致抓不到），真实证书链记入 Flow.TLS
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		// 方案 §4.1 的坑：自定义 TLSClientConfig 后 Go 保守禁用 h2，必须显式开启
		ForceAttemptHTTP2: true,
	}
	if s.upstream != "" {
		// 上游 HTTP 代理（含 CONNECT 级联由 Transport 处理，方案 §4.5）
		s.transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: s.upstream})
	}
	s.srv = &http.Server{
		Addr:    addr,
		Handler: s,
		// accept 时把 net.Conn 塞进 context，供进程归因取客户端源端口
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connContextKey{}, c)
		},
	}
	return s, nil
}

func (s *Server) ListenAndServe() error      { return s.srv.ListenAndServe() }
func (s *Server) Serve(l net.Listener) error { return s.srv.Serve(l) }
func (s *Server) Close() error               { return s.srv.Close() }

// guardUpstreamLoop 自身环路防护（方案 §4.5）：上游代理指向本工具监听地址时降级直连
func guardUpstreamLoop(upstream, selfHost string, selfPort uint16) string {
	if upstream == "" {
		return ""
	}
	h, p, err := net.SplitHostPort(upstream)
	if err != nil {
		return "" // 非法地址交给 Validate 拦截；此处降级直连
	}
	pu, err := strconv.ParseUint(p, 10, 16)
	if err == nil && uint16(pu) == selfPort {
		if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == selfHost {
			log.Printf("warn: 上游代理 %s 指向本工具端口，已跳过（防环路）", upstream)
			return ""
		}
	}
	return upstream
}

// shouldDecrypt 解密规则判定（默认 MITM）
func (s *Server) shouldDecrypt(host string) bool {
	if s.eng == nil {
		return true
	}
	return s.eng.Get().ShouldDecrypt(host)
}

// ApplyRulesFilter 把规则引擎接到 Recorder：ShouldDisplay 为 false 的流不记录
// （正常转发不受影响，规则设计 §5.3）。app 与 headless 共用。
func ApplyRulesFilter(rec *capture.Recorder, h *rules.Holder) {
	if h == nil {
		return
	}
	rec.Filter = func(f *capture.Flow) bool {
		eng := h.Get() // nil Engine ShouldDisplay 返回默认 true
		procName := ""
		if f.Process != nil {
			procName = f.Process.Name
		}
		rawURL := ""
		if f.Request != nil {
			rawURL = f.Request.URL
		}
		return eng.ShouldDisplay(hostOnly(f.ServerAddr), rawURL, procName)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	// /ca 下载页：仅响应"直接访问本代理"的请求（origin-form），代理请求（绝对 URL）不受影响
	if s.caPEM != nil && r.Method == http.MethodGet && !r.URL.IsAbs() && strings.HasPrefix(r.URL.Path, "/ca") {
		s.handleCA(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// processOf 即时查询 TCP 表做进程归因（不做长期缓存，见方案 §4.7）
func (s *Server) processOf(ctx context.Context) *capture.ProcessInfo {
	c, ok := ctx.Value(connContextKey{}).(net.Conn)
	if !ok {
		return nil
	}
	return s.processOfConn(c)
}

// processOfConn 直接从连接取客户端源端口查进程（MITM 循环内复用，连接级只查一次）
func (s *Server) processOfConn(c net.Conn) *capture.ProcessInfo {
	ta, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil
	}
	return rules.FindProcess(uint16(ta.Port), s.port)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	flow := s.newFlow(r, "http")
	s.rec.Begin(flow)

	// 代理请求转客户端请求
	r.RequestURI = ""
	if r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}
	removeHopHeaders(r.Header)

	flow.State = capture.StateStreaming
	s.rec.Update(flow)
	resp, err := s.roundTrip(flow, r)
	if err != nil {
		http.Error(w, "Bad Gateway: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	removeHopHeaders(resp.Header)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	s.streamResponse(flow, resp, w)
}

// newFlow 建一条 Flow 并记录请求元信息（handleHTTP 与 MITM 共用）
func (s *Server) newFlow(r *http.Request, scheme string) *capture.Flow {
	flow := capture.NewFlow(s.rec.NewID())
	flow.Scheme = scheme
	flow.ClientAddr = r.RemoteAddr
	flow.ServerAddr = r.Host
	flow.Process = s.processOf(r.Context())
	flow.Request = &capture.Message{
		Method: r.Method,
		URL:    r.URL.String(),
		Proto:  r.Proto,
		Header: r.Header.Clone(),
	}
	return flow
}

// roundTrip tee 捕获请求体并转发上游；出错时收尾 flow 并返回 err
func (s *Server) roundTrip(flow *capture.Flow, r *http.Request) (*http.Response, error) {
	reqCap := capture.NewBodyCapture(capture.MaxBodyCapture)
	if r.Body != nil {
		r.Body = &teeReadCloser{Reader: io.TeeReader(r.Body, reqCap), Closer: r.Body}
	}

	resp, err := s.transport.RoundTrip(r)
	// RoundTrip 返回时请求体已被完整消费（或出错中断），tee 捕获即定稿
	flow.Request.Body = reqCap.Bytes()
	flow.Request.BodyTruncated = reqCap.Truncated()
	flow.BytesUp = int64(len(reqCap.Bytes()))
	if err != nil {
		flow.State = capture.StateError
		flow.Err = err.Error()
		s.rec.Finish(flow)
		return nil, err
	}
	return resp, nil
}

// streamResponse tee 捕获响应体并写出，随后收尾 flow。
// w 满足 io.Writer 即可：handleHTTP 传 ResponseWriter，MITM 传 bufio.Writer。
func (s *Server) streamResponse(flow *capture.Flow, resp *http.Response, w io.Writer) {
	// 先落响应头（无 body）：SSE/大下载期间 UI 即可看到状态行与首部
	flow.Response = &capture.Message{
		Proto:           resp.Proto,
		StatusCode:      resp.StatusCode,
		Header:          resp.Header.Clone(),
		ContentEncoding: resp.Header.Get("Content-Encoding"),
	}
	s.rec.Update(flow)

	respCap := capture.NewBodyCapture(capture.MaxBodyCapture)
	n, copyErr := io.Copy(w, io.TeeReader(resp.Body, respCap))
	flow.BytesDown = n
	flow.Response.Body = respCap.Bytes()
	flow.Response.BodyTruncated = respCap.Truncated()
	if copyErr != nil {
		flow.State = capture.StateError
		flow.Err = copyErr.Error()
	} else {
		flow.State = capture.StateDone
	}
	s.rec.Finish(flow)
}

type teeReadCloser struct {
	io.Reader
	io.Closer
}

var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Keep-Alive",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
