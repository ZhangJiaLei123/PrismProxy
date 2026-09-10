// ai.go M13 复盘 × AI 分析 HTTP 面（设计《复盘AI分析设计.md》§5.3/§5.5）：
//   - GET  /api/v1/ai/config → 掩码视图（apiKey 永不回原值，仅 hasApiKey/apiKeyMasked）
//   - POST /api/v1/ai/config → 部分更新（出现的字段才覆盖；apiKey 空串=保持、"__clear__"=清空）
//   - POST /api/v1/ai/test   → 连通探测（非流式 max_tokens:8、10s，不落盘）
//   - POST /api/v1/ai/chat   → SSE 分析流（帧序 meta → (delta|intent|match)* → done）
//
// 流程约定（handler 与实现层的错误分界）：StreamAIChat 实现层在首次 emit 前返回的
// error 视为同步错误（handler 据 sentinel 映射 400/409/500 JSON 响应）；
// 首次 emit 后的错误必须经 error 帧投递（实现层发完 return nil），
// 客户端断开（r.Context() 取消）静默关闭、不发 error 帧（§5.5）。
package ctlapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
)

// AIChatRequest POST /ai/chat 请求体（Wails AIChatStart 复用同一结构，设计 §5.3）。
type AIChatRequest struct {
	Mode     string        `json:"mode"`     // explain|intents|locate|flowmap（对应 ai.ChatMode）
	FlowID   string        `json:"flowId"`   // explain 单流 id
	IDs      []string      `json:"ids"`      // intents/locate/flowmap 批量流 id
	Question string        `json:"question"` // locate/flowmap 用户目标
	Options  AIChatOptions `json:"options"`
}

// AIChatOptions 分析附加选项。
type AIChatOptions struct {
	IncludeReqBody  bool   `json:"includeReqBody"`
	IncludeRespBody bool   `json:"includeRespBody"`
	Language        string `json:"language"` // 一期仅 zh（模板即中文），字段留扩展
}

// AIChatEmit 事件推送函数：SSE handler 逐帧编码；Wails 事件桥转 EventsEmit。
// 返回 error 表示连接已断/帧写出失败，实现层应立即终止编排。
type AIChatEmit func(event string, data any) error

// AI 事件名（设计 §5.3 帧协议）
const (
	AIEventMeta   = "meta"   // {mode,total,sent,budget:{flows,kb},truncated}
	AIEventDelta  = "delta"  // {text}
	AIEventIntent = "intent" // {flowId,seq,intent,confidence,needsBody}
	AIEventMatch  = "match"  // {flowId,rank,method,url,reason,confidence}
	AIEventError  = "error"  // {message}
	AIEventDone   = "done"   // {finishReason,truncated}
)

// 同步错误 sentinel：StreamAIChat 实现层在首次 emit 前 return（可 fmt.Errorf("%w: …") 包装），
// handler 据此映射 HTTP 码；首次 emit 后不再适用（错误走 error 帧）。
var (
	ErrAIConflict = errors.New("已有 AI 分析任务在进行，请等待完成或停止") // → 409
	ErrAIBadReq   = errors.New("AI 请求非法")                            // → 400（未配置/参数/ids 全失效）
)

// aiErrCode 同步错误 → HTTP 状态码（未识别错误视为内部错误）
func aiErrCode(err error) int {
	switch {
	case errors.Is(err, ErrAIConflict):
		return http.StatusConflict
	case errors.Is(err, ErrAIBadReq):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// handleAIConfig GET=掩码视图 / POST=部分更新（成功回传更新后的掩码视图）
func (s *Server) handleAIConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		v, err := s.svc.GetAIConfig()
		if err != nil {
			writeErr(w, aiErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPost:
		raw, err := readBody(r, 64<<10)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		v, err := s.svc.SaveAIConfig(raw)
		if err != nil {
			writeErr(w, aiErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
	}
}

// handleAITest POST 连通探测：{ok, model, latencyMs, message}；未配置等请求类错误 → 400
func (s *Server) handleAITest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	v, err := s.svc.AITestConnection()
	if err != nil {
		writeErr(w, aiErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleAIChat POST SSE 分析流。首帧 meta 由实现层在全部同步校验（未配置/参数/ids/
// 并发闸门）通过后立即发出——同步错误在响应头写出前捕获并映射 JSON。
func (s *Server) handleAIChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	raw, err := readBody(r, 1<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req AIChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体非法: "+err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "服务端不支持流式响应")
		return
	}

	started := false
	emit := func(event string, data any) error {
		if !started {
			h := w.Header()
			h.Set("Content-Type", "text/event-stream")
			h.Set("Cache-Control", "no-cache")
			h.Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		b, mErr := json.Marshal(data)
		if mErr != nil {
			return mErr
		}
		if _, wErr := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); wErr != nil {
			return wErr
		}
		flusher.Flush()
		return nil
	}

	if err := s.svc.StreamAIChat(r.Context(), req, emit); err != nil {
		if !started {
			// 同步阶段错误：映射 JSON（未配置=400、并发=409、其余=500）
			writeErr(w, aiErrCode(err), err.Error())
			return
		}
		// 已开流：客户端断开（ctx 取消）或帧写出失败，仅记录不补帧
		log.Printf("AI chat 流中断: %v", err)
	}
}
