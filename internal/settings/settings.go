// Package settings 用户配置的加载与持久化（方案 §4.8）。
// 存放于 exe 同级 config 目录（便携模式，见 DefaultConfigDir）。
//
// M9 起配置拆分为两层（项目配置设计 §3/§4）：
//   - 全局配置 GlobalSettings：环境类（监听/上游/预算/开关/bypassList/persist 策略/ADB）
//     + 项目清单与当前指针，存 config/settings.json；
//   - 项目配置 ProjectConfig：规则类（filterGroups/decryptRules），
//     存 config/projects/<id>/project.json（见 project.go）。
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

// GlobalSettings 全局（环境类）配置 + 项目清单与当前指针（项目配置设计 §4.1）。
// 环境字段所有项目共享；规则类字段在 ProjectConfig（project.go）。
type GlobalSettings struct {
	ListenAddr    string `json:"listenAddr"`    // 监听地址，默认 127.0.0.1:9090
	UpstreamMode  string `json:"upstreamMode"`  // direct | manual | system
	UpstreamProxy string `json:"upstreamProxy"` // manual 模式的 HTTP 代理 host:port
	MaxFlows      int    `json:"maxFlows"`      // 环形缓冲条数，默认 2000
	MaxBodyMB     int    `json:"maxBodyMB"`     // body 字节预算（MB），默认 256；0=不限

	// ShowSysProxySwitch 是否在顶栏显示系统代理快捷开关（默认显示）。
	ShowSysProxySwitch bool `json:"showSysProxySwitch"`

	// AutoSysProxy 启动程序时自动接管系统代理（默认关闭）。
	AutoSysProxy bool `json:"autoSysProxy"`

	// BypassList 系统代理 ProxyOverride 绕过列表（本机网络环境属性，全局唯一，
	// 不随项目切换变化；项目配置设计 §3.2）。
	// 语义：裸域名匹配自身+全部子域；代理崩溃残留时这些域名仍直连（方案 §4.6）。
	BypassList []string `json:"bypassList"`

	// Persist 流量 SQLite 持久化策略（M7，方案 §4.11）。默认关闭；
	// 开启后流量异步落盘、启动加载最近历史，落盘是旁路不影响转发与内存 store 语义。
	// M9 起 DB 文件按项目（projects/<id>/prism.db），此处仅 enabled/retainDays/maxMB 生效。
	Persist PersistConfig `json:"persist"`

	// ADB 安卓模拟器/真机自动代理配置：多条 adb 路径 + 一键设置/清除设备全局 http_proxy。
	ADB ADBConfig `json:"adb"`

	// Projects 项目清单（顺序即展示顺序）；CurrentProject 当前项目 id。
	Projects       []ProjectMeta `json:"projects"`
	CurrentProject string        `json:"currentProject"`
}

// PersistConfig 流量持久化配置（M7，方案 §4.11）
type PersistConfig struct {
	Enabled bool `json:"enabled"` // 是否开启落盘（默认关）
	// DBPath 已废弃（M9）：DB 路径固定为 projects/<id>/prism.db。
	// 字段保留仅为兼容旧 JSON 读取（迁移时检测自定义路径告警），加载后清空、保存不写。
	DBPath     string `json:"dbPath,omitempty"`
	RetainDays int    `json:"retainDays"` // 保留天数；0=不限天数
	MaxMB      int    `json:"maxMB"`      // DB 体积上限（MB）；0=不限体积
}

// DefaultDBPath 默认数据库文件名（落在项目目录 projects/<id>/ 下）
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

// DefaultGlobal 默认全局配置（无项目；调用方负责 EnsureDefaultProject）
func DefaultGlobal() *GlobalSettings {
	return &GlobalSettings{
		ListenAddr:         "127.0.0.1:9090",
		UpstreamMode:       UpstreamDirect,
		MaxFlows:           2000,
		MaxBodyMB:          256,
		ShowSysProxySwitch: true,
		BypassList:         append([]string(nil), BuiltinBypass...),
		Persist: PersistConfig{
			Enabled:    false,
			RetainDays: 7,   // 默认保留 7 天
			MaxMB:      500, // 默认 DB 体积上限 500MB
		},
		ADB: ADBConfig{
			DeviceProxyHost: DefaultDeviceProxyHost,
			Configs:         []ADBDevice{},
		},
		Projects: []ProjectMeta{},
	}
}

// LoadGlobal 从 dir 加载全局配置；文件不存在返回 DefaultGlobal；损坏返回错误（调用方降级并提示）。
// 加载后 Persist.DBPath 强制清空（字段已废弃，不再读取、不再写出）。
func LoadGlobal(dir string) (*GlobalSettings, error) {
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultGlobal(), nil
		}
		return nil, err
	}
	g := DefaultGlobal() // 以默认值兜底，兼容旧版缺字段
	if err := json.Unmarshal(data, g); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", fileName, err)
	}
	g.Persist.DBPath = ""
	if g.Projects == nil {
		g.Projects = []ProjectMeta{}
	}
	return g, nil
}

// SaveGlobal 持久化全局配置到 dir（先写临时文件再改名，避免写一半损坏配置）
func (g *GlobalSettings) SaveGlobal(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	g.Persist.DBPath = "" // dbPath 废弃，保存不写
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, fileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, fileName))
}

// ValidateEnv 环境字段校验（项目配置设计 §5.5：SaveSettings 环境半边）。
// 规则可编译性校验在 ValidateRules（项目半边）。
func (g *GlobalSettings) ValidateEnv() error {
	if _, _, err := net.SplitHostPort(g.ListenAddr); err != nil {
		return fmt.Errorf("监听地址 %q 非法: %v", g.ListenAddr, err)
	}
	switch g.UpstreamMode {
	case UpstreamDirect:
	case UpstreamManual:
		if g.UpstreamProxy == "" {
			return fmt.Errorf("手动上游代理模式须填写代理地址")
		}
		if _, _, err := net.SplitHostPort(g.UpstreamProxy); err != nil {
			return fmt.Errorf("上游代理地址 %q 非法: %v", g.UpstreamProxy, err)
		}
	case UpstreamSystem:
	default:
		return fmt.Errorf("非法上游模式 %q", g.UpstreamMode)
	}
	if g.MaxFlows <= 0 {
		return fmt.Errorf("MaxFlows 须 > 0")
	}
	if g.MaxBodyMB < 0 {
		return fmt.Errorf("MaxBodyMB 须 >= 0")
	}
	if g.Persist.RetainDays < 0 {
		return fmt.Errorf("persist.retainDays 须 >= 0")
	}
	if g.Persist.MaxMB < 0 {
		return fmt.Errorf("persist.maxMB 须 >= 0")
	}
	return nil
}

// ValidateRules 规则可编译性校验（规则设计 §4.2）。
// knownGroups 为内置域名组 id 集合（@组名 引用存在性检查，缺失只产生 warning 不阻塞）；
// 返回 (阻塞错误, 非阻塞警告)。
func ValidateRules(filterGroups []rules.FilterGroup, decryptRules []rules.DecryptRule, knownGroups map[string][]string) (error, []string) {
	// 规则可编译性（mode/组名/host 条目/glob 由 NewEngine 统一把关）
	if _, err := rules.NewEngine(filterGroups, decryptRules, knownGroups); err != nil {
		return err, nil
	}
	var warns []string
	seen := map[string]bool{}
	for _, g := range filterGroups {
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
