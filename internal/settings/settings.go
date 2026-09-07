// Package settings 用户配置的加载与持久化（方案 §4.8）。
// 存放于 exe 同级 config 目录（便携模式，见 DefaultConfigDir）。
package settings

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"prismproxy/internal/rules"
)

const fileName = "settings.json"

// DefaultConfigDir 返回配置目录：exe 同级的 config 文件夹（便携模式，
// 2026-09-04 起取代 %APPDATA%\PrismProxy）。获取 exe 路径失败时退化为 ./config。
// 新位置尚无配置且旧 %APPDATA% 配置存在时，一次性搬迁（不删旧文件）。
func DefaultConfigDir() string {
	dir := "config"
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Join(filepath.Dir(exe), "config")
	}
	migrateLegacyConfig(dir)
	return dir
}

func migrateLegacyConfig(dir string) {
	if _, err := os.Stat(filepath.Join(dir, fileName)); err == nil {
		return // 新位置已有配置
	}
	uc, err := os.UserConfigDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(uc, "PrismProxy", fileName))
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err == nil {
		log.Printf("已将旧配置从 %%APPDATA%%\\PrismProxy 搬迁到 %s", dir)
	}
}

// 上游代理模式（方案 §4.5）
const (
	UpstreamDirect = "direct" // 直连（默认）
	UpstreamManual = "manual" // 手动 HTTP 代理
	UpstreamSystem = "system" // 跟随系统代理（跳过自身，防环路）
)

// Settings 全部可配项（端口/绑定/上游代理/过滤规则组/解密规则/绕过列表/存储预算）
type Settings struct {
	ListenAddr    string `json:"listenAddr"`    // 监听地址，默认 127.0.0.1:9090
	UpstreamMode  string `json:"upstreamMode"`  // direct | manual | system
	UpstreamProxy string `json:"upstreamProxy"` // manual 模式的 HTTP 代理 host:port
	MaxFlows      int    `json:"maxFlows"`      // 环形缓冲条数，默认 2000
	MaxBodyMB     int    `json:"maxBodyMB"`     // body 字节预算（MB），默认 256；0=不限

	// ShowSysProxySwitch 是否在顶栏显示系统代理快捷开关（默认显示）。
	ShowSysProxySwitch bool `json:"showSysProxySwitch"`

	// AutoSysProxy 启动程序时自动接管系统代理（默认关闭）。
	AutoSysProxy bool `json:"autoSysProxy"`

	// BypassList 系统代理 ProxyOverride 绕过列表（内置默认，可增删并持久化）。
	// 语义：裸域名匹配自身+全部子域；代理崩溃残留时这些域名仍直连（方案 §4.6）。
	BypassList []string `json:"bypassList"`

	FilterGroups []rules.FilterGroup `json:"filterGroups"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`

	// Persist 流量 SQLite 持久化（M7，方案 §4.11）。默认关闭；
	// 开启后流量异步落盘、启动加载最近历史，落盘是旁路不影响转发与内存 store 语义。
	Persist PersistConfig `json:"persist"`

	// ADB 安卓模拟器/真机自动代理配置：多条 adb 路径 + 一键设置/清除设备全局 http_proxy。
	ADB ADBConfig `json:"adb"`

	// 旧字段仅作迁移用途：Migrate 迁移后清空并重写落盘（规则设计 §六）
	CaptureRules []rules.CaptureRule `json:"captureRules,omitempty"`
	ProcessRules []rules.ProcessRule `json:"processRules,omitempty"`
}

// PersistConfig 流量持久化配置（M7，方案 §4.11）
type PersistConfig struct {
	Enabled    bool   `json:"enabled"`    // 是否开启落盘（默认关）
	DBPath     string `json:"dbPath"`     // SQLite 文件路径；空=配置目录下 prism.db（便携模式）
	RetainDays int    `json:"retainDays"` // 保留天数；0=不限天数
	MaxMB      int    `json:"maxMB"`      // DB 体积上限（MB）；0=不限体积
}

// DefaultDBPath 默认数据库文件名（落在 exe 同级 config 目录，便携模式）
const DefaultDBPath = "prism.db"

// ADBConfig ADB 自动代理配置（设置面板「ADB 代理」）。
// DeviceProxyHost 为设备侧访问宿主机 PrismProxy 的 IP：雷电模拟器 NAT 默认 172.16.1.2，
// 端口自动取代理实际监听端口；Configs 为多条 adb 配置（不同模拟器/多开各一条）。
type ADBConfig struct {
	DeviceProxyHost string      `json:"deviceProxyHost"` // 设备侧访问宿主机的 IP，默认 172.16.1.2（雷电 NAT）
	Configs         []ADBDevice `json:"configs"`
}

// ADBDevice 一条 adb 配置：名称 + adb 可执行文件路径；
// AutoSet=true 时，PrismProxy 启动代理自动对该设备设置 http_proxy，停止代理自动清除。
// Serial 为可选设备序列号（adb devices 第一列）：同一 adb server 下有多台设备
// （模拟器多开/真机+模拟器）时必须指定，命令拼 `adb -s <serial> ...`；留空=仅一台设备。
type ADBDevice struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Serial  string `json:"serial,omitempty"`
	AutoSet bool   `json:"autoSet"`
}

// DefaultDeviceProxyHost 设备侧访问宿主机的默认 IP（雷电模拟器 VBox NAT 回环地址）
const DefaultDeviceProxyHost = "172.16.1.2"

// BuiltinBypass 内置绕过列表（方案 §4.6：开发工具自身/常见 AI 与本机服务）
var BuiltinBypass = []string{
	"<-loopback>", "localhost", "127.0.0.1",
	"trae.cn", "trae.ai", "trae.com", "trae.com.cn", "trae.volces.com",
	"zijieapi.com", "volces.com", "volcengine.com", "volcengineapi.com",
	"bytedance.com", "bytedance.net", "byted.org", "byteoversea.com",
	"byteintl.net", "bytepluses.com", "snssdk.com", "doubao.com",
	"byted-static.com", "tiktokcdn.com",
}

// Default 默认配置
func Default() *Settings {
	return &Settings{
		ListenAddr:         "127.0.0.1:9090",
		UpstreamMode:       UpstreamDirect,
		MaxFlows:           2000,
		MaxBodyMB:          256,
		ShowSysProxySwitch: true,
		BypassList:         append([]string(nil), BuiltinBypass...),
		FilterGroups:       []rules.FilterGroup{},
		DecryptRules:       []rules.DecryptRule{},
		Persist: PersistConfig{
			Enabled:    false,
			RetainDays: 7,   // 默认保留 7 天
			MaxMB:      500, // 默认 DB 体积上限 500MB
		},
		ADB: ADBConfig{
			DeviceProxyHost: DefaultDeviceProxyHost,
			Configs:         []ADBDevice{},
		},
	}
}

// Load 从 dir 加载；文件不存在返回 Default；损坏返回错误（调用方降级 Default 并提示）
func Load(dir string) (*Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, err
	}
	s := Default() // 以默认值兜底，兼容旧版缺字段
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", fileName, err)
	}
	return s, nil
}

// Save 持久化到 dir（先写临时文件再改名，避免写一半损坏配置）
func (s *Settings) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, fileName))
}

// Migrate 旧 captureRules/processRules → filterGroups（规则设计 §六）。
// 返回是否发生迁移（调用方据此落盘一次；迁移幂等：旧字段清空后二次调用返回 false）。
// urlRe/method 维度丢弃（path glob + 展示层方法过滤替代）并 log 提示；
// decryptRules 不迁移、不改动。
func (s *Settings) Migrate() bool {
	changed := false
	if len(s.CaptureRules) > 0 || len(s.ProcessRules) > 0 {
		changed = s.migrateLegacyRules()
	}
	if s.splitQuickIgnoreGroup() {
		changed = true
	}
	return changed
}

// splitQuickIgnoreGroup 拆分 M5 早期的混合内置黑名单组 _quick_ignore（hosts+processes 同组，
// 组内 AND 语义会令域名/进程忽略互相收窄）为两个独立组 _quick_ignore_hosts / _quick_ignore_procs
// （组间 OR）。幂等：拆分后旧组不存在，二次调用无操作。
func (s *Settings) splitQuickIgnoreGroup() bool {
	qi := -1
	for i := range s.FilterGroups {
		if s.FilterGroups[i].ID == "_quick_ignore" {
			qi = i
			break
		}
	}
	if qi < 0 {
		return false
	}
	old := s.FilterGroups[qi]
	// 移除旧组
	s.FilterGroups = append(s.FilterGroups[:qi], s.FilterGroups[qi+1:]...)

	ensureGroup := func(id, name string) *rules.FilterGroup {
		for i := range s.FilterGroups {
			if s.FilterGroups[i].ID == id {
				return &s.FilterGroups[i]
			}
		}
		s.FilterGroups = append(s.FilterGroups, rules.FilterGroup{
			ID: id, Name: name, Enabled: true, Mode: rules.ModeBlacklist,
		})
		return &s.FilterGroups[len(s.FilterGroups)-1]
	}
	mergeUniq := func(dst *[]string, src []string, caseInsensitive bool) {
		for _, v := range src {
			exist := false
			for _, x := range *dst {
				if (caseInsensitive && strings.EqualFold(x, v)) || (!caseInsensitive && x == v) {
					exist = true
					break
				}
			}
			if !exist {
				*dst = append(*dst, v)
			}
		}
	}
	if len(old.Hosts) > 0 || len(old.Paths) > 0 {
		g := ensureGroup("_quick_ignore_hosts", "快捷忽略-域名")
		mergeUniq(&g.Hosts, old.Hosts, false)
		mergeUniq(&g.Paths, old.Paths, false)
	}
	if len(old.Processes) > 0 {
		g := ensureGroup("_quick_ignore_procs", "快捷忽略-进程")
		mergeUniq(&g.Processes, old.Processes, true)
	}
	log.Printf("settings migrate: 已将混合组 _quick_ignore 拆分为 _quick_ignore_hosts / _quick_ignore_procs")
	return true
}

// migrateLegacyRules 旧 captureRules/processRules → filterGroups（规则设计 §六）。
// urlRe/method 维度丢弃（path glob + 展示层方法过滤替代）并 log 提示；decryptRules 不迁移、不改动。
func (s *Settings) migrateLegacyRules() bool {
	// 来源 × 模式 四个迁移桶：同 action 旧条目合并进同一组（语义聚合）
	type bucket struct {
		name      string
		id        string
		mode      string
		hosts     []string
		processes []string
	}
	buckets := map[string]*bucket{
		"capture_black": {name: "旧捕获规则-黑名单(迁移)", id: "_migrated_capture_black", mode: rules.ModeBlacklist},
		"capture_white": {name: "旧捕获规则-白名单(迁移)", id: "_migrated_capture_white", mode: rules.ModeWhitelist},
		"process_black": {name: "旧进程规则-黑名单(迁移)", id: "_migrated_process_black", mode: rules.ModeBlacklist},
		"process_white": {name: "旧进程规则-白名单(迁移)", id: "_migrated_process_white", mode: rules.ModeWhitelist},
	}
	appendUniq := func(dst *[]string, v string) {
		for _, x := range *dst {
			if x == v {
				return
			}
		}
		*dst = append(*dst, v)
	}
	for _, r := range s.CaptureRules {
		key := "capture_black"
		if r.Action == rules.ActionInclude {
			key = "capture_white"
		}
		if r.Host != "" {
			appendUniq(&buckets[key].hosts, r.Host)
		}
		if r.URLRe != "" || r.Method != "" {
			log.Printf("settings migrate: 旧捕获规则 %q 的 urlRe/method 维度已丢弃（path glob/展示层过滤替代）", r.Host)
		}
	}
	for _, r := range s.ProcessRules {
		key := "process_black"
		if r.Action == rules.ActionInclude {
			key = "process_white"
		}
		if r.Name != "" {
			appendUniq(&buckets[key].processes, r.Name)
		}
	}
	// 固定顺序追加非空迁移组，保证输出确定性
	for _, key := range []string{"capture_black", "capture_white", "process_black", "process_white"} {
		b := buckets[key]
		if len(b.hosts)+len(b.processes) == 0 {
			continue
		}
		s.FilterGroups = append(s.FilterGroups, rules.FilterGroup{
			ID: b.id, Name: b.name, Enabled: true, Mode: b.mode,
			Hosts: b.hosts, Processes: b.processes,
		})
	}
	s.CaptureRules = nil
	s.ProcessRules = nil
	return true
}

// Validate 保存前校验（规则设计 §4.2）。
// knownGroups 为内置域名组 id 集合（@组名 引用存在性检查，缺失只产生 warning 不阻塞）；
// 返回 (阻塞错误, 非阻塞警告)。
func (s *Settings) Validate(knownGroups map[string][]string) (error, []string) {
	if _, _, err := net.SplitHostPort(s.ListenAddr); err != nil {
		return fmt.Errorf("监听地址 %q 非法: %v", s.ListenAddr, err), nil
	}
	switch s.UpstreamMode {
	case UpstreamDirect:
	case UpstreamManual:
		if s.UpstreamProxy == "" {
			return fmt.Errorf("手动上游代理模式须填写代理地址"), nil
		}
		if _, _, err := net.SplitHostPort(s.UpstreamProxy); err != nil {
			return fmt.Errorf("上游代理地址 %q 非法: %v", s.UpstreamProxy, err), nil
		}
	case UpstreamSystem:
	default:
		return fmt.Errorf("非法上游模式 %q", s.UpstreamMode), nil
	}
	if s.MaxFlows <= 0 {
		return fmt.Errorf("MaxFlows 须 > 0"), nil
	}
	if s.MaxBodyMB < 0 {
		return fmt.Errorf("MaxBodyMB 须 >= 0"), nil
	}
	if s.Persist.RetainDays < 0 {
		return fmt.Errorf("persist.retainDays 须 >= 0"), nil
	}
	if s.Persist.MaxMB < 0 {
		return fmt.Errorf("persist.maxMB 须 >= 0"), nil
	}
	// 规则可编译性（mode/组名/host 条目/glob 由 NewEngine 统一把关）
	if _, err := rules.NewEngine(s.FilterGroups, s.DecryptRules, knownGroups); err != nil {
		return err, nil
	}
	var warns []string
	seen := map[string]bool{}
	for _, g := range s.FilterGroups {
		for _, h := range g.Hosts {
			if !strings.HasPrefix(h, "@") {
				continue
			}
			if _, ok := knownGroups[strings.TrimPrefix(h, "@")]; !ok && !seen[h] {
				seen[h] = true
				warns = append(warns, fmt.Sprintf("规则组 %q 引用了不存在的域名组 %q（该引用永不命中）", g.Name, h))
			}
		}
	}
	return nil, warns
}
