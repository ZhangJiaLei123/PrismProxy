package proxy

import (
	"bufio"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	"prismproxy/internal/capture"
)

// bufferedConn 让 tls.Server 从 hijack 的 bufio 缓冲读起（缓冲里可能已有 ClientHello 字节）
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// handleMITM CONNECT 的 HTTPS 解密分支（方案 §二：假证书 ↔ 代理 ↔ 真证书）
func (s *Server) handleMITM(w http.ResponseWriter, r *http.Request) {
	connectHost := hostOnly(r.Host)

	client, rw, err := hijackConn(w)
	if err != nil {
		return
	}

	// 面向客户端仅 http/1.1（M2 不做 h2 服务端，浏览器会自动降级）
	tlsConf := &tls.Config{
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = connectHost
			}
			return s.certs.Get(name)
		},
	}
	tlsConn := tls.Server(&bufferedConn{Conn: client, r: rw.Reader}, tlsConf)
	if err := tlsConn.Handshake(); err != nil {
		// 客户端不信任 CA / 证书固定（pinning）：记入 bypass，关连接让客户端重连后走盲透传
		s.addBypass(connectHost)
		log.Printf("mitm handshake failed for %s (%v); host bypassed, will tunnel on reconnect", connectHost, err)
		client.Close()
		return
	}
	defer tlsConn.Close()

	proc := s.processOfConn(client)
	clientState := tlsConn.ConnectionState()

	br := bufio.NewReader(tlsConn)
	bw := bufio.NewWriter(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			// EOF/连接重置：客户端关闭 keep-alive 连接，属正常结束
			return
		}

		// 100-continue：ReadRequest 不会自动回 100，需立即回客户端并摘掉 Expect，
		// 否则客户端等 100 不发 body、我们等 body 才 RoundTrip，双方死锁
		if req.Header.Get("Expect") == "100-continue" {
			if _, err := bw.WriteString("HTTP/1.1 100 Continue\r\n\r\n"); err == nil {
				err = bw.Flush()
			}
			if err != nil {
				return
			}
			req.Header.Del("Expect")
		}

		// WebSocket 等升级协议：M2 不解析帧，bypass 后回 502，客户端重连走透传
		if isUpgrade(req) {
			s.addBypass(connectHost)
			writeSimpleResponse(bw, http.StatusBadGateway, "upgrade protocol bypassed, reconnect to tunnel")
			log.Printf("mitm upgrade request for %s; host bypassed", connectHost)
			return
		}

		// 补全请求为客户端请求形态（隧道内 r.URL 是相对路径）
		req.URL.Scheme = "https"
		if req.URL.Host == "" {
			req.URL.Host = req.Host
		}
		req.RequestURI = ""
		req.RemoteAddr = client.RemoteAddr().String()
		removeHopHeaders(req.Header)

		flow := s.newFlow(req, "https")
		flow.ServerAddr = connectHost
		flow.Process = proc
		flow.TLS = &capture.TLSInfo{
			ClientVersion: tls.VersionName(clientState.Version),
			ServerName:    clientState.ServerName,
		}
		flow.State = capture.StateStreaming
		s.rec.Begin(flow)

		resp, err := s.roundTrip(flow, req)
		if err != nil {
			// 上游失败不影响客户端连接，回 502 后按 keep-alive 继续（roundTrip 已收尾 flow）
			if writeSimpleResponse(bw, http.StatusBadGateway, "Bad Gateway: "+err.Error()) != nil {
				return
			}
			if req.Close {
				return
			}
			continue
		}

		// 上游真实证书链入 Flow.TLS（方案 §4.2）
		if resp.TLS != nil {
			flow.TLS.ServerVersion = tls.VersionName(resp.TLS.Version)
			for _, c := range resp.TLS.PeerCertificates {
				flow.TLS.PeerCerts = append(flow.TLS.PeerCerts, capture.PeerCert{
					Subject:   c.Subject.String(),
					Issuer:    c.Issuer.String(),
					DNSNames:  c.DNSNames,
					NotBefore: c.NotBefore,
					NotAfter:  c.NotAfter,
				})
			}
		}

		removeHopHeaders(resp.Header)
		// 先落响应头（无 body），流式期间 UI 即可看到状态行与首部
		flow.Response = &capture.Message{
			Proto:           resp.Proto,
			StatusCode:      resp.StatusCode,
			Header:          resp.Header.Clone(),
			ContentEncoding: resp.Header.Get("Content-Encoding"),
		}
		s.rec.Update(flow)

		// tee 捕获响应体；resp.Write 自处理状态行/头/Content-Length/chunked
		respCap := capture.NewBodyCapture(capture.MaxBodyCapture)
		body := resp.Body
		resp.Body = &teeReadCloser{Reader: io.TeeReader(body, respCap), Closer: body}
		writeErr := resp.Write(bw)
		flushErr := bw.Flush()
		resp.Body.Close()

		n := int64(len(respCap.Bytes()))
		flow.BytesDown = n
		flow.Response.Body = respCap.Bytes()
		flow.Response.BodyTruncated = respCap.Truncated()
		if writeErr != nil {
			flow.State = capture.StateError
			flow.Err = writeErr.Error()
			s.rec.Finish(flow)
			return
		}
		flow.State = capture.StateDone
		s.rec.Finish(flow)

		if flushErr != nil || req.Close || resp.Close {
			return
		}
	}
}

func isUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") == "" {
		return false
	}
	for _, v := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(v), "upgrade") {
			return true
		}
	}
	return false
}

func writeSimpleResponse(bw *bufio.Writer, status int, msg string) error {
	resp := &http.Response{
		StatusCode:    status,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Body:          io.NopCloser(strings.NewReader(msg + "\n")),
		ContentLength: int64(len(msg) + 1),
		Header:        make(http.Header),
	}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if err := resp.Write(bw); err != nil {
		return err
	}
	return bw.Flush()
}
