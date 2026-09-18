package proxy

import (
	"bytes"
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
	"sync/atomic"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/mitm"
	"prismproxy/internal/rules"
)

type connContextKey struct{}

// upstreamHolderKey / upstreamHolder：把本次请求实际解析到的上游端点经 context
// 从 transport.Proxy 闭包回传给 roundTrip（请求级隔离，避免读 Server 共享 curUpstream 的并发竞态）。
type upstreamHolderKey struct{}

type upstreamHolder struct {
	ep       *UpstreamEndpoint // 本次回源实际使用的上游；nil=直连
	addr     string            // 冗余的入口 host:port，供 roundTrip 回填 flow.UpstreamAddr
	resolved bool              // roundTrip 是否已显式解析（含直连结论）；true 时 Proxy 闭包不再二次 resolve
}

// UpstreamEndpoint 动态上游（代理池）端点，引擎唯一消费类型（代理池设计 §7.1）；
// App 装配层负责把 proxypool.Endpoint 薄适配为本类型。
type UpstreamEndpoint struct {
	Addr     string // host:port
	Username string // 账密模式预置 Proxy-Authorization（S2：Go 仅 userinfo 时预置）
	Password string
}

// Options 可选项（M4：上游代理 + 规则引擎；代理池：动态上游）
type Options struct {
	UpstreamProxy string        // 上游 HTTP 代理 host:port；空=直连（方案 §4.5）
	Engine        *rules.Holder // 规则引擎（热更新容器）；nil=全默认动作

	// 代理池动态上游：非 nil 时优先于静态 UpstreamProxy。
	// 返回 (nil,nil)=本次直连（OnEmpty=direct 降级）；返回 err=本次失败回 502。
	UpstreamResolver func(ctx context.Context) (*UpstreamEndpoint, error)
	// 坏点上报（dial 失败 / 407 / 防环命中），App 接 Manager.ReportBad；nil=不上报
	OnBadEntry func(addr string)
	// 407 时触发换 IP（记坏点后调用），App 接 Manager.Rotate；nil=仅记坏点
	RotateUpstream func(ctx context.Context)
}

// Server HTTP 代理：明文转发 + CONNECT（ca != nil 时 MITM 解密，否则盲透传）
type Server struct {
	addr      string
	host      string
	port      uint16
	rec       *capture.Recorder
	srv       *http.Server
	transport *http.Transport
	certs     *mitm.CertCache // nil 时 CONNECT 一律透传（M1 行为）
	caPEM     []byte          // /ca 下载页用
	caDER     []byte
	upstream  string // 静态上游代理地址（环路防护后；空=直连）
	eng       *rules.Holder

	resolver    func(ctx context.Context) (*UpstreamEndpoint, error)
	onBadEntry  func(addr string)
	rotate      func(ctx context.Context)
	curUpstream atomic.Pointer[string] // 最近一次 resolve 出的上游地址（坏点归因用；""=直连）

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

	s := &Server{addr: addr, host: host, port: uint16(p), rec: rec, bypass: make(map[string]struct{})}
	if opts != nil {
		s.eng = opts.Engine
		s.resolver = opts.UpstreamResolver
		s.onBadEntry = opts.OnBadEntry
		s.rotate = opts.RotateUpstream
		s.upstream = GuardUpstreamLoop(opts.UpstreamProxy, host, uint16(p))
	}
	if ca != nil {
		s.certs = mitm.NewCertCache(ca)
		s.caPEM = ca.CertPEM
		s.caDER = ca.CertDER
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	s.transport = &http.Transport{
		// 保真：不解压、不改写 Accept-Encoding，原始字节透传（压缩体后续按 Content-Encoding 展示层解压）
		DisableCompression: true,
		// 坏点埋点通道一（§6.3 S1）：走代理时 Transport 拨的是代理地址，
		// dial 失败且与当前 upstream 一致才计坏点（目标站超时/直连失败不误杀）
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := dialer.DialContext(ctx, network, addr)
			if err != nil && s.resolver != nil && addr == s.currentUpstreamAddr() {
				s.reportBad(addr)
			}
			return c, err
		},
		// 抓包工具惯例：不校验上游证书（避免上游证书问题导致抓不到），真实证书链记入 Flow.TLS
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		// 方案 §4.1 的坑：自定义 TLSClientConfig 后 Go 保守禁用 h2，必须显式开启
		ForceAttemptHTTP2: true,
	}
	// 统一 Proxy 闭包（§7.2）：动态 resolver 优先，静态 upstream 兜底，nil=直连。
	// userinfo 预置账密（S2：Go 只在 proxy URL 含 userinfo 时预置 Proxy-Authorization，不做 407 质询重发）。
	s.transport.Proxy = func(req *http.Request) (*url.URL, error) {
		// 优先用 roundTrip 已解析好的本次上游（权威；含 ep=nil 的直连结论——以 resolved 判定，
		// 避免直连时再触发一次 resolveUpstream 二次取货）。HTTPS 隧道复用时本闭包不会被调用，
		// 故真正的"每条流记录上游"在 roundTrip 完成，这里只负责连接路由。
		var ep *UpstreamEndpoint
		if h, ok := req.Context().Value(upstreamHolderKey{}).(*upstreamHolder); ok && h.resolved {
			ep = h.ep
		} else {
			var err error
			ep, err = s.resolveUpstream(req.Context())
			if err != nil {
				return nil, err
			}
		}
		if ep == nil {
			return nil, nil
		}
		u := &url.URL{Scheme: "http", Host: ep.Addr}
		if ep.Username != "" {
			u.User = url.UserPassword(ep.Username, ep.Password)
		}
		return u, nil
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

// GuardUpstreamLoop 自身环路防护（方案 §4.5）：上游代理指向本工具监听地址时降级直连。
// 代理 Server 与 M6 调试重发（compose）共用。
func GuardUpstreamLoop(upstream, selfHost string, selfPort uint16) string {
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

// maxUpstreamResolveAttempts 防环连续命中取货上限（> MaxRetry×常规池容量），防死循环（§7.1）
const maxUpstreamResolveAttempts = 16

var errUpstreamLoopGuard = errString("上游地址连续命中自环/非法，已放弃本次解析")

// resolveUpstream 统一上游解析：resolver（pool）优先，静态 UpstreamProxy 其次，都空=直连(nil,nil)。
// resolver 路径逐次动态防环（§7.1）：Addr 每次过 GuardUpstreamLoop，命中自环/非法判坏点取下一个。
func (s *Server) resolveUpstream(ctx context.Context) (*UpstreamEndpoint, error) {
	if s.resolver == nil {
		if s.upstream == "" {
			return nil, nil
		}
		return &UpstreamEndpoint{Addr: s.upstream}, nil
	}
	for i := 0; i < maxUpstreamResolveAttempts; i++ {
		ep, err := s.resolver(ctx)
		if err != nil {
			s.setCurrentUpstream("")
			return nil, err
		}
		if ep == nil || ep.Addr == "" {
			s.setCurrentUpstream("")
			return nil, nil
		}
		if GuardUpstreamLoop(ep.Addr, s.host, s.port) == "" {
			s.reportBad(ep.Addr)
			continue
		}
		s.setCurrentUpstream(ep.Addr)
		return ep, nil
	}
	s.setCurrentUpstream("")
	return nil, errUpstreamLoopGuard
}

// reportBad 坏点上报（dial 失败 / 407 / 防环命中），App 接 Manager.ReportBad（含迟报与阈值保护）
func (s *Server) reportBad(addr string) {
	if s.onBadEntry != nil && addr != "" {
		s.onBadEntry(addr)
	}
}

func (s *Server) setCurrentUpstream(addr string) { s.curUpstream.Store(&addr) }

func (s *Server) currentUpstreamAddr() string {
	if p := s.curUpstream.Load(); p != nil {
		return *p
	}
	return ""
}

// ResetBypass 清空因 MITM 握手失败被降级为盲隧道的 host 集合，使后续 CONNECT 重新尝试 MITM。
// 典型触发：系统 CA 注入成功 / 用户手动要求重试（证书补好后，不清空则这些 host 永久走盲隧道）。
// 仅影响新连接，已建立的盲隧道连接与已运行 App 的信任缓存不在此处理。返回被清理的 host 数。
func (s *Server) ResetBypass() int {
	s.bypassMu.Lock()
	defer s.bypassMu.Unlock()
	n := len(s.bypass)
	s.bypass = make(map[string]struct{})
	return n
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
	// 请求级上游：此处显式解析一次（权威）。Go transport 对 HTTPS 会复用空闲 CONNECT
	// 隧道，隧道复用时不再回调 transport.Proxy，因此记录必须在这里完成而非依赖 Proxy 闭包。
	holder := &upstreamHolder{}
	if ep, rerr := s.resolveUpstream(r.Context()); rerr != nil {
		flow.Request.Body = reqCap.Bytes()
		flow.Request.BodyTruncated = reqCap.Truncated()
		flow.BytesUp = int64(len(reqCap.Bytes()))
		flow.State = capture.StateError
		flow.Err = rerr.Error()
		s.rec.Finish(flow)
		return nil, rerr
	} else {
		holder.ep = ep
		holder.resolved = true
		if ep != nil {
			holder.addr = ep.Addr
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), upstreamHolderKey{}, holder))

	resp, err := s.transport.RoundTrip(r)
	// RoundTrip 返回时请求体已被完整消费（或出错中断），tee 捕获即定稿
	flow.Request.Body = reqCap.Bytes()
	flow.Request.BodyTruncated = reqCap.Truncated()
	flow.BytesUp = int64(len(reqCap.Bytes()))
	flow.UpstreamAddr = holder.addr
	if err == nil && resp.StatusCode == http.StatusProxyAuthRequired && s.resolver != nil {
		// 明文路径上游 407 是正常响应（Go 不做质询重发，§6.3 S2）：记坏点+换 IP+重试一次。
		// 重试前重新解析拿到轮换后的新节点，写入 holder 供 Proxy 闭包路由与 flow 记录。
		resp, err = s.retryAfter407(r, resp, reqCap, holder)
		flow.UpstreamAddr = holder.addr
	}
	if err != nil {
		flow.State = capture.StateError
		flow.Err = err.Error()
		s.rec.Finish(flow)
		return nil, err
	}
	return resp, nil
}

// retryAfter407 出口 407 处理（§6.3）：坏点上报 + 触发换 IP + 用捕获体换新 entry 重试一次；
// 仍 407 或请求体不完整（截断无法重放）则原样透传，不无限重试。
func (s *Server) retryAfter407(r *http.Request, resp *http.Response, reqCap *capture.BodyCapture, holder *upstreamHolder) (*http.Response, error) {
	// 用本请求权威的上游地址计坏点，不读共享 curUpstream（并发下可能已被其他请求改写，导致记错/漏记）
	if holder.addr != "" {
		s.reportBad(holder.addr)
	}
	if s.rotate != nil {
		s.rotate(r.Context())
	}
	// 轮换后重新解析，拿到新 entry 更新本次请求路由与 flow 记录。
	// 返回 (nil,nil)=池耗尽降级直连，属正常分支；只有 resolve 报错时不能拿刚判坏的旧节点重放，直接透传原 407。
	ep, rerr := s.resolveUpstream(r.Context())
	if rerr != nil {
		return resp, nil
	}
	holder.ep = ep
	holder.resolved = true
	holder.addr = ""
	if ep != nil {
		holder.addr = ep.Addr
	}
	if reqCap.Truncated() {
		return resp, nil
	}
	r2 := new(http.Request)
	*r2 = *r
	if r.Body != nil {
		r2.Body = io.NopCloser(bytes.NewReader(reqCap.Bytes()))
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return s.transport.RoundTrip(r2)
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
