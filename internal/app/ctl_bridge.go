package app

// ctl_bridge.go：ctlapi.Service 适配层（M8，方案 §4.12）。
// App 已有同名/近签名 Wails binding，故用 ctlService 适配器包装 *App，
// 控制 API 与 Wails Bindings 共用同一套内部服务层，不另起逻辑。
//
// M9（项目配置设计 §6.3）：规则/设置类方法支持目标项目选择（project 参数，
// 空=当前项目，否则按 id 精确 → 名称匹配解析）；对非当前项目的写只改其
// project.json 文件，不改变 currentProject、不触发引擎热切换。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/domains"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
)

// 编译期断言：ctlService 实现 ctlapi.Service
var _ ctlapi.Service = (*ctlService)(nil)

// ctlService 把 *App 的 Wails bindings 适配为 ctlapi.Service
type ctlService struct{ app *App }

func newCtlService(a *App) *ctlService { return &ctlService{app: a} }

// ---------- 状态与流量 ----------

func (s *ctlService) Status() map[string]any { return s.app.ctlStatusSnapshot() }

// ctlStatusSnapshot 构造控制面状态快照：ctlService.Status()（GET /status）与
// SSE status 频道（seed 帧 + 各发布点）共用，避免双份构造漂移。
func (a *App) ctlStatusSnapshot() map[string]any {
	ps := a.GetProxyStatus()
	sys := a.GetSystemProxyStatus()
	a.mu.Lock()
	ui := a.ctx != nil
	a.mu.Unlock()
	a.projMu.Lock()
	cur := a.currentMeta()
	a.projMu.Unlock()
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
		"project":  map[string]any{"id": cur.ID, "name": cur.Name},
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

// ---------- 项目（M9，设计 §6.3） ----------

// resolveProjectIDLocked 按 id 精确 → 名称匹配（区分大小写；重名提示改用 id）→ 报错列清单。
// 空串返回当前项目 id。调用方须持 projMu。
func (a *App) resolveProjectIDLocked(idOrName string) (string, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return a.proj.ID, nil
	}
	for _, m := range a.gcfg.Projects {
		if m.ID == idOrName {
			return m.ID, nil
		}
	}
	var named []string
	for _, m := range a.gcfg.Projects {
		if m.Name == idOrName {
			named = append(named, m.ID)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return "", fmt.Errorf("项目 %q 不存在（可用项目: %s）", idOrName, a.projectListLocked())
	default:
		return "", fmt.Errorf("项目名称 %q 有 %d 个匹配，请改用 id 指定（可用项目: %s）",
			idOrName, len(named), a.projectListLocked())
	}
}

// projectListLocked 「名称(id)」清单串（错误提示用；调用方持 projMu）
func (a *App) projectListLocked() string {
	parts := make([]string, 0, len(a.gcfg.Projects))
	for _, m := range a.gcfg.Projects {
		parts = append(parts, fmt.Sprintf("%s(%s)", m.Name, m.ID))
	}
	return strings.Join(parts, ", ")
}

func (s *ctlService) ListProjects() any {
	a := s.app
	a.projMu.Lock()
	defer a.projMu.Unlock()
	metas := append([]settings.ProjectMeta(nil), a.gcfg.Projects...)
	return map[string]any{"projects": metas, "currentProject": a.gcfg.CurrentProject}
}

// SwitchProject 切换当前项目（等价 GUI 热切换；idOrName 按 §6.3 解析）
func (s *ctlService) SwitchProject(idOrName string) (any, error) {
	a := s.app
	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(idOrName)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := a.SwitchProject(id); err != nil {
		return nil, err
	}
	return a.GetCurrentProject()
}

// CreateProject 新建项目并切换；from 非空=从该项目（id|名称）复制规则+域名组
func (s *ctlService) CreateProject(name, from string) (any, error) {
	a := s.app
	fromID := ""
	if strings.TrimSpace(from) != "" {
		a.projMu.Lock()
		id, err := a.resolveProjectIDLocked(from)
		a.projMu.Unlock()
		if err != nil {
			return nil, err
		}
		fromID = id
	}
	return a.CreateProject(name, fromID)
}

func (s *ctlService) RenameProject(idOrName, name string) (any, error) {
	a := s.app
	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(idOrName)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := a.RenameProject(id, name); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": id}, nil
}

func (s *ctlService) DeleteProject(idOrName string) error {
	a := s.app
	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(idOrName)
	a.projMu.Unlock()
	if err != nil {
		return err
	}
	return a.DeleteProject(id)
}

// ---------- 规则（目标项目：project 空=当前项目；非当前项目只改文件不热切换） ----------

func (s *ctlService) ListRules(project string) (any, error) {
	a := s.app
	a.projMu.Lock()
	defer a.projMu.Unlock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		return nil, err
	}
	var fg []rules.FilterGroup
	var dr []rules.DecryptRule
	if id == a.proj.ID {
		fg = append([]rules.FilterGroup(nil), a.proj.FilterGroups...)
		dr = append([]rules.DecryptRule(nil), a.proj.DecryptRules...)
	} else {
		pc, err := settings.LoadProjectConfig(a.cfgDir, id, "")
		if err != nil {
			return nil, fmt.Errorf("项目配置读取失败: %w", err)
		}
		fg, dr = pc.FilterGroups, pc.DecryptRules
	}
	return map[string]any{
		"project":      id,
		"filterGroups": fg,
		"decryptRules": dr,
		"quickIgnore": map[string]any{
			"hostGroup":    QuickIgnoreHostGroupID,
			"processGroup": QuickIgnoreProcGroupID,
		},
	}, nil
}

// mutateTargetProjectRules 对目标项目做规则写（设计 §6.3）：
// 当前项目 = 内存改 + 落盘 + 引擎热重建；非当前项目 = 读盘 → 改 → 编译预检 → 落盘
// （非法规则被编译校验拒绝、不落盘；不改变 currentProject、不触发热切换）。
func (s *ctlService) mutateTargetProjectRules(project string, fn func(pc *settings.ProjectConfig) error) error {
	a := s.app
	a.projMu.Lock()
	defer a.projMu.Unlock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		return err
	}
	if id == a.proj.ID {
		if err := fn(a.proj); err != nil {
			return err
		}
		if err := settings.SaveProjectConfig(a.cfgDir, a.proj); err != nil {
			return err
		}
		return a.rebuildEngine()
	}
	pc, err := settings.LoadProjectConfig(a.cfgDir, id, "")
	if err != nil {
		return fmt.Errorf("项目配置读取失败: %w", err)
	}
	if err := fn(pc); err != nil {
		return err
	}
	// 编译预检（设计 §九：非当前项目写入非法规则被编译校验拒绝、不落盘）
	var gmap map[string][]string
	if gs, gerr := domains.LoadUser(settings.ProjectDomainsDir(a.cfgDir, id)); gerr == nil {
		gmap = gs.Domains
	}
	if _, err := rules.NewEngine(pc.FilterGroups, pc.DecryptRules, gmap); err != nil {
		return fmt.Errorf("项目规则编译失败，未保存: %w", err)
	}
	return settings.SaveProjectConfig(a.cfgDir, pc)
}

func (s *ctlService) RuleIgnore(project, target, value string) (bool, error) {
	var added bool
	err := s.mutateTargetProjectRules(project, func(pc *settings.ProjectConfig) error {
		var err error
		added, err = addQuickIgnoreTo(pc, target, value)
		return err
	})
	return added, err
}

func (s *ctlService) RuleGroupSetEnabled(project, id string, enabled bool) error {
	return s.mutateTargetProjectRules(project, func(pc *settings.ProjectConfig) error {
		gi := -1
		for i := range pc.FilterGroups {
			if pc.FilterGroups[i].ID == id {
				gi = i
				break
			}
		}
		if gi < 0 {
			return fmt.Errorf("过滤规则组 %q 不存在", id)
		}
		pc.FilterGroups[gi].Enabled = enabled
		return nil
	})
}

func (s *ctlService) RuleDecrypt(project, action, host string) error {
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
	return s.mutateTargetProjectRules(project, func(pc *settings.ProjectConfig) error {
		pc.DecryptRules = append(pc.DecryptRules, rules.DecryptRule{Action: act, Host: host})
		return nil
	})
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

// GetSettings 合并视图（设计 §6.1）；project 非空时规则字段取指定项目（只读上下文不变）。
func (s *ctlService) GetSettings(project string) (any, error) {
	a := s.app
	a.projMu.Lock()
	defer a.projMu.Unlock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		return nil, err
	}
	if id == a.proj.ID {
		return a.settingsViewLocked(a.proj.FilterGroups, a.proj.DecryptRules, id), nil
	}
	pc, err := settings.LoadProjectConfig(a.cfgDir, id, "")
	if err != nil {
		return nil, fmt.Errorf("项目配置读取失败: %w", err)
	}
	return a.settingsViewLocked(pc.FilterGroups, pc.DecryptRules, id), nil
}

// SaveSettings 与 GUI 同路径（设计 §6.3）：环境字段写全局、规则字段写目标项目；
// 入参 currentProject/projects 一律忽略（切换走 project switch）。
// 目标为非当前项目时：规则半边先校验落盘（不热切换），环境半边借当前项目标准路径保存。
func (s *ctlService) SaveSettings(project string, raw json.RawMessage) ([]string, error) {
	a := s.app
	var nu SettingsView
	if err := json.Unmarshal(raw, &nu); err != nil {
		return nil, fmt.Errorf("配置 JSON 解析失败: %w", err)
	}

	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(project)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	if id == "" || id == currentProjectID(a) {
		// 当前项目：标准保存路径（含 rulesProject 并发令牌校验与热应用）
		res, err := a.SaveSettings(&nu)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		return res.Warnings, nil
	}

	// ---- 目标为非当前项目 ----
	// 1) 规则半边：校验 + 写目标项目 project.json（不触发热切换）
	var warns []string
	a.projMu.Lock()
	if a.proj.ID == id {
		// 解析后目标项目被切换为当前：走内存 + 热重建保持一致
		var gmap map[string][]string
		if a.groups != nil {
			gmap = a.groups.Domains
		}
		verr, w := settings.ValidateRules(nu.FilterGroups, nu.DecryptRules, gmap)
		if verr != nil {
			a.projMu.Unlock()
			return nil, verr
		}
		warns = w
		fillGroupIDs(nu.FilterGroups)
		a.proj.FilterGroups = nu.FilterGroups
		a.proj.DecryptRules = nu.DecryptRules
		if err := settings.SaveProjectConfig(a.cfgDir, a.proj); err != nil {
			a.projMu.Unlock()
			return nil, fmt.Errorf("保存项目配置: %w", err)
		}
		if err := a.rebuildEngine(); err != nil {
			a.projMu.Unlock()
			return nil, err
		}
	} else {
		var gmap map[string][]string
		if gs, gerr := domains.LoadUser(settings.ProjectDomainsDir(a.cfgDir, id)); gerr == nil {
			gmap = gs.Domains
		}
		verr, w := settings.ValidateRules(nu.FilterGroups, nu.DecryptRules, gmap)
		if verr != nil {
			a.projMu.Unlock()
			return nil, verr
		}
		warns = w
		fillGroupIDs(nu.FilterGroups)
		pc, err := settings.LoadProjectConfig(a.cfgDir, id, "")
		if err != nil {
			a.projMu.Unlock()
			return nil, fmt.Errorf("项目配置读取失败: %w", err)
		}
		pc.FilterGroups = nu.FilterGroups
		pc.DecryptRules = nu.DecryptRules
		if err := settings.SaveProjectConfig(a.cfgDir, pc); err != nil {
			a.projMu.Unlock()
			return nil, fmt.Errorf("保存项目配置: %w", err)
		}
	}
	a.projMu.Unlock()

	// 2) 环境半边：借当前项目标准路径（规则字段取当前项目现状，仅环境字段生效）
	env := a.GetSettings()
	env.ListenAddr = nu.ListenAddr
	env.UpstreamMode = nu.UpstreamMode
	env.UpstreamProxy = nu.UpstreamProxy
	env.MaxFlows = nu.MaxFlows
	env.MaxBodyMB = nu.MaxBodyMB
	env.ShowSysProxySwitch = nu.ShowSysProxySwitch
	env.AutoSysProxy = nu.AutoSysProxy
	env.BypassList = nu.BypassList
	env.Persist = nu.Persist
	env.ADB = nu.ADB
	res, err := a.SaveSettings(env)
	if err != nil {
		return nil, err
	}
	if res != nil {
		warns = append(warns, res.Warnings...)
	}
	return warns, nil
}

// currentProjectID 当前项目 id（自取 projMu；供锁外比较）
func currentProjectID(a *App) string {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	return a.proj.ID
}

// fillGroupIDs 补全新组 ID（时间戳毫秒+序号；与 SaveSettings 同规则，规则设计 §4.2）
func fillGroupIDs(fg []rules.FilterGroup) {
	seq := 0
	for i := range fg {
		if fg[i].ID == "" {
			seq++
			fg[i].ID = fmt.Sprintf("%d-%d", time.Now().UnixMilli(), seq)
		}
	}
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

// ---------- 代理生命周期 / CA（M10 补面） ----------

func (s *ctlService) StartProxy() error { return s.app.StartProxy("") }
func (s *ctlService) StopProxy() error   { return s.app.StopProxy() }
func (s *ctlService) InstallCA() error   { return s.app.InstallRootCA() }

// ---------- ADB 设备代理（M10 补面） ----------

// AdbDevices 返回已配置的 ADB 设备清单（名称/path/serial/autoSet），供 CLI 发现 path/serial。
func (s *ctlService) AdbDevices() any {
	a := s.app
	a.projMu.Lock()
	defer a.projMu.Unlock()
	return map[string]any{
		"deviceProxyHost": a.gcfg.ADB.DeviceProxyHost,
		"devices":         a.gcfg.ADB.Configs,
	}
}

// resolveAdbPathSerial 未显式传 adbPath 时回退到已配置设备：单配置取它，
// 多配置时要求显式 serial 消歧（否则报错提示）。
func (a *App) resolveAdbPathSerial(adbPath, serial string) (string, string, error) {
	if strings.TrimSpace(adbPath) != "" {
		return adbPath, serial, nil
	}
	a.projMu.Lock()
	defer a.projMu.Unlock()
	var cfgs []settings.ADBDevice
	for _, c := range a.gcfg.ADB.Configs {
		if strings.TrimSpace(c.Path) != "" {
			cfgs = append(cfgs, c)
		}
	}
	if len(cfgs) == 0 {
		return "", "", fmt.Errorf("未配置 adb 路径：请先在设置中配置，或用 --adb <adb 路径> 指定")
	}
	if strings.TrimSpace(serial) != "" {
		for _, c := range cfgs {
			if c.Serial == serial {
				return c.Path, serial, nil
			}
		}
		return "", "", fmt.Errorf("已配置设备中找不到序列号 %q", serial)
	}
	if len(cfgs) == 1 {
		return cfgs[0].Path, cfgs[0].Serial, nil
	}
	return "", "", fmt.Errorf("配置了 %d 台设备，请用 --serial 指定设备序列号（可用：adb devices 命令查看）", len(cfgs))
}

func (s *ctlService) AdbTest(adbPath string) (string, error) {
	p, _, err := s.app.resolveAdbPathSerial(adbPath, "")
	if err != nil {
		return "", err
	}
	return s.app.AdbTest(p)
}

func (s *ctlService) AdbSetProxy(adbPath, serial string) (string, error) {
	p, sr, err := s.app.resolveAdbPathSerial(adbPath, serial)
	if err != nil {
		return "", err
	}
	// deviceHost 传空：由 App.AdbSetProxy 按全局配置 DeviceProxyHost → 默认值回退
	return s.app.AdbSetProxy(p, sr, "")
}

func (s *ctlService) AdbClearProxy(adbPath, serial string) (string, error) {
	p, sr, err := s.app.resolveAdbPathSerial(adbPath, serial)
	if err != nil {
		return "", err
	}
	return s.app.AdbClearProxy(p, sr)
}

// ---------- 域名组（M10 补面；project 空=当前项目，非当前项目只写文件不热切换） ----------

func (s *ctlService) ListDomainGroups(project string) (any, error) {
	a := s.app
	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(project)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	if id == currentProjectID(a) {
		return a.ListDomainGroupDetails(), nil
	}
	// 非当前项目：直接读其 domains 目录构建清单
	dir := settings.ProjectDomainsDir(a.cfgDir, id)
	g, gerr := domains.LoadUser(dir)
	if gerr != nil {
		return nil, fmt.Errorf("项目域名组读取失败: %w", gerr)
	}
	out := make([]map[string]any, 0, len(g.Domains))
	for gid, list := range g.Domains {
		name := gid
		if t := g.Titles[gid]; t != "" {
			name = t
		}
		out = append(out, map[string]any{"id": gid, "name": name, "count": len(list), "custom": true})
	}
	return map[string]any{"project": id, "groups": out}, nil
}

func (s *ctlService) GetDomainGroup(project, gid string) (any, error) {
	a := s.app
	if err := domains.ValidateID(gid); err != nil {
		return nil, err
	}
	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(project)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	data, rerr := os.ReadFile(filepath.Join(settings.ProjectDomainsDir(a.cfgDir, id), gid+".txt"))
	if rerr != nil {
		return nil, fmt.Errorf("域名组 %q 不存在或读取失败: %w", gid, rerr)
	}
	return map[string]any{"project": id, "id": gid, "text": string(data)}, nil
}

// writeDomainGroupLocked 把内容落盘到指定项目 domains 目录（调用方须持 projMu）。
func (a *App) writeDomainGroupLocked(projectID, gid string, content []byte) (int, error) {
	if err := domains.ValidateID(gid); err != nil {
		return 0, err
	}
	dir := settings.ProjectDomainsDir(a.cfgDir, projectID)
	n, err := domains.WriteUser(dir, gid, content)
	if err != nil {
		return 0, err
	}
	if projectID == a.proj.ID {
		if err := a.reloadGroups(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (s *ctlService) SaveDomainGroup(project, gid, content string) (any, error) {
	a := s.app
	a.projMu.Lock()
	defer a.projMu.Unlock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		return nil, err
	}
	n, err := a.writeDomainGroupLocked(id, gid, []byte(content))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "project": id, "id": gid, "count": n}, nil
}

func (s *ctlService) DeleteDomainGroup(project, gid string) error {
	a := s.app
	if err := domains.ValidateID(gid); err != nil {
		return err
	}
	a.projMu.Lock()
	defer a.projMu.Unlock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		return err
	}
	dir := settings.ProjectDomainsDir(a.cfgDir, id)
	if err := domains.DeleteUser(dir, gid); err != nil {
		return err
	}
	if id == a.proj.ID {
		return a.reloadGroups()
	}
	return nil
}

// ImportDomainGroup 从本地文件路径或 http(s) URL 导入域名组（source），落盘到目标项目。
func (s *ctlService) ImportDomainGroup(project, gid, source string) (any, error) {
	a := s.app
	data, err := fetchSourceBytes(source)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(gid) == "" {
		// id 空：URL/路径取文件名（去扩展名），与 GUI 同口径
		base := source
		if i := strings.IndexAny(base, "?#"); i >= 0 {
			base = base[:i]
		}
		base = filepath.Base(base)
		if dot := strings.LastIndex(base, "."); dot > 0 {
			base = base[:dot]
		}
		gid = base
	}
	a.projMu.Lock()
	defer a.projMu.Unlock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		return nil, err
	}
	n, err := a.writeDomainGroupLocked(id, gid, data)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "project": id, "id": strings.ToLower(strings.TrimSpace(gid)), "count": n}, nil
}

// fetchSourceBytes 从本地文件或 http(s) URL 读取内容（复用 app 的 httpGet，限 4MB/20s）。
func fetchSourceBytes(source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return httpGet(source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("读取文件: %w", err)
	}
	return data, nil
}

// ---------- 规则导入导出（M10 补面） ----------

// ExportRules 返回规则 JSON 原文（rulesFile 同构）；非当前项目读其 project.json + domains 构建。
func (s *ctlService) ExportRules(project string, embedGroups bool) (json.RawMessage, error) {
	a := s.app
	a.projMu.Lock()
	id, err := a.resolveProjectIDLocked(project)
	if err != nil {
		a.projMu.Unlock()
		return nil, err
	}
	var fg []rules.FilterGroup
	var dr []rules.DecryptRule
	var gmap map[string][]string
	bp := append([]string(nil), a.gcfg.BypassList...)
	if id == a.proj.ID {
		fg = append([]rules.FilterGroup(nil), a.proj.FilterGroups...)
		dr = append([]rules.DecryptRule(nil), a.proj.DecryptRules...)
		if a.groups != nil {
			gmap = a.groups.Domains
		}
	} else {
		pc, perr := settings.LoadProjectConfig(a.cfgDir, id, "")
		if perr != nil {
			a.projMu.Unlock()
			return nil, fmt.Errorf("项目配置读取失败: %w", perr)
		}
		fg, dr = pc.FilterGroups, pc.DecryptRules
		if gs, gerr := domains.LoadUser(settings.ProjectDomainsDir(a.cfgDir, id)); gerr == nil {
			gmap = gs.Domains
		}
	}
	a.projMu.Unlock()

	doc := rulesFile{
		Version:      1,
		ExportedAt:   time.Now().Format(time.RFC3339),
		FilterGroups: fg,
		DecryptRules: dr,
		BypassList:   bp,
	}
	if embedGroups {
		doc.Groups = map[string][]string{}
		for _, g := range fg {
			for _, h := range g.Hosts {
				if !strings.HasPrefix(h, "@") {
					continue
				}
				name := strings.TrimPrefix(h, "@")
				if _, ok := doc.Groups[name]; ok {
					continue
				}
				if list, ok := gmap[name]; ok {
					doc.Groups[name] = append([]string(nil), list...)
				}
			}
		}
		if len(doc.Groups) == 0 {
			doc.Groups = nil
		}
	}
	return json.MarshalIndent(doc, "", "  ")
}

// ImportRules 从本地路径/URL 导入规则：当前项目走 App.ImportRules（热更新）；
// 非当前项目读盘 → 补建内嵌域名组 → 编译预检 → 落盘（不热切换）。
func (s *ctlService) ImportRules(project, src string) ([]string, error) {
	a := s.app
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("CLI 导入需指定来源（本地文件路径或 http(s) URL）")
	}
	id, err := currentOrResolved(a, project)
	if err != nil {
		return nil, err
	}
	if id == currentProjectID(a) {
		res, err := a.ImportRules(src)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, fmt.Errorf("导入失败（空结果）")
		}
		return res.Warnings, nil
	}

	// ---- 非当前项目：加载文档并落盘到该项目 ----
	data, err := fetchSourceBytes(src)
	if err != nil {
		return nil, err
	}
	var doc rulesFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("不是有效的规则 JSON: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("不支持的规则文件版本: %d（当前支持版本 1）", doc.Version)
	}
	if doc.FilterGroups == nil {
		doc.FilterGroups = []rules.FilterGroup{}
	}
	if doc.DecryptRules == nil {
		doc.DecryptRules = []rules.DecryptRule{}
	}
	var warns []string
	if doc.BypassList != nil {
		warns = append(warns, "系统代理绕过列表为全局环境设置，导入时已忽略（请在「设置-常规」中修改）")
	}

	a.projMu.Lock()
	defer a.projMu.Unlock()
	dir := settings.ProjectDomainsDir(a.cfgDir, id)
	for gid, list := range doc.Groups {
		gid = strings.ToLower(strings.TrimSpace(gid))
		if gid == "" || len(list) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString("# 规则导入内嵌域名组：" + gid + "\n")
		for _, d := range list {
			if d = strings.TrimSpace(d); d != "" {
				b.WriteString(d + "\n")
			}
		}
		if _, werr := domains.WriteUser(dir, gid, []byte(b.String())); werr != nil {
			return nil, fmt.Errorf("补建内嵌域名组 %q: %w", gid, werr)
		}
	}
	gmap := map[string][]string{}
	if gs, gerr := domains.LoadUser(dir); gerr == nil {
		gmap = gs.Domains
	}
	verr, vwarns := settings.ValidateRules(doc.FilterGroups, doc.DecryptRules, gmap)
	if verr != nil {
		return nil, verr
	}
	warns = append(warns, vwarns...)
	pc, err := settings.LoadProjectConfig(a.cfgDir, id, "")
	if err != nil {
		return nil, fmt.Errorf("项目配置读取失败: %w", err)
	}
	pc.FilterGroups = doc.FilterGroups
	pc.DecryptRules = doc.DecryptRules
	if err := settings.SaveProjectConfig(a.cfgDir, pc); err != nil {
		return nil, fmt.Errorf("保存项目配置: %w", err)
	}
	return warns, nil
}

// currentOrResolved 解析目标项目 id（空=当前项目）。
func currentOrResolved(a *App, project string) (string, error) {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	return a.resolveProjectIDLocked(project)
}

// ---------- 流量动作 / 调试（M10 补面） ----------

func (s *ctlService) SetFlowPinned(id string, pinned bool) error {
	return s.app.SetFlowPinned(id, pinned)
}

func (s *ctlService) BuildCurl(id, shell string) (any, error) {
	return s.app.BuildCurl(id, shell)
}

func (s *ctlService) Compose(raw json.RawMessage) (any, error) {
	var req ComposedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("请求体解析失败: %w", err)
	}
	return s.app.SendComposed(&req)
}

func (s *ctlService) ListProcesses() []string {
	return s.app.ListSystemProcesses()
}
