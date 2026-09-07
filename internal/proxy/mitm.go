package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
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

	// 回写必须按 http/1.1 手工组帧（客户端侧 TLS 仅协商 http/1.1，见上方 NextProtos）：
	// ① 不能用 resp.Write 直接写——上游协商到 h2 时 resp.ProtoMajor=2，会写出非法状态行
	//    "HTTP/2.0 200 OK"，h1 客户端（老 okhttp/xutils 等）解析 ProtocolException 判无网络；
	// ② 也不能等读完整个 body 再 flush——SSE/长连接 body 永不 EOF，bufio.Writer 4KB 缓冲
	//    会把小帧（事件/心跳）积压到连接关闭，客户端连响应头都收不到（EventSource 判无网络）。
	// flow.Response.Proto 已记录上游真实协议（h2）供 UI 展示，此处对客户端恒写 HTTP/1.1。
	respCap := capture.NewBodyCapture(capture.MaxBodyCapture)
	n, writeErr := writeH1Response(bw, req, resp, respCap)
	resp.Body.Close()

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

	if req.Close || resp.Close {
		return
	}
	}
}

// writeH1Response 向 MITM 客户端（h1 over TLS）回写响应：先写状态行+首部并立即 flush，
// 再边读上游 body 边转发、每个读块都 flush，保证 SSE/流式响应逐帧实时到达。
// 分帧规则：上游 h2 响应（无 Content-Length、无 Transfer-Encoding）一律转 h1 chunked；
// 其余保留上游 Content-Length 定长帧。返回转发 body 字节数。
func writeH1Response(bw *bufio.Writer, req *http.Request, resp *http.Response, bodyCap *capture.BodyCapture) (int64, error) {
	// 状态行
	if _, err := fmt.Fprintf(bw, "HTTP/1.1 %03d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode)); err != nil {
		return 0, err
	}

	// HEAD 请求与 204/304 无响应体：不带任何分帧首部，写完头即结束
	noBody := req.Method == http.MethodHead || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified
	if noBody {
		resp.Header.Del("Transfer-Encoding")
		resp.Header.Del("Content-Length")
		if err := resp.Header.Write(bw); err != nil {
			return 0, err
		}
		if _, err := bw.WriteString("\r\n"); err != nil {
			return 0, err
		}
		return 0, bw.Flush()
	}

	// hop-by-hop 首部已在调用前剥除（Connection/Transfer-Encoding 等）。
	// h2→h1 无 Content-Length 时按 chunked 组帧，避免读 body 到 EOF 才知道长度；
	// 每块读完立即 flush，SSE/流式响应逐帧实时到达。
	chunked := resp.ContentLength < 0 && resp.Header.Get("Content-Length") == ""
	if cl := resp.Header.Get("Content-Length"); cl == "" && resp.ContentLength >= 0 {
		resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if chunked {
		resp.Header.Set("Transfer-Encoding", "chunked")
	}
	if err := resp.Header.Write(bw); err != nil {
		return 0, err
	}
	if _, err := bw.WriteString("\r\n"); err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}

	buf := make([]byte, 32*1024)
	body := io.TeeReader(resp.Body, bodyCap)
	var n int64
	for {
		nr, er := body.Read(buf)
		if nr > 0 {
			n += int64(nr)
			if chunked {
				fmt.Fprintf(bw, "%x\r\n", nr)
			}
			bw.Write(buf[:nr])
			if chunked {
				bw.WriteString("\r\n")
			}
			if err := bw.Flush(); err != nil {
				return n, err
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return n, er
		}
	}
	if chunked {
		bw.WriteString("0\r\n\r\n")
		bw.Flush()
	}
	return n, nil
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
