package app

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"prismproxy/internal/procs"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/sysproxy"
)

// ---------- 设置 / 系统代理 / 系统信息 Bindings（方案 §4.8、项目配置设计 §5.5/§6.1） ----------

// GetSettings 读取合并视图：全局环境字段 + 当前项目规则字段（前端设置页）。
// 无打开项目（M11 欢迎页态）：规则字段为空切片，环境字段照常返回（欢迎页设置可用）。
func (a *App) GetSettings() *SettingsView {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	var fg []rules.FilterGroup
	var dr []rules.DecryptRule
	rulesProject := ""
	if a.proj != nil {
		fg, dr, rulesProject = a.proj.FilterGroups, a.proj.DecryptRules, a.proj.ID
	}
	return a.settingsViewLocked(fg, dr, rulesProject)
}

// settingsViewLocked 构造合并视图（调用方持 projMu）：环境字段取全局 gcfg，
// 规则字段由调用方给定（当前项目内存态，或 ctl --project 指定的非当前项目磁盘态）。
// 规则 slice 做顶层浅拷贝：Wails 在方法返回（锁释放）后才 JSON 序列化，拷贝消除
// 序列化期间与持锁写方（如 AddQuickIgnore 组内 append）的数据竞争窗口（M9 审计）。
func (a *App) settingsViewLocked(fg []rules.FilterGroup, dr []rules.DecryptRule, rulesProject string) *SettingsView {
	g := a.gcfg
	return &SettingsView{
		ListenAddr:         g.ListenAddr,
		UpstreamMode:       g.UpstreamMode,
		UpstreamProxy:      g.UpstreamProxy,
		MaxFlows:           g.MaxFlows,
		MaxBodyMB:          g.MaxBodyMB,
		ShowSysProxySwitch: g.ShowSysProxySwitch,
		AutoSysProxy:       g.AutoSysProxy,
		BypassList:         append([]string(nil), g.BypassList...),
		Persist:            g.Persist,
		ADB:                g.ADB,
		AI:                 g.AI.WithDefaults().AISettings(), // P2-10：无 key 投影，读取侧兜底默认值
		FilterGroups:       append([]rules.FilterGroup(nil), fg...),
		DecryptRules:       append([]rules.DecryptRule(nil), dr...),
		CurrentProject:     a.currentMeta(),
		RulesProject:       rulesProject,
	}
}

// SaveSettingsResult 保存结果：warnings 为非阻塞提示（如 @引用不存在的域名组）
type SaveSettingsResult struct {
	Warnings []string `json:"warnings"`
}

// SaveSettings 校验并拆分持久化（项目配置设计 §5.5）：环境字段 → 全局 settings.json，
// 规则字段 → 当前项目 project.json；随后热应用：规则/存储预算立即生效，监听/上游变化
// 且代理运行中时自动重启代理，bypassList 变化且系统代理接管中时 Reapply 热下发。
// rulesProject 是并发令牌（防 TOCTOU）：与当前项目不一致（面板打开期间项目被外部切换）
// 拒绝保存；入参 currentProject 一律忽略——切换唯一入口是 SwitchProject。
// 组 ID 为空由后端补全（时间戳毫秒+序号，规则设计 §4.2）。
func (a *App) SaveSettings(nu *SettingsView) (*SaveSettingsResult, error) {
	if nu == nil {
		return nil, fmt.Errorf("配置为空")
	}
	// 与切换/项目 CRUD 互斥（§5.4）；切换进行中快速失败
	if !a.projMu.TryLock() {
		return nil, fmt.Errorf("正在切换项目，请稍后重试")
	}
	defer a.projMu.Unlock()

	// M11：无打开项目（欢迎页态）时仅保存环境字段；规则半边无项目可属，跳过。
	noProject := a.proj == nil
	if !noProject && nu.RulesProject != a.proj.ID {
		return nil, fmt.Errorf("项目已切换，请重新打开设置面板后再保存")
	}

	// 环境半边：在候选副本上覆盖后校验（不改内存态）
	g := *a.gcfg
	g.ListenAddr = nu.ListenAddr
	g.UpstreamMode = nu.UpstreamMode
	g.UpstreamProxy = nu.UpstreamProxy
	g.MaxFlows = nu.MaxFlows
	g.MaxBodyMB = nu.MaxBodyMB
	g.ShowSysProxySwitch = nu.ShowSysProxySwitch
	g.AutoSysProxy = nu.AutoSysProxy
	g.BypassList = append([]string(nil), nu.BypassList...)
	g.Persist = nu.Persist
	g.ADB = nu.ADB
	// AI 半边（P2-10，设计稿 §六）：顶层 APIKey 仍不经表单链路（保留已存值）；供应商
	// 条目自带 key、随表单明文往返。整体零值=前端未携带 ai 字段（旧面板整结构回传），
	// 跳过合并防误清（判定式与 WithDefaults 未初始化哨兵一致）。
	if !(nu.AI.Provider == "" && nu.AI.MaxKB == 0) {
		g.AI = settings.AIConfig{
			Enabled:     nu.AI.Enabled,
			Provider:    nu.AI.Provider,
			BaseURL:     nu.AI.BaseURL,
			APIKey:      g.AI.APIKey,
			Model:       nu.AI.Model,
			Temperature: nu.AI.Temperature,
			TimeoutSec:  nu.AI.TimeoutSec,
			MaxFlows:    nu.AI.MaxFlows,
			MaxKB:       nu.AI.MaxKB,
			Redact:      nu.AI.Redact,
			// SaveSettings 不走 AI Validate，清单归一化在此显式完成（trim/去空/去重/截断/Current 唯一化）
			Entries: settings.NormalizeAIEntries(nu.AI.Entries),
		}
		// 顶层四字段=当前生效条目快照（分析链路 runAIChatOnce/aiProbe/WarnNoKey/Validate
		// 只读顶层，零改动）；entries 归一化为空时 no-op，顶层保留存量（旧单顶层形态兼容）
		g.AI.SyncAICurrent()
	}
	if err := g.ValidateEnv(); err != nil {
		return nil, err
	}

	var warns []string
	// 规则半边（仅在有打开项目时）：可编译性校验（warnings 非阻塞）+ 组 ID 补全
	if !noProject {
		var gmap map[string][]string
		if a.groups != nil {
			gmap = a.groups.Domains
		}
		var werr error
		werr, warns = settings.ValidateRules(nu.FilterGroups, nu.DecryptRules, gmap)
		if werr != nil {
			return nil, werr
		}
		for _, w := range warns {
			log.Printf("settings warning: %s", w)
		}
		// 补全新组 ID（同批多组加循环序号去重）
		seq := 0
		for i := range nu.FilterGroups {
			if nu.FilterGroups[i].ID == "" {
				seq++
				nu.FilterGroups[i].ID = fmt.Sprintf("%d-%d", time.Now().UnixMilli(), seq)
			}
		}
	}

	// P2-14：AI 已启用但未配 key（非本地 Ollama）——非阻塞提示，随 warnings 下发。
	// 置于规则半边之后：warns 在 ValidateRules 处被整体重赋值，此追加必须在其后
	if w := g.WarnNoKey(); w != "" {
		warns = append(warns, w)
	}

	// 拆分落盘：环境 → 全局 settings.json
	if err := g.SaveGlobal(a.cfgDir); err != nil {
		return nil, fmt.Errorf("保存全局配置: %w", err)
	}
	var pc *settings.ProjectConfig
	if !noProject {
		// 规则 → 当前项目 project.json
		pc = &settings.ProjectConfig{
			ID:           a.proj.ID,
			Name:         a.proj.Name,
			FilterGroups: nu.FilterGroups,
			DecryptRules: nu.DecryptRules,
		}
		if err := settings.SaveProjectConfig(a.cfgDir, pc); err != nil {
			return nil, fmt.Errorf("保存项目配置: %w", err)
		}
	}

	// 内存应用（projMu 已持有；a.srv 读取走 a.mu，锁序 projMu → a.mu）
	a.mu.Lock()
	needRestart := a.srv != nil && (g.ListenAddr != a.gcfg.ListenAddr ||
		g.UpstreamMode != a.gcfg.UpstreamMode || g.UpstreamProxy != a.gcfg.UpstreamProxy)
	a.mu.Unlock()
	oldADB := a.gcfg.ADB           // 保存旧 ADB 配置用于自动挂钩收敛
	oldBypass := a.gcfg.BypassList // 保存旧 bypass 用于变化检测
	*a.gcfg = g                    // Projects/CurrentProject 随浅拷贝保留
	if pc != nil {
		a.proj.FilterGroups = pc.FilterGroups
		a.proj.DecryptRules = pc.DecryptRules
	}

	a.st.SetLimits(g.MaxFlows, int64(g.MaxBodyMB)<<20)
	if noProject {
		// 无项目态：无规则引擎/项目库可热应用；环境热应用（重启/绕过/ADB）仍照常执行。
	} else {
		// 落盘已成功：后续热应用失败一律降级 warning（配置已持久化、重启后必然生效），
		// 不再返回错误——否则磁盘已写而调用方以为主全部失败，造成磁盘/内存认知不一致（M9 审计）。
		if err := a.rebuildEngine(); err != nil {
			log.Printf("规则引擎热重建失败（配置已保存，重启后生效）: %v", err)
			warns = append(warns, fmt.Sprintf("规则已保存，但热应用失败（重启后生效）: %v", err))
		}
		// M7：持久化开关/参数热应用（开启即加载历史，关闭则停写保留 DB 文件）
		if err := a.applyPersist(g.Persist); err != nil {
			log.Printf("持久化设置热应用失败（配置已保存，重启后生效）: %v", err)
			warns = append(warns, fmt.Sprintf("持久化设置已保存，但热应用失败（重启后生效）: %v", err))
		}
	}
	if needRestart {
		// settings 热应用重启代理：内部方法连调（避免重复推 status），重启完成后推一次。
		// 本方法整程持 projMu：启动走免 projMu 的 startProxySnap 并传入新配置快照、
		// 推送走 publishStatusHoldingProj——否则两处重取 projMu 均自死锁（M10 真机抓包实测踩中）。
		if err := a.stopProxy(); err != nil {
			a.publishStatusHoldingProj()
			warns = append(warns, fmt.Sprintf("监听/上游已保存，但停止旧代理失败: %v", err))
		} else if err := a.startProxySnap(g.ListenAddr, g.UpstreamMode, g.UpstreamProxy); err != nil {
			a.publishStatusHoldingProj()
			warns = append(warns, fmt.Sprintf("监听/上游已保存，但代理重启失败（可在顶栏手动启动）: %v", err))
		} else {
			a.publishStatusHoldingProj()
		}
	}
	// bypassList 变化且系统代理接管中：以备份为基线重建 Override 热下发（§5.2/§5.5；
	// 未接管时 Reapply 内部 no-op，故无需先查状态）
	if !equalStrings(oldBypass, g.BypassList) {
		a.mu.Lock()
		addr := a.addr
		a.mu.Unlock()
		if err := sysproxy.Reapply(addr, g.BypassList, a.backupFile()); err != nil {
			log.Printf("bypassList 热下发失败（已落盘，下次接管时生效）: %v", err)
			warns = append(warns, fmt.Sprintf("系统代理绕过列表已保存，但热下发失败（下次接管时生效）: %v", err))
		}
	}
	// ADB 配置热更新收敛：被删除/取消 AutoSet 的设备补 clear（防代理残留断网）；
	// 代理运行中新开启 AutoSet 的设备补 set（热重启场景由启动挂钩统一处理，见 convergeAdbConfigs）。
	a.convergeAdbConfigs(oldADB, g.ADB, needRestart)
	return &SaveSettingsResult{Warnings: warns}, nil
}

func equalStrings(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// SystemProxyStatus 系统代理状态（DTO）
type SystemProxyStatus struct {
	State    string `json:"state"` // off | on | occupied
	Server   string `json:"server"`
	Override string `json:"override"`
}

// GetSystemProxyStatus 查询系统代理状态（验收 #10）
func (a *App) GetSystemProxyStatus() SystemProxyStatus {
	a.mu.Lock()
	addr := a.addr
	a.mu.Unlock()
	st, c, err := sysproxy.Status(addr)
	if err != nil {
		return SystemProxyStatus{State: sysproxy.StateOff}
	}
	return SystemProxyStatus{State: st, Server: c.Server, Override: c.Override}
}

// SetSystemProxy 一键接管/恢复系统代理（ProxyOverride 合并不覆盖）。
// 接管时代理未运行则先启动（否则系统流量会断）。
func (a *App) SetSystemProxy(enable bool) error {
	a.projMu.Lock()
	bypass := append([]string(nil), a.gcfg.BypassList...)
	a.projMu.Unlock()
	a.mu.Lock()
	addr := a.addr
	running := a.srv != nil
	a.mu.Unlock()

	if enable {
		if !running {
			// 内部连调 startProxy（不单独推 status），整个接管完成后统一推一次
			if err := a.startProxy(""); err != nil {
				a.publishStatus()
				return fmt.Errorf("启动代理失败，未接管系统代理: %w", err)
			}
		}
		err := sysproxy.Enable(addr, bypass, a.backupFile())
		a.publishStatus()
		return err
	}
	err := sysproxy.Disable(addr, a.backupFile())
	a.publishStatus()
	return err
}

// ListSystemProcesses 返回系统当前运行的全部进程名（小写、去重、排序），
// 供过滤规则「进程」维度下拉选择；枚举失败时静默返回空列表（不影响手输）。
func (a *App) ListSystemProcesses() []string {
	names, err := procs.List()
	if err != nil {
		log.Printf("枚举系统进程失败：%v", err)
		return []string{}
	}
	return names
}

// GetLocalAddrs 本机 IPv4 地址候选（绑定地址设置项）
func (a *App) GetLocalAddrs() []string {
	out := []string{"127.0.0.1"}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, ad := range addrs {
			ip, _, err := net.ParseCIDR(ad.String())
			if err != nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}

// FindFreePort 在 ip 上从 start 起向上逐个探测，返回首个空闲端口
func (a *App) FindFreePort(ip string, start int) (int, error) {
	if start < 1 || start > 65535 {
		return 0, fmt.Errorf("起始端口非法: %d", start)
	}
	if ip == "" {
		ip = "127.0.0.1"
	}
	for p := start; p <= 65535; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(p)))
		if err != nil {
			continue // 被占用（含自身代理监听），继续向上
		}
		_ = ln.Close()
		return p, nil
	}
	return 0, fmt.Errorf("无空闲端口")
}
