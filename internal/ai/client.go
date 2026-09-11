// Package ai 提供 M13 复盘 × AI 分析的核心能力：
//   - client.go：OpenAI 兼容 Chat Completions 流式客户端（SSE 解析、首块超时、错误包装、代理透传）
//   - prompt.go：四模式（explain/intent/locate/flowmap）Prompt 构造 + 双预算裁剪 + 脱敏 + 文末 JSON 块解析
//
// 本包零外部依赖、不 import 项目其他包（Config.ProxyURL 由接线层算好传入，
// 避免 import app 循环依赖；设计稿 §5.2 v2.1 定稿）。
package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// 角色常量（OpenAI 兼容）
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

const (
	// firstChunkTimeoutMax 首块超时上限（云端/自托管 Timeout 未设时的兜底）：
	// 30s 内收不到首个 data 帧即视为服务不可用。
	// 不用 http.Client.Timeout 的原因：整体超时会掐断长回答的流式输出（设计稿 §5.2）。
	firstChunkTimeoutMax = 30 * time.Second
	// errBodyLimit 非 200 响应读体上限（截断 2KB 解析 error.message）。
	errBodyLimit = 2048
	// scanInitBuf / scanMaxBuf SSE 行扫描缓冲：初始 64KB、上限 1MB
	//（长 JSON 帧如 base64 图片字段可能超 64KB，Scanner 默认上限 64KB 会中断流）。
	scanInitBuf = 64 * 1024
	scanMaxBuf  = 1024 * 1024
)

// Config AI 客户端配置（接线层从 settings.AIConfig + 代理装配结果换算）。
type Config struct {
	BaseURL     string        // OpenAI 兼容接口地址（内部自动归一化补 /v1）
	APIKey      string        // Bearer 凭据；空则不发 Authorization 头（自托管 Ollama）
	Model       string        // 模型名，如 deepseek-chat
	Temperature float64       // 采样温度（0 为哨兵，调用方应先经 WithDefaults 兜底）
	Timeout     time.Duration // 整请求超时（ctx 之外的最后兜底；流式下=首块看门狗阈值：云端 min(30s, Timeout/4)、自托管 Timeout 全额）
	ProxyURL    string        // 出站代理（接线层按 UpstreamMode 算好传入；空=直连），本包不反查全局配置
}

// Message 一条对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Delta 一个流式增量块。
type Delta struct {
	Text   string // 正文增量（choices[0].delta.content）
	Reason string // 可选：推理模型 reasoning 增量（deepseek-reasoner 等），透传由前端决定折叠展示
}

// Client OpenAI 兼容流式客户端。
type Client struct {
	cfg  Config
	http *http.Client // 禁设 Timeout（杀流式）；首块超时 + 外层 ctx 兜底
}

// NewClient 构造客户端。ProxyURL 空=直连。
func NewClient(cfg Config) *Client {
	tr := &http.Transport{
		// 不设 TLSClientConfig（保住默认 ForceAttemptHTTP2 的 h2 能力）；无自定义超时字段
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if cfg.ProxyURL != "" {
		if u, err := url.Parse(cfg.ProxyURL); err == nil && u.Scheme != "" {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{cfg: cfg, http: &http.Client{Transport: tr}}
}

// normalizeBaseURL 归一化 BaseURL：去空白/尾 /；末段为 /v<纯数字>（如 /v1、智谱 /v4）
// 则保留，否则补 /v1。与 settings.NormalizeAIBaseURL 同语义镜像（小函数重复换解耦，避免 import settings）。
func normalizeBaseURL(raw string) string {
	b := strings.TrimRight(strings.TrimSpace(raw), "/")
	if b == "" || endsWithVersionSeg(b) {
		return b
	}
	return b + "/v1"
}

// endsWithVersionSeg 判断末段是否为 /v<纯数字> 形态（v1/v4/...），镜像 settings 包实现。
func endsWithVersionSeg(b string) bool {
	i := strings.LastIndex(b, "/")
	if i < 0 {
		return false
	}
	v := b[i+1:]
	if len(v) < 2 || v[0] != 'v' {
		return false
	}
	for j := 1; j < len(v); j++ {
		if v[j] < '0' || v[j] > '9' {
			return false
		}
	}
	return true
}

// firstChunkTimeout 首块超时按部署形态区分（设计稿 §5.2 v2.3）：
// 自托管（未配 APIKey，Ollama/LM Studio 等）取 Timeout 全额——本地模型首 token 前需
// 完成冷加载 + 全量 prompt prefill，实测 64KB prompt 温机 TTFB≈17s，30s 上限会误杀；
// 云端（已配 APIKey）维持 min(30s, Timeout/4) 快速判死。Timeout 未设（<=0）时兜底 30s。
func (c *Client) firstChunkTimeout() time.Duration {
	if c.cfg.Timeout <= 0 {
		return firstChunkTimeoutMax
	}
	if c.cfg.APIKey == "" {
		return c.cfg.Timeout
	}
	ft := c.cfg.Timeout / 4
	if ft > firstChunkTimeoutMax {
		ft = firstChunkTimeoutMax
	}
	return ft
}

// firstChunkTimeoutErr 首块看门狗超时的统一错误文案；自托管附加「调大超时/确认服务可达」提示。
func (c *Client) firstChunkTimeoutErr(ft time.Duration) error {
	if c.cfg.APIKey == "" {
		return fmt.Errorf("连接服务商超时：%.0f 秒内未收到首个响应。本地模型冷启动/长文本推理可能较慢，可调大设置中的超时时间后重试；若持续超时，请确认服务已启动、地址可达", ft.Seconds())
	}
	return fmt.Errorf("连接服务商超时：%.0f 秒内未收到首个响应，请检查网络或代理设置", ft.Seconds())
}

// endpoint 请求地址 = 归一化 BaseURL + /chat/completions。
func (c *Client) endpoint() string {
	return normalizeBaseURL(c.cfg.BaseURL) + "/chat/completions"
}

// streamOptions OpenAI 流式选项；include_usage=false 不要求最后 usage 帧。
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Temperature   float64        `json:"temperature"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"` // 仅 Probe 非流式探测用
}

// SSE 帧：取 choices[0].delta；上游把错误也以 200 + error 帧下发时在此识别。
type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Stream 发起一次流式对话。onDelta 在收到增量块时回调（同 goroutine 顺序调用）；
// ctx 取消即关闭 HTTP 连接并返回 ctx.Err()。
//
// 超时模型（设计稿 §5.2 v2.3）：不设 http.Client.Timeout；启动「首块超时」定时器
// （云端 min(30s, Timeout/4)、自托管 Timeout 全额），收到首个 data 帧后撤销，
// 整体由外层 ctx 兜底——长回答不再被整体超时掐断。
func (c *Client) Stream(ctx context.Context, messages []Message, onDelta func(Delta)) error {
	reqBody, err := json.Marshal(chatRequest{
		Model:         c.cfg.Model,
		Messages:      messages,
		Temperature:   c.cfg.Temperature,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: false},
	})
	if err != nil {
		return fmt.Errorf("构造请求失败：%w", err)
	}

	// 看门狗 ctx：首块超时取消，父 ctx 取消也联动取消
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstSeen, timedOut atomic.Bool
	ft := c.firstChunkTimeout()
	timer := time.AfterFunc(ft, func() {
		// 首块已到则不触发（Stop 与本回调的竞态下，靠 timedOut 标记在读取侧裁决）
		if !firstSeen.Load() {
			timedOut.Store(true)
			cancel()
		}
	})
	defer timer.Stop()

	req, err := http.NewRequestWithContext(watchCtx, http.MethodPost, c.endpoint(), strings.NewReader(string(reqBody)))
	if err != nil {
		return fmt.Errorf("构造请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		if timedOut.Load() {
			return c.firstChunkTimeoutErr(ft)
		}
		if ctx.Err() != nil {
			return ctx.Err() // 用户主动停止：正常关闭路径，由编排层转为「已停止」
		}
		return fmt.Errorf("连接服务商失败：%v，请检查网络或代理设置", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return wrapHTTPError(resp)
	}

	// 逐行扫 SSE：只关心 data: 行；event:/注释/空行跳过
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, scanInitBuf), scanMaxBuf)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return nil
		}
		// 收到首个数据帧：撤销看门狗（先 CAS 标记再 Stop，避免与定时器竞态漏判）
		if !firstSeen.Load() {
			firstSeen.Store(true)
			timer.Stop()
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 容错：跳过无法解析的杂帧（部分网关会夹心跳/日志）
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("服务商返回错误：%s", chunk.Error.Message)
		}
		if len(chunk.Choices) > 0 {
			d := chunk.Choices[0].Delta
			if d.Content != "" || d.ReasoningContent != "" {
				onDelta(Delta{Text: d.Content, Reason: d.ReasoningContent})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if timedOut.Load() {
			return c.firstChunkTimeoutErr(ft)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("读取响应流失败：%w", err)
	}
	// 流正常结束（部分服务商不发 [DONE] 直接关连接，视为完成）
	return nil
}

// wrapHTTPError 非 200：读体（截 2KB）解析 error.message，包装为可读错误（设计稿 §5.2）。
func wrapHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	if msg := extractErrorMessage(body); msg != "" {
		return fmt.Errorf("服务商返回 %d：%s", resp.StatusCode, msg)
	}
	return fmt.Errorf("服务商返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// extractErrorMessage 从错误响应体提取 message：优先 {"error":{"message":...}}，退化截断原文。
func extractErrorMessage(body []byte) string {
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Error.Message != "" {
		return wrapped.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// probeResponse 非流式响应（Probe 用）。
type probeResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Probe 非流式极简探测（max_tokens:8），供「测试连接」使用；返回模型回复文本。
// 调用方负责计时与文案组装。
func (c *Client) Probe(ctx context.Context, messages []Message) (string, error) {
	reqBody, err := json.Marshal(chatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: c.cfg.Temperature,
		Stream:      false,
		MaxTokens:   8,
	})
	if err != nil {
		return "", fmt.Errorf("构造请求失败：%w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), strings.NewReader(string(reqBody)))
	if err != nil {
		return "", fmt.Errorf("构造请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("连接服务商失败：%v，请检查网络或代理设置", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", wrapHTTPError(resp)
	}
	var out probeResponse
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("读取响应失败：%w", err)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("响应解析失败：%w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return "", fmt.Errorf("服务商返回错误：%s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("服务商返回空结果")
	}
	return out.Choices[0].Message.Content, nil
}

// modelsResponse OpenAI 兼容 GET /models 响应（Ollama /v1/models、火山 Ark /api/v3/models 等同形）。
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels 拉取模型列表（OpenAI 兼容 GET {归一化 BaseURL}/models），供设置页
// 「获取模型」下拉建议。返回去空白、去重后的模型 id；key 非空发 Bearer（Ollama 免鉴权）。
// 空列表视为错误（可用服务至少返回一个模型）。
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeBaseURL(c.cfg.BaseURL)+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败：%w", err)
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("连接服务商失败：%v，请检查网络或代理设置", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, wrapHTTPError(resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	var out modelsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("响应解析失败：%w", err)
	}
	seen := make(map[string]struct{}, len(out.Data))
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("服务商返回空模型列表")
	}
	return models, nil
}
