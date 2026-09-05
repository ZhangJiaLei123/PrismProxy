package app

// ctl_bridge.go：ctlapi.Service 适配层（M8，方案 §4.12）。
// App 已有同名/近签名 Wails binding，故用 ctlService 适配器包装 *App，
// 控制 API 与 Wails Bindings 共用同一套内部服务层，不另起逻辑。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
)

// 编译期断言：ctlService 实现 ctlapi.Service
var _ ctlapi.Service = (*ctlService)(nil)

// ctlService 把 *App 的 Wails bindings 适配为 ctlapi.Service
type ctlService struct{ app *App }

func newCtlService(a *App) *ctlService { return &ctlService{app: a} }

// ---------- 状态与流量 ----------

func (s *ctlService) Status() map[string]any {
	a := s.app
	ps := a.GetProxyStatus()
	sys := a.GetSystemProxyStatus()
	a.mu.Lock()
	ui := a.ctx != nil
	a.mu.Unlock()
	return map[string]any{
		"proxy": map[string]any{
			"running":    ps.Running,
			"addr":       ps.Addr,
			"mode":       ps.Mode,
			"flowCount":  ps.FlowCount,
			"startError": ps.StartError,
		},
		"systemProxy": map[string]any{
			"state":    sys.State,
			"server":   sys.Server,
			"override": sys.Override,
		},
		"ui":       ui,
		"headless": !ui,
	}
}

func (s *ctlService) ListFlows(limit int, filter string) any {
	metas := s.app.ListFlows()
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter != "" {
		kept := metas[:0]
		for _, m := range metas {
			if strings.Contains(strings.ToLower(m.Host), filter) ||
				strings.Contains(strings.ToLower(m.URL), filter) ||
				strings.Contains(strings.ToLower(m.Path), filter) {
				kept = append(kept, m)
			}
		}
		metas = kept
	}
	if limit > 0 && limit < len(metas) {
		metas = metas[len(metas)-limit:] // 列表 FIFO（旧→新），取最近 N 条
	}
	return metas
}

func (s *ctlService) GetFlow(id string) (any, error) {
	return s.app.GetFlowDetail(id)
}

func (s *ctlService) GetFlowBody(id, which string) (any, error) {
	return s.app.GetFlowBody(id, which)
}

func (s *ctlService) ClearFlows() int {
	a := s.app
	before := len(a.st.List())
	a.st.Clear()
	return before - len(a.st.List()) // 差值=清除条数（置顶保留）
}

// ---------- 规则 ----------

func (s *ctlService) ListRules() any {
	a := s.app
	a.mu.Lock()
	fg := append([]rules.FilterGroup(nil), a.cfg.FilterGroups...)
	dr := append([]rules.DecryptRule(nil), a.cfg.DecryptRules...)
	a.mu.Unlock()
	return map[string]any{
		"filterGroups": fg,
		"decryptRules": dr,
		"quickIgnore": map[string]any{
			"hostGroup":    QuickIgnoreHostGroupID,
			"processGroup": QuickIgnoreProcGroupID,
		},
	}
}

func (s *ctlService) RuleIgnore(target, value string) (bool, error) {
	return s.app.AddQuickIgnore(target, value)
}

func (s *ctlService) RuleGroupSetEnabled(id string, enabled bool) error {
	a := s.app
	a.mu.Lock()
	gi := -1
	for i := range a.cfg.FilterGroups {
		if a.cfg.FilterGroups[i].ID == id {
			gi = i
			break
		}
	}
	if gi < 0 {
		a.mu.Unlock()
		return fmt.Errorf("过滤规则组 %q 不存在", id)
	}
	a.cfg.FilterGroups[gi].Enabled = enabled
	cfg := a.cfg
	a.mu.Unlock()
	if err := cfg.Save(a.cfgDir); err != nil {
		return err
	}
	return a.rebuildEngine()
}

func (s *ctlService) RuleDecrypt(action, host string) error {
	a := s.app
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host 为空")
	}
	var act rules.Action
	switch action {
	case "mitm":
		act = rules.ActionMITM
	case "bypass":
		act = rules.ActionBypass
	default:
		return fmt.Errorf("解密动作须为 mitm|bypass")
	}
	a.mu.Lock()
	a.cfg.DecryptRules = append(a.cfg.DecryptRules, rules.DecryptRule{Action: act, Host: host})
	cfg := a.cfg
	a.mu.Unlock()
	if err := cfg.Save(a.cfgDir); err != nil {
		return err
	}
	return a.rebuildEngine()
}

// ---------- 系统代理 / 设置 ----------

func (s *ctlService) SysProxy(action string) (string, error) {
	a := s.app
	switch action {
	case "status":
		return a.GetSystemProxyStatus().State, nil
	case "on":
		if err := a.SetSystemProxy(true); err != nil {
			return "", err
		}
		return a.GetSystemProxyStatus().State, nil
	case "off":
		if err := a.SetSystemProxy(false); err != nil {
			return "", err
		}
		return a.GetSystemProxyStatus().State, nil
	}
	return "", fmt.Errorf("action 须为 on|off|status")
}

func (s *ctlService) GetSettings() any { return s.app.GetSettings() }

func (s *ctlService) SaveSettings(raw json.RawMessage) ([]string, error) {
	var nu settings.Settings
	if err := json.Unmarshal(raw, &nu); err != nil {
		return nil, fmt.Errorf("配置 JSON 解析失败: %w", err)
	}
	res, err := s.app.SaveSettings(&nu)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.Warnings, nil
}

// ---------- UI 控制（第二步） ----------

// UIClear 清除记录列表（数据级动作）：GUI 与 headless 均清 store，
// GUI 前端经 flow:evict 事件自动同步列表。返回 (清除条数, 是否有 GUI)。
func (s *ctlService) UIClear() (int, bool) {
	cleared := s.ClearFlows()
	a := s.app
	a.mu.Lock()
	ui := a.ctx != nil
	a.mu.Unlock()
	return cleared, ui
}

// UISettings 打开/切换 GUI 设置面板；headless（无 Wails 运行时）返回 ui=false。
func (s *ctlService) UISettings(tab string) bool {
	a := s.app
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return false // headless：无界面可驱动
	}
	if tab == "" {
		tab = "general"
	}
	runtime.EventsEmit(ctx, "ui:open-settings", tab)
	return true
}
