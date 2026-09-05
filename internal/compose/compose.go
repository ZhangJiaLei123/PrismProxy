// Package compose 实现 M6 调试重发（Composer，方案 §4.10）：
// 以独立 http.Client 直接发出编辑后的请求（不经自身代理，防回环；可跟随配置的上游代理），
// 响应（含网络失败）以 Source=composer 的新 Flow 存入 store 进列表，可继续迭代。
package compose

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prismproxy/internal/capture"
)

// KV 请求首部键值对（前端按行编辑，顺序保留、允许重复键）
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Request 调试重发请求
type Request struct {
	Method     string `json:"method"`               // 空=GET
	URL        string `json:"url"`                  // 绝对 http/https URL
	Headers    []KV   `json:"headers"`              // 请求首部（逐行可编辑）
	Body       string `json:"body"`                 // 请求体（文本）
	SkipVerify bool   `json:"skipVerify"`           // 跳过 HTTPS 证书校验（默认校验）
}

// Sender 重发执行器：ID 分配、落库与代理 Recorder 共用同一 Store
type Sender struct {
	NewID    func() string
	Store    capture.FlowStore
	Upstream string // 上游 HTTP 代理 host:port（环路防护后；空=直连）
}

// composerHopHeaders 重发时剥离的逐跳/自动派生首部（Go Transport 与重定向会自行处理）
var composerHopHeaders = map[string]struct{}{
	"Connection":       {},
	"Proxy-Connection": {},
	"Proxy-Authenticate": {},
	"Proxy-Authorization": {},
	"Keep-Alive":       {},
	"Te":               {},
	"Trailer":          {},
	"Transfer-Encoding": {},
	"Upgrade":          {},
	"Content-Length":   {},
}

// Send 执行一次重发：请求/响应/失败均落为 Source=composer 的 Flow；网络错误也返回 err 供前端提示
func (s *Sender) Send(ctx context.Context, req Request) (*capture.Flow, error) {
	rawURL := strings.TrimSpace(req.URL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, &url.Error{Op: "parse", URL: rawURL, Err: errInvalidURL}
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}

	flow := capture.NewFlow(s.NewID())
	flow.Source = capture.SourceComposer
	flow.Scheme = u.Scheme
	flow.ServerAddr = u.Host
	procName := "PrismProxy"
	if name, err := os.Executable(); err == nil {
		procName = filepath.Base(name)
	}
	flow.Process = &capture.ProcessInfo{Name: procName}

	reqHeader := http.Header{}
	for _, h := range req.Headers {
		key := strings.TrimSpace(h.Key)
		if key == "" {
			continue
		}
		if _, hop := composerHopHeaders[http.CanonicalHeaderKey(key)]; hop {
			continue
		}
		reqHeader.Add(key, h.Value)
	}
	bodyBytes := []byte(req.Body)
	flow.Request = &capture.Message{
		Method: method,
		URL:    u.String(),
		Proto:  "HTTP/1.1",
		Header: reqHeader.Clone(),
		Body:   bodyBytes,
	}
	flow.BytesUp = int64(len(bodyBytes))
	flow.State = capture.StateStreaming
	s.add(flow)

	// 自定义 TLSClientConfig 会令 Go 保守禁用 h2（方案 §4.1 陷阱）——Composer 明确 HTTP/1.1 only（§4.10 边界）
	tr := &http.Transport{
		DisableCompression: true, // 保真：不解压，展示层按 Content-Encoding 解压（与抓包链路一致）
		DialContext:        (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: req.SkipVerify}, //nolint:gosec // 用户显式开关
	}
	if s.Upstream != "" {
		tr.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: s.Upstream})
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return fail(flow, s, err), err
	}
	httpReq.Header = reqHeader

	resp, err := client.Do(httpReq)
	if err != nil {
		return fail(flow, s, err), err
	}
	defer resp.Body.Close()

	flow.Response = &capture.Message{
		Proto:           resp.Proto,
		StatusCode:      resp.StatusCode,
		Header:          resp.Header.Clone(),
		ContentEncoding: resp.Header.Get("Content-Encoding"),
	}
	s.add(flow) // 响应头先落（与抓包链路一致）

	capBody := capture.NewBodyCapture(capture.MaxBodyCapture)
	n, copyErr := io.Copy(io.Discard, io.TeeReader(resp.Body, capBody))
	flow.BytesDown = n
	flow.Response.Body = capBody.Bytes()
	flow.Response.BodyTruncated = capBody.Truncated()
	if copyErr != nil {
		flow.State = capture.StateError
		flow.Err = copyErr.Error()
	} else {
		flow.State = capture.StateDone
	}
	if flow.Timing != nil {
		flow.Timing.Duration = time.Since(flow.Timing.Start)
	}
	s.add(flow)
	log.Printf("composer %s [%s] %s %s -> %d state=%s dur=%v err=%q",
		flow.ID, flow.Process.Name, method, u.String(), resp.StatusCode, flow.State, flow.Timing.Duration, flow.Err)
	return flow, nil
}

// add 直接落 store——重发流必须进列表，不受抓包过滤规则影响（Recorder.Filter 不接此路径）
func (s *Sender) add(f *capture.Flow) { s.Store.Add(f) }

func fail(flow *capture.Flow, s *Sender, err error) *capture.Flow {
	flow.State = capture.StateError
	flow.Err = err.Error()
	if flow.Timing != nil {
		flow.Timing.Duration = time.Since(flow.Timing.Start)
	}
	s.add(flow)
	log.Printf("composer %s error: %v", flow.ID, err)
	return flow
}

var errInvalidURL = errors.New("URL 非法（仅支持 http/https 绝对地址）")
