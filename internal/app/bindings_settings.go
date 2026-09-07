package app

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"prismproxy/internal/procs"
	"prismproxy/internal/settings"
	"prismproxy/internal/sysproxy"
)

// ---------- 设置 / 系统代理 / 系统信息 Bindings（方案 §4.8） ----------

// GetSettings 读取当前配置（前端设置页）
func (a *App) GetSettings() *settings.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// SaveSettingsResult 保存结果：warnings 为非阻塞提示（如 @引用不存在的域名组）
type SaveSettingsResult struct {
	Warnings []string `json:"warnings"`
}

// SaveSettings 校验并持久化配置，随后热应用：规则/存储预算立即生效；
// 监听地址或上游变化且代理运行中时自动重启代理。
// 组 ID 为空由后端补全（时间戳毫秒+序号，规则设计 §4.2）。
func (a *App) SaveSettings(nu *settings.Settings) (*SaveSettingsResult, error) {
	if nu == nil {
		return nil, fmt.Errorf("配置为空")
	}
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	err, warns := nu.Validate(gmap)
	if err != nil {
		return nil, err
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
	if err := nu.Save(a.cfgDir); err != nil {
		return nil, fmt.Errorf("保存配置: %w", err)
	}

	a.mu.Lock()
	needRestart := a.srv != nil && (nu.ListenAddr != a.cfg.ListenAddr ||
		nu.UpstreamMode != a.cfg.UpstreamMode || nu.UpstreamProxy != a.cfg.UpstreamProxy)
	oldADB := a.cfg.ADB // 保存旧 ADB 配置用于自动挂钩收敛
	a.cfg = nu
	a.mu.Unlock()

	a.st.SetLimits(nu.MaxFlows, int64(nu.MaxBodyMB)<<20)
	if err := a.rebuildEngine(); err != nil {
		return nil, err // 理论上 Validate 已拦截，双保险
	}
	// M7：持久化开关/参数热应用（开启即加载历史，关闭则停写保留 DB 文件）
	if err := a.applyPersist(nu.Persist); err != nil {
		return nil, err
	}
	if needRestart {
		// settings 热应用重启代理：内部方法连调（避免重复推 status），重启完成后推一次
		if err := a.stopProxy(); err != nil {
			a.publishStatus()
			return nil, err
		}
		if err := a.startProxy(""); err != nil {
			a.publishStatus()
			return nil, err
		}
		a.publishStatus()
	}
	// ADB 配置热更新收敛：被删除/取消 AutoSet 的设备补 clear（防代理残留断网）；
	// 代理运行中新开启 AutoSet 的设备补 set（热重启场景由启动挂钩统一处理，见 convergeAdbConfigs）。
	a.convergeAdbConfigs(oldADB, nu.ADB, needRestart)
	return &SaveSettingsResult{Warnings: warns}, nil
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
	a.mu.Lock()
	addr := a.addr
	running := a.srv != nil
	bypass := append([]string(nil), a.cfg.BypassList...)
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
