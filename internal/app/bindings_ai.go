package app

// bindings_ai.go：AI 分析 Wails 主窗绑定（M13 P2-9/P2-13，设计稿 §六/§7.3）。
// 主窗内嵌复盘页无 ctlapi token、wails.localhost fetch 9595 受 CORS 限制、SSE 无法
// 走绑定——chat 经事件桥：AIChatStart 起后台会话逐帧 EventsEmit("ai:chat",
// {handle, event, data})，AIChatStop 取消（语义同 HTTP 断连）；密钥独立存取
// （GetAIConfigApp/SaveAIConfigApp）直连 app 层不经 ctlapi（设计稿 §六第 4 项）。
//
// Wails 铁律（M12.3 踩坑⑥）：绑定方法最多 2 返回值且末位 error；本文件 DTO 一律单结构。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/settings"
)

// ---------- DTO（Wails 序列化；同步 frontend/wailsjs 三文件，P2-12） ----------

// AITestResult 测试连接结果（TestAIConnection）：成功 message=上游回复原文，
// 失败 message=上游错误原文（ok=false 走正常返回，仅配置缺失等走 error，P2-4 语义）。
type AITestResult struct {
	Model     string `json:"model"`
	LatencyMS int64  `json:"latencyMs"`
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
}

// AIModelsResult 模型列表结果（FetchAIModels，设置页「获取模型」下拉建议）。
type AIModelsResult struct {
	Models []string `json:"models"`
}

// AIChatHandle AI 分析会话句柄：AIChatStart 返回，AIChatStop 按其取消。
type AIChatHandle struct {
	Handle string `json:"handle"`
}

// AIConfigView 主窗 AiTab 密钥独立存取视图（GetAIConfigApp/SaveAIConfigApp）：
// 永不回原 key，仅掩码辅助辨认；keyMissingWarn 供前端引导提示。
type AIConfigView struct {
	HasAPIKey      bool   `json:"hasApiKey"`
	APIKeyMasked   string `json:"apiKeyMasked"`
	KeyMissingWarn string `json:"keyMissingWarn"`
}

// ---------- 测试连接（P2-9） ----------

// aiDraftKey 解析条目草稿测试/拉模型时的生效 key：草稿 key 非空直接用；为空时仅当
// 草稿端点与已存当前条目（顶层快照）同端点（归一化 BaseURL 一致）才回填已存 key，
// 不同端点不外发凭据（多供应商条目下避免 A 家 key 发往 B 家，宁缺勿错——key 缺失
// 由上游按未配置报错）。调用方须持 projMu，stored 传 a.gcfg.AI 值拷贝。
func aiDraftKey(cfg settings.AIConfig, stored settings.AIConfig) string {
	if strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	if settings.NormalizeAIBaseURL(cfg.BaseURL) == settings.NormalizeAIBaseURL(stored.BaseURL) {
		return stored.APIKey
	}
	return ""
}

// TestAIConnection 用界面当前值探测（未落盘可测，设计稿 §六第 9 项/AC2）：BaseURL/
// 模型/温度等全按入参；key 留空且与已存当前条目同端点时回填已存 key（异端点不外发）。
// 不落盘；配置缺失返回 error，网络/服务商失败为正常业务结果（ok=false + 上游原文）。
func (a *App) TestAIConnection(cfg settings.AIConfig) (*AITestResult, error) {
	a.projMu.Lock()
	upMode, upProxy, listen := a.gcfg.UpstreamMode, a.gcfg.UpstreamProxy, a.gcfg.ListenAddr
	cfg.APIKey = aiDraftKey(cfg, a.gcfg.AI)
	a.projMu.Unlock()
	return a.aiProbe(cfg, resolveUpstream(upMode, upProxy, listen))
}

// ---------- 获取模型列表（OpenAI 兼容 GET {base}/models，设置页「获取模型」） ----------

// FetchAIModels 用界面当前值拉取模型列表：语义同 TestAIConnection——key 留空且与
// 已存当前条目同端点时回填已存 key（异端点不外发）、不落盘；出站代理随全局
// UpstreamMode。仅 Wails 主窗使用（无 HTTP 状态码映射需求），BaseURL 缺失与
// 网络/服务商失败一律走 error 由前端 toast 呈现。
func (a *App) FetchAIModels(cfg settings.AIConfig) (*AIModelsResult, error) {
	a.projMu.Lock()
	upMode, upProxy, listen := a.gcfg.UpstreamMode, a.gcfg.UpstreamProxy, a.gcfg.ListenAddr
	cfg.APIKey = aiDraftKey(cfg, a.gcfg.AI)
	a.projMu.Unlock()
	models, err := a.aiListModels(cfg, resolveUpstream(upMode, upProxy, listen))
	if err != nil {
		return nil, err
	}
	return &AIModelsResult{Models: models}, nil
}

// ---------- chat 事件桥（P2-13） ----------

// aiChatSeq 会话句柄序号（进程内单调；句柄仅用于本实例 AIChatStop 匹配）。
var aiChatSeq atomic.Uint64

// AIChatStart 发起一次 AI 分析会话：与 ctlapi chat 共用 runAIChatOnce 编排与
// aiBusy 闸门（并发冲突返回与 409 同文案的 error）；事件经 "ai:chat" 频道逐帧
// 下发，每帧 {handle, event, data}，event ∈ meta|delta|intent|match|notice|error|done。
// 同步错误段（meta 帧前，如未配置 400 类）在事件桥无 HTTP 状态码，转 error 帧。
func (a *App) AIChatStart(req ctlapi.AIChatRequest) (AIChatHandle, error) {
	if err := a.aiGateAcquire(); err != nil {
		return AIChatHandle{}, err
	}
	h := AIChatHandle{Handle: fmt.Sprintf("ai-%d", aiChatSeq.Add(1))}
	ctx, cancel := context.WithCancel(context.Background())

	a.aiChatMu.Lock()
	if a.aiChats == nil {
		a.aiChats = make(map[string]context.CancelFunc)
	}
	a.aiChats[h.Handle] = cancel
	a.aiChatMu.Unlock()

	go func() {
		// 收敛路径：闸门释放 / 会话注销 / ctx 兜底取消（注销先行、cancel 兜底
		// Stop 在注销后到达的场景）
		defer a.aiBusy.Store(false)
		defer func() {
			a.aiChatMu.Lock()
			delete(a.aiChats, h.Handle)
			a.aiChatMu.Unlock()
			cancel()
		}()
		emit := func(event string, data any) error {
			runtime.EventsEmit(a.ctx, "ai:chat", map[string]any{"handle": h.Handle, "event": event, "data": data})
			return nil // EventsEmit 无 error：emitSafe 中断仅由 AIChatStop 取消触发
		}
		if err := a.runAIChatOnce(ctx, req, emit); err != nil {
			// 同步错误段（meta 帧前）：事件桥无状态码，转 error 帧后正常收尾
			emit(ctlapi.AIEventError, map[string]any{"message": err.Error()})
		}
	}()
	return h, nil
}

// AIChatStop 停止进行中的分析会话（ctx cancel，语义同 HTTP 断连：静默收尾、
// 不发 error 帧，设计稿 §5.5）。句柄已结束/不存在则幂等返回 nil。
func (a *App) AIChatStop(h AIChatHandle) error {
	a.aiChatMu.Lock()
	cancel, ok := a.aiChats[h.Handle]
	a.aiChatMu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

// ---------- 密钥独立存取（P2-13，主窗 AiTab 直连 app 层） ----------

// GetAIConfigApp 密钥掩码视图（AiTab 进入时回填 hasApiKey/apiKeyMasked，设计稿 §六第 4 项）。
func (a *App) GetAIConfigApp() (*AIConfigView, error) {
	a.projMu.Lock()
	g := *a.gcfg
	a.projMu.Unlock()
	return &AIConfigView{
		HasAPIKey:      g.AI.APIKey != "",
		APIKeyMasked:   maskAPIKey(g.AI.APIKey),
		KeyMissingWarn: g.WarnNoKey(),
	}, nil
}

// SaveAIConfigApp 保存/清除密钥：哨兵语义与 POST /ai/config 的 apiKey 字段一致
// （空串=保持、__clear__=清除、传值=换 key），复用 SaveAIConfigPatch 单字段部分
// 更新（其余字段不受影响，P3-3 两通道不打架），成功后回读掩码视图。
func (a *App) SaveAIConfigApp(key string) (*AIConfigView, error) {
	raw, err := json.Marshal(map[string]string{"apiKey": key})
	if err != nil {
		return nil, err
	}
	if _, err := a.SaveAIConfigPatch(raw); err != nil {
		return nil, err
	}
	return a.GetAIConfigApp()
}
