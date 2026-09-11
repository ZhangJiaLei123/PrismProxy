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

	// 纯空白=未输入，保持原值（审计低-1：与空串哨兵同防误清语义）
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"apiKey":"   "}`)); err != nil {
		t.Fatalf("纯空白保持应成功: %v", err)
	}
	if a.gcfg.AI.APIKey != "sk-abcdef123456" {
		t.Fatalf("纯空白不应清 key，得 %q", a.gcfg.AI.APIKey)
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

	// entries 条目清单：归一化（trim/去空/三元组去重/Current 唯一化）+ 当前条目快照同步顶层
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"entries":[{"provider":"deepseek","model":" deepseek-chat ","alias":" 主力 ","baseUrl":" https://api.deepseek.com/ ","apiKey":" sk-ds-1 ","current":true},{"model":""},{"provider":"deepseek","model":"deepseek-chat","alias":"dup","baseUrl":"https://api.deepseek.com","current":true}]}`)); err != nil {
		t.Fatalf("更新 entries 应成功: %v", err)
	}
	if len(a.gcfg.AI.Entries) != 1 || a.gcfg.AI.Entries[0].Model != "deepseek-chat" || a.gcfg.AI.Entries[0].Alias != "主力" || a.gcfg.AI.Entries[0].APIKey != "sk-ds-1" {
		t.Fatalf("entries 应归一化去重: %+v", a.gcfg.AI.Entries)
	}
	if a.gcfg.AI.Model != "deepseek-chat" || a.gcfg.AI.APIKey != "sk-ds-1" || a.gcfg.AI.Provider != "deepseek" {
		t.Fatalf("entries 更新应同步当前条目到顶层: %+v", a.gcfg.AI)
	}
	v, err := a.GetAIConfigView()
	if err != nil {
		t.Fatalf("GetAIConfigView: %v", err)
	}
	// 视图为脱敏条目（model/alias/baseUrl/hasKey/current，永不回原文 key）
	ves, ok := v["entries"].([]map[string]any)
	if !ok || len(ves) != 1 || ves[0]["alias"] != "主力" || ves[0]["hasKey"] != true || ves[0]["current"] != true {
		t.Fatalf("视图应回传脱敏 entries: %v", v["entries"])
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), "sk-ds-1") {
		t.Fatalf("条目视图泄漏原 key: %s", b)
	}

	// 双字段同请求（审计问题2回归）：map 遍历无序，entries+apiKey 同请求时 apiKey
	// 补丁在 entries 同步之后最后生效——顶层=补丁值，且经 SyncKeyToCurrent 回写当前条目
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"entries":[{"provider":"deepseek","model":"deepseek-chat","baseUrl":"https://api.deepseek.com","apiKey":"sk-entry","current":true}],"apiKey":"sk-patch"}`)); err != nil {
		t.Fatalf("entries+apiKey 双字段应成功: %v", err)
	}
	if a.gcfg.AI.APIKey != "sk-patch" {
		t.Fatalf("apiKey 补丁不应被 SyncAICurrent 覆盖，得 %q", a.gcfg.AI.APIKey)
	}
	if len(a.gcfg.AI.Entries) != 1 || a.gcfg.AI.Entries[0].APIKey != "sk-patch" {
		t.Fatalf("apiKey 补丁应经 SyncKeyToCurrent 回写当前条目: %+v", a.gcfg.AI.Entries)
	}

	// __clear__=显式清除（顶层 key 清空并经 SyncKeyToCurrent 回写当前条目，防回退）
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"apiKey":"__clear__"}`)); err != nil {
		t.Fatalf("__clear__ 应成功: %v", err)
	}
	if a.gcfg.AI.APIKey != "" {
		t.Fatalf("__clear__ 后 key 应为空，得 %q", a.gcfg.AI.APIKey)
	}
	if len(a.gcfg.AI.Entries) == 1 && a.gcfg.AI.Entries[0].APIKey != "" {
		t.Fatalf("__clear__ 后当前条目 key 应同步清空: %+v", a.gcfg.AI.Entries)
	}

	// 空对象 / 未知字段 / 坏 JSON / 坏字段类型 → ErrAIBadReq（解析错误不得伪装 500）
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{}`)); !errors.Is(err, ctlapi.ErrAIBadReq) {
		t.Fatalf("空对象应 ErrAIBadReq，得 %v", err)
	}
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"foo":1}`)); !errors.Is(err, ctlapi.ErrAIBadReq) {
		t.Fatalf("未知字段应 ErrAIBadReq，得 %v", err)
	}
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{`)); !errors.Is(err, ctlapi.ErrAIBadReq) {
		t.Fatalf("坏 JSON 应 ErrAIBadReq，得 %v", err)
	}
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"enabled":"yes"}`)); !errors.Is(err, ctlapi.ErrAIBadReq) {
		t.Fatalf("坏字段类型应 ErrAIBadReq，得 %v", err)
	}
}

// TestSaveAIConfigPatchDefaultsNotPersisted 兜底不固化（P0-7 红线）+ 温度哨兵放行：
// 落盘保留哨兵 0 值（Provider=""/Temperature=0/MaxKB=0），视图展示兜底生效值
func TestSaveAIConfigPatchDefaultsNotPersisted(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.AI = settings.AIConfig{}

	// 未初始化配置部分更新：校验用兜底副本通过，落盘不固化 WithDefaults 结果
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"enabled":true,"baseUrl":"https://x.example","model":"m1"}`)); err != nil {
		t.Fatalf("未初始化配置部分更新应成功: %v", err)
	}
	if a.gcfg.AI.Provider != "" || a.gcfg.AI.Temperature != 0 || a.gcfg.AI.MaxKB != 0 {
		t.Fatalf("兜底值不应固化落盘: %+v", a.gcfg.AI)
	}
	v, err := a.GetAIConfigView()
	if err != nil {
		t.Fatalf("GetAIConfigView: %v", err)
	}
	if v["temperature"] != 0.3 || v["provider"] != "custom" {
		t.Fatalf("视图应展示兜底生效值: %v", v)
	}

	// 已初始化配置显式 temperature:0 → 哨兵语义：校验放行、落盘保留 0、视图显示 0.3
	a.gcfg.AI = seedAI()
	if _, err := a.SaveAIConfigPatch(json.RawMessage(`{"temperature":0}`)); err != nil {
		t.Fatalf("显式 0 应回到哨兵语义: %v", err)
	}
	if a.gcfg.AI.Temperature != 0 {
		t.Fatalf("0 应作为哨兵保留（不固化 0.3）: %v", a.gcfg.AI.Temperature)
	}
	v, err = a.GetAIConfigView()
	if err != nil {
		t.Fatalf("GetAIConfigView: %v", err)
	}
	if v["temperature"] != 0.3 {
		t.Fatalf("视图温度应显示生效值 0.3: %v", v["temperature"])
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
