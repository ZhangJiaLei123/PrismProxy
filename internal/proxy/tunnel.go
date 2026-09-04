package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"prismproxy/internal/capture"
)

// handleConnect CONNECT 分流：bypass 命中、解密规则 exclude 或未启用 MITM → 盲透传；否则 MITM 解密
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	if s.certs == nil || s.isBypass(host) || !s.shouldDecrypt(host) {
		s.handleTunnel(w, r)
		return
	}
	s.handleMITM(w, r)
}

// isBypass / addBypass 维护"客户端不信任 CA 或需升级协议"的 host 集合
func (s *Server) isBypass(host string) bool {
	s.bypassMu.Lock()
	defer s.bypassMu.Unlock()
	_, ok := s.bypass[host]
	return ok
}

func (s *Server) addBypass(host string) {
	s.bypassMu.Lock()
	s.bypass[host] = struct{}{}
	s.bypassMu.Unlock()
}

func hostOnly(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return host
}

// hijackConn 劫持客户端连接并回 200 Established；返回的连接读侧已含 hijack 缓冲
func hijackConn(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errHijackUnsupported
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

var errHijackUnsupported = errString("hijack unsupported")

type errString string

func (e errString) Error() string { return string(e) }

// handleTunnel CONNECT 盲透传（不解密：M1 行为 + M2 bypass 降级路径）
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	flow := capture.NewFlow(s.rec.NewID())
	flow.Scheme = "https"
	flow.ClientAddr = r.RemoteAddr
	flow.ServerAddr = r.Host
	flow.Process = s.processOf(r.Context())
	flow.Request = &capture.Message{
		Method: r.Method,
		URL:    "https://" + r.Host,
		Proto:  r.Proto,
		Header: r.Header.Clone(),
	}
	s.rec.Begin(flow)

	target := r.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	var upstream net.Conn
	var err error
	if s.upstream != "" {
		// 上游代理级联：向上游发 CONNECT 建隧道（方案 §4.5）
		upstream, err = dialViaUpstream(s.upstream, target, 15*time.Second)
	} else {
		upstream, err = net.DialTimeout("tcp", target, 15*time.Second)
	}
	if err != nil {
		flow.State = capture.StateError
		flow.Err = "dial " + target + ": " + err.Error()
		s.rec.Finish(flow)
		http.Error(w, "Bad Gateway: "+err.Error(), http.StatusBadGateway)
		return
	}

	client, rw, err := hijackConn(w)
	if err != nil {
		upstream.Close()
		flow.State = capture.StateError
		flow.Err = err.Error()
		s.rec.Finish(flow)
		return
	}

	flow.State = capture.StateStreaming
	s.rec.Update(flow)

	// hijack 缓冲里可能已有客户端紧随 CONNECT 发来的 TLS 字节，需一并转发
	clientReader := io.Reader(client)
	if rw.Reader.Buffered() > 0 {
		clientReader = io.MultiReader(rw.Reader, client)
	}

	var upN, downN int64
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(upstream, clientReader)
		atomic.StoreInt64(&upN, n)
		closeWrite(upstream) // 通知上游客户端已停发，促使其收尾关闭
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(client, upstream)
		atomic.StoreInt64(&downN, n)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
	client.Close()
	upstream.Close()

	flow.BytesUp = atomic.LoadInt64(&upN)
	flow.BytesDown = atomic.LoadInt64(&downN)
	flow.State = capture.StateDone
	s.rec.Finish(flow)
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
}

// dialViaUpstream 经上游 HTTP 代理对 target 发 CONNECT 建立隧道（盲透传路径的级联，方案 §4.5）
func dialViaUpstream(proxyAddr, target string, timeout time.Duration) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(timeout))
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target}, // Opaque 使请求行为 "CONNECT host:port HTTP/1.1"
		Host:   target,
		Header: make(http.Header),
	}
	if err := req.Write(c); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		c.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf("上游代理 CONNECT %s 失败: %s", target, resp.Status)
	}
	c.SetDeadline(time.Time{})
	// 200 之后上游可能已随缓冲发来后续字节，读侧必须走 br（复用 mitm.go 的 bufferedConn）
	return &bufferedConn{Conn: c, r: br}, nil
}
