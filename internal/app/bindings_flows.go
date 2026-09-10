package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"prismproxy/internal/capture"
	"prismproxy/internal/compose"
	"prismproxy/internal/proxy"
)

// ---------- 流量查询 / cURL / 置顶 / 调试重发 / 原文复制 Bindings（方案 §4.7-4.10） ----------

func (a *App) ListFlows() []FlowMeta {
	flows := a.st.List()
	out := make([]FlowMeta, 0, len(flows))
	for _, f := range flows {
		out = append(out, a.toMeta(f))
	}
	return out
}

func (a *App) GetFlowDetail(id string) (*FlowDetail, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return nil, fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	return a.flowDetail(f), nil
}

// ComposedHeader Composer 请求首部行（前端逐行编辑，顺序保留）
type ComposedHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ComposedRequest 调试重发请求（方案 §4.10）
type ComposedRequest struct {
	Method     string          `json:"method"` // 空=GET
	URL        string          `json:"url"`
	Headers    []ComposedHeader `json:"headers"`
	Body       string          `json:"body"`
	SkipVerify bool            `json:"skipVerify"` // 跳过 HTTPS 证书校验（默认校验）
}

// SendComposed 执行调试重发（M6，方案 §4.10）：独立 http.Client 直连目标（不经自身代理防回环，
// 跟随配置的上游代理），结果以 Source=composer 的新 Flow 入 store 进列表。
// 网络/校验失败也会落一条 error 态 Flow（返回的 err 供前端即时提示）。
func (a *App) SendComposed(req *ComposedRequest) (*FlowDetail, error) {
	if req == nil {
		return nil, fmt.Errorf("请求为空")
	}
	// 上游为全局环境配置：先 projMu 快照再进 a.mu（锁序 projMu → a.mu，禁止反向）。
	a.projMu.Lock()
	upMode, upManual := a.gcfg.UpstreamMode, a.gcfg.UpstreamProxy
	a.projMu.Unlock()
	a.mu.Lock()
	upstream := proxy.GuardUpstreamLoop(resolveUpstream(upMode, upManual, a.addr), hostOfAddr(a.addr), portOfAddr(a.addr))
	rec := a.rec
	st := a.st
	a.mu.Unlock()

	sender := &compose.Sender{
		NewID:    rec.NewID,
		Store:    st,
		Upstream: upstream,
		Gen:      func() uint64 { return a.projGen.Load() }, // M9：重发流也打项目代际（否则切换后永不落盘）
	}
	hdrs := make([]compose.KV, 0, len(req.Headers))
	for _, h := range req.Headers {
		hdrs = append(hdrs, compose.KV{Key: h.Key, Value: h.Value})
	}
	flow, err := sender.Send(context.Background(), compose.Request{
		Method:     req.Method,
		URL:        req.URL,
		Headers:    hdrs,
		Body:       req.Body,
		SkipVerify: req.SkipVerify,
	})
	if err != nil {
		// 参数校验失败（无落库 Flow）时 flow 为 nil；网络失败已落一条 error 态 Flow
		if flow != nil {
			return a.flowDetail(flow), err
		}
		return nil, err
	}
	return a.flowDetail(flow), nil
}

// hostOfAddr / portOfAddr 从监听地址拆出 host/port（供上游代理环路防护；失败返回零值由 Guard 容错）
func hostOfAddr(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return ""
}

func portOfAddr(addr string) uint16 {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if u, err := strconv.ParseUint(p, 10, 16); err == nil {
			return uint16(u)
		}
	}
	return 0
}

// GetFlowBody 取消息体：which = "req" | "resp"；展示层解压（方案 §4.1）
func (a *App) GetFlowBody(id, which string) (*BodyPayload, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return nil, fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	var msg *capture.Message
	switch which {
	case "req":
		msg = f.Request
	case "resp":
		msg = f.Response
	default:
		return nil, fmt.Errorf("which 须为 req|resp")
	}
	if msg == nil {
		return &BodyPayload{}, nil // 响应尚未到达
	}
	// M7：历史流 body 不在内存，惰性回查 SQLite 并缓存到该消息（后续请求走内存）；
	// 回查失败/持久化已关闭时给出提示而非静默空白
	histHint := ""
	if len(msg.Body) == 0 && msg.BodyLen > 0 && f.Source == capture.SourceHistory {
		histHint = a.loadHistBody(msg, id, which)
	}
	p := &BodyPayload{
		Encoding:    msg.ContentEncoding,
		ContentType: msg.Header.Get("Content-Type"),
		Truncated:   msg.BodyTruncated,
		Raw:         msg.Body,
	}
	if len(msg.Body) > 0 {
		dec, err := capture.DecodeBody(msg.ContentEncoding, msg.Body)
		if err != nil {
			p.DecodeErr = err.Error()
		} else {
			p.Body = dec
		}
	} else if histHint != "" {
		p.DecodeErr = histHint
	}
	return p, nil
}

func (a *App) ClearFlows() { a.st.Clear() }

// BuildCurl 生成可直接执行的 cURL 命令：shell = cmd | bash（转义规则见 capture.BuildCurl）
func (a *App) BuildCurl(id, shell string) (*capture.CurlResult, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return nil, fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	return capture.BuildCurl(f, shell)
}

// SetFlowPinned 置顶/取消置顶；置顶流固定顶部、不参与淘汰、Clear 保留（方案 §4.4）
func (a *App) SetFlowPinned(id string, pinned bool) error {
	_, err := a.st.SetPinned(id, pinned)
	return err
}

// GetFlowRawText 生成详情复制文本（方案 §4.9）。
// part = req | resp；kind = headers（起始行+首部原文）| body（解压后文本）| all（完整报文）。
func (a *App) GetFlowRawText(id, part, kind string) (string, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return "", fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	var msg *capture.Message
	switch part {
	case "req":
		msg = f.Request
	case "resp":
		msg = f.Response
	default:
		return "", fmt.Errorf("part 须为 req|resp")
	}
	if msg == nil {
		if part == "req" {
			return "", fmt.Errorf("该流无请求（盲透传隧道）")
		}
		return "", fmt.Errorf("响应尚未到达")
	}
	// M7：历史流 body 惰性回查 SQLite（同 GetFlowBody 口径）；回查失败/持久化关闭时
	// 在 body/all 文本中给出提示，headers 不受影响（头已随元数据加载）
	histHint := ""
	if len(msg.Body) == 0 && msg.BodyLen > 0 && f.Source == capture.SourceHistory {
		histHint = a.loadHistBody(msg, id, part)
	}
	switch kind {
	case "headers":
		return headerBlock(msg, part), nil
	case "body":
		if histHint != "" {
			return "【" + histHint + "】", nil
		}
		return string(msgBodyDecoded(msg)), nil
	case "all":
		var b strings.Builder
		b.WriteString(headerBlock(msg, part))
		b.WriteString("\r\n")
		if histHint != "" {
			b.WriteString("【" + histHint + "】")
		} else {
			b.Write(msgBodyDecoded(msg))
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("kind 须为 headers|body|all")
	}
}

// headerBlock 起始行 + 首部（按 key 排序输出，CRLF）
func headerBlock(msg *capture.Message, part string) string {
	var b strings.Builder
	b.WriteString(startLine(msg, part))
	b.WriteString("\r\n")
	keys := make([]string, 0, len(msg.Header))
	for k := range msg.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range msg.Header[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

// startLine 请求行 / 状态行（响应未存 reason phrase，以状态码文本兜底）
func startLine(msg *capture.Message, part string) string {
	proto := msg.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	if part == "req" {
		target := msg.URL
		if u, err := url.Parse(msg.URL); err == nil && u != nil {
			target = u.RequestURI()
		}
		method := msg.Method
		if method == "" {
			method = "GET"
		}
		return method + " " + target + " " + proto
	}
	return proto + " " + strconv.Itoa(msg.StatusCode) + " " + http.StatusText(msg.StatusCode)
}

// msgBodyDecoded 展示层解压后的消息体（解压失败回退原始字节）
func msgBodyDecoded(msg *capture.Message) []byte {
	if len(msg.Body) == 0 {
		return nil
	}
	if dec, err := capture.DecodeBody(msg.ContentEncoding, msg.Body); err == nil {
		return dec
	}
	return msg.Body
}
