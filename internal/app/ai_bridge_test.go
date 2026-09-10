package app

// ai_bridge_test.go：AI 配置哨兵语义与事件桥闸门互斥（M13 P2-11/P2-13）。
// 事件桥 Go 单测边界（计划 P2-13 出口锚点）：只验证绑定签名（编译期）与闸门
// 互斥/Stop 幂等；含 runtime.EventsEmit 的全链路留 P3/P4 GUI 实测——测试环境
// a.ctx=nil，成功路径的 goroutine 会触发 wails runtime panic，故不可在单测运行。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/settings"
)

// seedAI 完整有效 AI 配置（通过 Validate 的基准值）
func seedAI() settings.AIConfig {
	return settings.AIConfig{
		Enabled: true, Provider: "deepseek",
		BaseURL: "https://api.deepseek.com", APIKey: "sk-abcdef123456",
		Model: "deepseek-chat", Temperature: 0.3,
		TimeoutSec: 120, MaxFlows: 50, MaxKB: 64, Redact: true,
	}
}

// TestSaveAIConfigPatchSentinel 哨兵语义（计划 P2-11）：空串=保持原值、
// __clear__=显式清空、部分更新（未提及字段不动）、空对象/未知字段 → ErrAIBadReq
func TestSaveAIConfigPatchSentinel(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.AI = seedAI()

	// 空串=保持原值（掩码视图不回原 key，防误清）
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"apiKey":""}`)); err != nil {
		t.Fatalf("空串保持应成功: %v", err)
	}
	if a.gcfg.AI.APIKey != "sk-abcdef123456" {
		t.Fatalf("空串不应清 key，得 %q", a.gcfg.AI.APIKey)
	}

	// 部分更新：只发 {apiKey} → 其余字段不动
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"apiKey":"sk-new-987654"}`)); err != nil {
		t.Fatalf("换 key 应成功: %v", err)
	}
	if a.gcfg.AI.APIKey != "sk-new-987654" {
		t.Fatalf("key 未更新: %q", a.gcfg.AI.APIKey)
	}
	if a.gcfg.AI.Model != "deepseek-chat" || a.gcfg.AI.MaxKB != 64 || !a.gcfg.AI.Redact {
		t.Fatalf("未提及字段不应变: %+v", a.gcfg.AI)
	}

	// 反向部分更新：只发 {model} → key 不动
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"model":"gpt-4o-mini"}`)); err != nil {
		t.Fatalf("改 model 应成功: %v", err)
	}
	if a.gcfg.AI.Model != "gpt-4o-mini" || a.gcfg.AI.APIKey != "sk-new-987654" {
		t.Fatalf("部分更新越界: model=%q key=%q", a.gcfg.AI.Model, a.gcfg.AI.APIKey)
	}

	// __clear__=显式清除
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"apiKey":"__clear__"}`)); err != nil {
		t.Fatalf("__clear__ 应成功: %v", err)
	}
	if a.gcfg.AI.APIKey != "" {
		t.Fatalf("__clear__ 后 key 应为空，得 %q", a.gcfg.AI.APIKey)
	}

	// 空对象 / 未知字段 → ErrAIBadReq
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{}`)); !errors.Is(err, ctlapi.ErrAIBadReq) {
		t.Fatalf("空对象应 ErrAIBadReq，得 %v", err)
	}
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"foo":1}`)); !errors.Is(err, ctlapi.ErrAIBadReq) {
		t.Fatalf("未知字段应 ErrAIBadReq，得 %v", err)
	}
}

// TestAIConfigViewMasked 掩码视图（GET /ai/config 与 GetAIConfigApp）：key 永不回原值
func TestAIConfigViewMasked(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.AI = seedAI()

	v, err := a.GetAIConfigView()
	if err != nil {
		t.Fatalf("GetAIConfigView: %v", err)
	}
	if v["hasApiKey"] != true || v["apiKeyMasked"] != "****3456" {
		t.Fatalf("掩码视图异常: %v", v)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "sk-abcdef123456") {
		t.Fatalf("掩码视图泄漏原 key: %s", b)
	}

	appView, err := a.GetAIConfigApp()
	if err != nil || !appView.HasAPIKey || appView.APIKeyMasked != "****3456" {
		t.Fatalf("GetAIConfigApp 异常: %+v err=%v", appView, err)
	}

	// 清除后 hasApiKey=false 且出现 keyMissingWarn（Enabled 且非 Ollama）
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"apiKey":"__clear__"}`)); err != nil {
		t.Fatalf("__clear__: %v", err)
	}
	appView, err = a.GetAIConfigApp()
	if err != nil || appView.HasAPIKey || appView.KeyMissingWarn == "" {
		t.Fatalf("清除后视图异常: %+v err=%v", appView, err)
	}

	// SaveAIConfigApp（主窗独立通道）复用同一哨兵语义，换 key 后 warn 消失
	appView, err = a.SaveAIConfigApp("sk-xyz-8765")
	if err != nil || !appView.HasAPIKey || appView.APIKeyMasked != "****8765" || appView.KeyMissingWarn != "" {
		t.Fatalf("SaveAIConfigApp 异常: %+v err=%v", appView, err)
	}
	if a.gcfg.AI.APIKey != "sk-xyz-8765" {
		t.Fatalf("SaveAIConfigApp 未落 gcfg: %q", a.gcfg.AI.APIKey)
	}
}

// TestAIGateMutexShared 闸门互斥（计划 P2-13）：Wails AIChatStart 与 ctlapi runAIChat
// 共用 aiBusy——占位后两路同步冲突（Wails error 与 HTTP 409 同哨兵文案），
// 冲突路径不起 goroutine、不触 EventsEmit（a.ctx=nil 安全边界）
func TestAIGateMutexShared(t *testing.T) {
	a := newTestApp(t)
	a.aiBusy.Store(true)

	if _, err := a.AIChatStart(ctlapi.AIChatRequest{Mode: "explain", FlowID: "f1"}); !errors.Is(err, ctlapi.ErrAIConflict) {
		t.Fatalf("AIChatStart 冲突应 ErrAIConflict，得 %v", err)
	}
	if err := a.runAIChat(context.Background(), ctlapi.AIChatRequest{Mode: "explain", FlowID: "f1"}, nil); !errors.Is(err, ctlapi.ErrAIConflict) {
		t.Fatalf("runAIChat 冲突应 ErrAIConflict，得 %v", err)
	}
	if !a.aiBusy.Load() {
		t.Fatal("冲突路径不应释放闸门（由持有人释放）")
	}

	// AIChatStop 幂等：未知句柄 nil；已知句柄触发 ctx 取消
	if err := a.AIChatStop(AIChatHandle{Handle: "ghost"}); err != nil {
		t.Fatalf("未知句柄 Stop 应幂等 nil，得 %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.aiChats = map[string]context.CancelFunc{"ai-1": cancel}
	if err := a.AIChatStop(AIChatHandle{Handle: "ai-1"}); err != nil {
		t.Fatalf("Stop 已知句柄应 nil，得 %v", err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop 后 ctx 应已取消")
	}
}
