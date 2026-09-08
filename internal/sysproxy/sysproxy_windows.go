//go:build windows

// Package sysproxy Windows 系统代理开关（方案 §4.6）：
// 注册表接管/恢复 + InternetSetOption 广播 + ProxyOverride 合并 + 崩溃自愈。
package sysproxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// regPath Internet Settings 完整路径（方案 §4.6 简写 HKCU\Internet Settings 的实体）
const regPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// InternetSetOption 选项常量
const (
	optRefresh         = 37 // INTERNET_OPTION_REFRESH
	optSettingsChanged = 39 // INTERNET_OPTION_SETTINGS_CHANGED
)

var procInternetSetOption = syscall.NewLazyDLL("wininet.dll").NewProc("InternetSetOptionW")

// Config 系统代理当前配置（注册表三项 + AutoConfigURL）
type Config struct {
	Enable        bool   `json:"enable"`
	Server        string `json:"server"`
	Override      string `json:"override"`
	AutoConfigURL string `json:"autoConfigURL"`

	// 各值原本是否存在（恢复时须删回而非写空串，忠实还原）
	ServerExists   bool `json:"serverExists"`
	OverrideExists bool `json:"overrideExists"`
	AutoURLExists  bool `json:"autoURLExists"`
}

// 系统代理状态
const (
	StateOff      = "off"      // 未启用
	StateOn       = "on"       // 指向本工具
	StateOccupied = "occupied" // 指向其他代理（Clash 等）
)

// Current 读注册表当前配置（缺失值按零值 + Exists=false）
func Current() (Config, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.QUERY_VALUE)
	if err != nil {
		return Config{}, fmt.Errorf("打开注册表: %w", err)
	}
	defer k.Close()

	var c Config
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		c.Enable = v != 0
	}
	if s, _, err := k.GetStringValue("ProxyServer"); err == nil {
		c.Server, c.ServerExists = s, true
	}
	if s, _, err := k.GetStringValue("ProxyOverride"); err == nil {
		c.Override, c.OverrideExists = s, true
	}
	if s, _, err := k.GetStringValue("AutoConfigURL"); err == nil {
		c.AutoConfigURL, c.AutoURLExists = s, true
	}
	return c, nil
}

// Status 判定系统代理状态：off | on(指向本工具) | occupied(其他代理)
func Status(selfAddr string) (string, Config, error) {
	c, err := Current()
	if err != nil {
		return StateOff, c, err
	}
	if !c.Enable {
		return StateOff, c, nil
	}
	if pointsToSelf(c.Server, selfAddr) {
		return StateOn, c, nil
	}
	return StateOccupied, c, nil
}

// pointsToSelf 系统代理 ProxyServer 是否指向 selfAddr（兼容 host:port 与 协议=host:port 多段写法）
func pointsToSelf(server, selfAddr string) bool {
	_, selfPort, err := net.SplitHostPort(selfAddr)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(server, ";") {
		seg = strings.TrimSpace(seg)
		if i := strings.Index(seg, "="); i >= 0 {
			seg = seg[i+1:]
		}
		if _, p, err := net.SplitHostPort(seg); err == nil && p == selfPort {
			return true
		}
	}
	return false
}

// UpstreamFromSystem 取当前系统代理作为上游（方案 §4.5 system 模式）：
// 未启用/无可用料/仅指向自身时返回空（降级直连，防环路）
func UpstreamFromSystem(selfAddr string) string {
	c, err := Current()
	if err != nil || !c.Enable {
		return ""
	}
	selfHost, selfPort, _ := net.SplitHostPort(selfAddr)
	for _, seg := range strings.Split(c.Server, ";") {
		seg = strings.TrimSpace(seg)
		proto := ""
		if i := strings.Index(seg, "="); i >= 0 {
			proto, seg = strings.ToLower(seg[:i]), seg[i+1:]
		}
		if proto != "" && proto != "http" && proto != "https" {
			continue // socks= 等本工具不支持，跳过
		}
		h, p, err := net.SplitHostPort(seg)
		if err != nil {
			continue
		}
		self := p == selfPort && (h == selfHost || h == "127.0.0.1" || h == "localhost" || h == "::1")
		if !self {
			return seg
		}
	}
	return ""
}

// Enable 接管系统代理：备份原值 → 合并 ProxyOverride（绝不覆盖）→ 写注册表 → 广播。
// 重复接管不覆盖旧备份（保留最初原值）。
func Enable(selfAddr string, bypass []string, backupFile string) error {
	cur, err := Current()
	if err != nil {
		return err
	}
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		if err := writeBackup(backupFile, cur); err != nil {
			return fmt.Errorf("备份原代理配置: %w", err)
		}
	}
	merged := MergeOverride(cur.Override, bypass)
	if err := write(Config{
		Enable: true, Server: selfAddr, Override: merged,
		ServerExists: true, OverrideExists: true,
		AutoConfigURL: cur.AutoConfigURL, AutoURLExists: cur.AutoURLExists,
	}); err != nil {
		return err
	}
	broadcast()
	return nil
}

// Reapply 以备份原始 Override 为基线重合并 bypassList 并写回注册表（项目配置设计 §5.2）。
// 触发点仅为 SaveSettings 修改全局 bypassList 且系统代理接管中（项目切换不触发）。
// 直接重调 Enable（合并当前注册表 Override）会把已删除的旧 bypass 条目永久残留，
// 故以 sysproxy-backup.json 的原值为基线重建；不重建备份（仍供 Disable/崩溃自愈还原）、
// 不广播"代理关闭"（避免窗口期断流）。备份缺失（异常态）降级为合并当前值并 log 告警。
func Reapply(selfAddr string, bypass []string, backupFile string) error {
	cur, err := Current()
	if err != nil {
		return err
	}
	if !cur.Enable || !pointsToSelf(cur.Server, selfAddr) {
		return nil // 非本工具接管，不动用户配置
	}
	base := cur.Override
	bak, berr := readBackup(backupFile)
	if berr != nil {
		log.Printf("sysproxy Reapply: 读取备份失败(%v)，降级为合并当前 Override", berr)
	} else {
		base = bak.Override
	}
	merged := MergeOverride(base, bypass)
	if err := write(Config{
		Enable: true, Server: cur.Server, Override: merged,
		ServerExists: true, OverrideExists: true,
		AutoConfigURL: cur.AutoConfigURL, AutoURLExists: cur.AutoURLExists,
	}); err != nil {
		return err
	}
	broadcast()
	return nil
}

// Disable 按备份恢复原值并删除备份；无备份且当前指向本工具时仅关 ProxyEnable
func Disable(selfAddr, backupFile string) error {
	bak, err := readBackup(backupFile)
	if err != nil {
		cur, cerr := Current()
		if cerr != nil || !cur.Enable || !pointsToSelf(cur.Server, selfAddr) {
			return nil // 非本工具接管，不动用户配置
		}
		if werr := writeEnable(false); werr != nil {
			return werr
		}
		broadcast()
		return nil
	}
	if err := write(bak); err != nil {
		return fmt.Errorf("恢复原代理配置: %w", err)
	}
	_ = os.Remove(backupFile)
	broadcast()
	return nil
}

// SelfHeal 崩溃自愈：启动时备份文件存在 = 上次接管期间进程被强杀 → 按备份还原。
// 返回是否发生了自愈。
func SelfHeal(backupFile string) (bool, error) {
	bak, err := readBackup(backupFile)
	if err != nil {
		return false, nil // 无备份：上次是干净退出
	}
	if err := write(bak); err != nil {
		return true, fmt.Errorf("崩溃自愈恢复失败: %w", err)
	}
	_ = os.Remove(backupFile)
	broadcast()
	return true, nil
}

// ---------- 内部 ----------

func write(c Config) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表(写): %w", err)
	}
	defer k.Close()

	en := uint32(0)
	if c.Enable {
		en = 1
	}
	if err := k.SetDWordValue("ProxyEnable", en); err != nil {
		return err
	}
	if err := setOrDel(k, "ProxyServer", c.Server, c.ServerExists); err != nil {
		return err
	}
	if err := setOrDel(k, "ProxyOverride", c.Override, c.OverrideExists); err != nil {
		return err
	}
	return setOrDel(k, "AutoConfigURL", c.AutoConfigURL, c.AutoURLExists)
}

func setOrDel(k registry.Key, name, val string, exists bool) error {
	if exists {
		return k.SetStringValue(name, val)
	}
	err := k.DeleteValue(name)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func writeEnable(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	en := uint32(0)
	if on {
		en = 1
	}
	return k.SetDWordValue("ProxyEnable", en)
}

func broadcast() {
	procInternetSetOption.Call(0, optSettingsChanged, 0, 0)
	procInternetSetOption.Call(0, optRefresh, 0, 0)
}

func writeBackup(path string, c Config) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readBackup(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}
