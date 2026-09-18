// 动态代理池配置（代理池设计 §5.2/§5.5，v3 定稿）。
// 生效唯一以 GlobalSettings.UpstreamMode=="pool" 为准（无 Enabled 字段，杜绝双源不一致）。
// 敏感字段 Secret/Password：不进 SettingsView 全量 DTO，增改删走独立 CRUD 路径
// （仿 ctl_bridge.go SaveAIConfigPatch 读-改-写-0600），掩码保存场景由 CRUD 合并原值后再校验。
package settings

import (
	"fmt"
	"strings"
)

// 池耗尽/提取失败策略（OnEmpty，代理池设计 §十二决策 1）
const (
	PoolOnEmptyDirect = "direct" // 回退直连（默认；降级期 UI 黄色警示，见 PoolStatus.Degraded）
	PoolOnEmptyError  = "error"  // 拒绝出站（HTTP 转发/CONNECT 回 502，不静默用真实 IP）
)

// 轮换模式（RotationConfig.Mode）
const (
	RotationSticky     = "sticky"     // 粘滞（默认：到期才换/手动换）
	RotationPerRequest = "perRequest" // 每请求轮换（预留，首期 UI 不开放）
)

// 代理鉴权方式（ProxyProviderConf.AuthMode）
const (
	PoolAuthAuto      = "auto"      // 自动（默认：优先账密，缺失则白名单）
	PoolAuthUserPass  = "userpass"  // 用户名+密码
	PoolAuthWhitelist = "whitelist" // 终端 IP 白名单
)

// ProxyPoolSettings 动态代理池总配置（环境类、跨项目）。
// v3：删除 Enabled 字段——是否生效唯一以 GlobalSettings.UpstreamMode=="pool" 为准，
// UI 池子区显隐同样只读 upstreamMode。
type ProxyPoolSettings struct {
	ActiveProvider string              `json:"activeProvider"` // 当前生效的账号配置 id（空=未选择）
	Providers      []ProxyProviderConf `json:"providers"`      // 多厂商/多账号配置清单
	Rotation       RotationConfig      `json:"rotation"`       // 轮换与续期策略
	OnEmpty        string              `json:"onEmpty"`        // 池耗尽/提取失败：direct 直连(默认) | error 拒绝
}

// ProxyProviderConf 一条厂商账号配置（厂商下拉选择；同厂商可多条=多账号）。
// Secret/Password 为敏感字段：本地 settings.json 0600 落盘，掩码投影不回显原值，不写日志。
type ProxyProviderConf struct {
	ID       string `json:"id"`                 // 本地生成唯一 id（app 层 CRUD 生成）
	Name     string `json:"name"`               // 用户可读别名（必填，默认"厂商-尾号"由 app 层补）
	Vendor   string `json:"vendor"`             // 厂商 id：首期 "91http"；存在性由 app 层查 registry 校验
	TradeNo  string `json:"tradeNo"`            // 91http 业务编号（通用字段名按厂商映射）
	Secret   string `json:"secret"`             // 91http 业务密钥（敏感）
	Protocol string `json:"protocol,omitempty"` // "http"（首期仅 http；socks 预留不开放）
	// 提取参数
	FetchNum   int    `json:"fetchNum,omitempty"`   // 单次提取数量，默认 1（粘滞模式 1 即可）
	Province   string `json:"province,omitempty"`   // 省份筛选（可选，首期手填）
	City       string `json:"city,omitempty"`       // 城市筛选（可选，首期手填）
	ISP        string `json:"isp,omitempty"`        // "" / "0" 不筛选 / "1" 电信 / "2" 移动 / "3" 联通
	FilterUsed bool   `json:"filterUsed,omitempty"` // filter=1 过滤今日已提取
	AuthMode   string `json:"authMode,omitempty"`   // whitelist | userpass | auto（默认 auto）
	// 账密可选覆盖：一般由 order/get-info 自动获取；允许手填兜底
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // 敏感
}

// RotationConfig 轮换/续期策略
type RotationConfig struct {
	Mode            string `json:"mode"`            // sticky（默认）| perRequest（预留）
	RefreshAheadSec int    `json:"refreshAheadSec"` // 到期前多少秒提前续提，默认 30
	MinPoolSize     int    `json:"minPoolSize"`     // 池低于该数触发补池，默认 1
	MaxRetry        int    `json:"maxRetry"`        // 提取/建连失败重试次数，默认 2
	ResolveWaitSec  int    `json:"resolveWaitSec"`  // Resolving 窗口 Resolver 等待单飞上限，默认 5，范围 1-15
}

// DefaultProxyPool 默认代理池配置（无账号；策略字段给默认值，对齐 AI/Persist 默认子值范式）
func DefaultProxyPool() ProxyPoolSettings {
	return ProxyPoolSettings{
		Providers: []ProxyProviderConf{},
		Rotation: RotationConfig{
			Mode:            RotationSticky,
			RefreshAheadSec: 30,
			MinPoolSize:     1,
			MaxRetry:        2,
			ResolveWaitSec:  5,
		},
		OnEmpty: PoolOnEmptyDirect,
	}
}

// WithDefaults 零值兜底（代理池设计 §5.2 说明，沿用 AIConfig.WithDefaults 范式）：
// 旧 JSON 无 proxyPool 字段或部分缺省时反序列化出零值，读取侧一律先兜底再使用；
// 仅内存转换不回写落盘。
func (s ProxyPoolSettings) WithDefaults() ProxyPoolSettings {
	if s.Rotation.RefreshAheadSec <= 0 {
		s.Rotation.RefreshAheadSec = 30
	}
	if s.Rotation.MinPoolSize <= 0 {
		s.Rotation.MinPoolSize = 1
	}
	if s.Rotation.MaxRetry <= 0 {
		s.Rotation.MaxRetry = 2
	}
	if s.Rotation.ResolveWaitSec <= 0 {
		s.Rotation.ResolveWaitSec = 5
	}
	if s.Rotation.Mode == "" {
		s.Rotation.Mode = RotationSticky
	}
	if s.OnEmpty == "" {
		s.OnEmpty = PoolOnEmptyDirect
	}
	for i := range s.Providers {
		s.Providers[i] = s.Providers[i].WithDefaults()
	}
	return s
}

// WithDefaults 单条账号零值兜底：FetchNum<=0→1、AuthMode==""→auto
func (c ProxyProviderConf) WithDefaults() ProxyProviderConf {
	if c.FetchNum <= 0 {
		c.FetchNum = 1
	}
	if c.AuthMode == "" {
		c.AuthMode = PoolAuthAuto
	}
	return c
}

// ActiveConf 返回当前生效账号（nil=未选择或指向不存在）
func (s ProxyPoolSettings) ActiveConf() *ProxyProviderConf {
	for i := range s.Providers {
		if s.Providers[i].ID == s.ActiveProvider {
			return &s.Providers[i]
		}
	}
	return nil
}

// Validate 代理池配置校验（代理池设计 §5.5）。调用前须先 WithDefaults 兜底——
// 校验只认非法值，不惩罚缺省。ValidateEnv 的 pool 分支与 CRUD 独立落盘路径共用。
// 厂商存在性（registry 查询）不在此校验：settings 不反向依赖 proxypool，由 app 层把关。
func (s ProxyPoolSettings) Validate() error {
	switch s.OnEmpty {
	case PoolOnEmptyDirect, PoolOnEmptyError:
	default:
		return fmt.Errorf("代理池耗尽策略 onEmpty 须为 direct|error")
	}
	switch s.Rotation.Mode {
	case RotationSticky, RotationPerRequest:
	default:
		return fmt.Errorf("代理池轮换模式须为 sticky|perRequest")
	}
	if s.Rotation.ResolveWaitSec < 1 || s.Rotation.ResolveWaitSec > 15 {
		return fmt.Errorf("代理池 ResolveWaitSec 须在 1-15 秒")
	}
	for i := range s.Providers {
		if err := s.Providers[i].validateShape(); err != nil {
			return err
		}
	}
	if s.ActiveProvider == "" {
		return fmt.Errorf("使用代理池前须先选择生效账号（proxy pool add --use）")
	}
	active := s.ActiveConf()
	if active == nil {
		return fmt.Errorf("代理池生效账号 %q 不存在于账号清单", s.ActiveProvider)
	}
	// 凭据只强制当前生效账号（掩码保存场景由 CRUD 合并原值后再走到这里）；
	// 非生效账号允许是凭据未补全的草稿。
	if strings.TrimSpace(active.TradeNo) == "" || strings.TrimSpace(active.Secret) == "" {
		return fmt.Errorf("代理池生效账号 %q 缺少业务编号/业务密钥", active.Name)
	}
	return nil
}

// validateShape 单条账号形态校验（不含凭据强制；调用前须先 WithDefaults）
func (c ProxyProviderConf) validateShape() error {
	if strings.TrimSpace(c.Vendor) == "" {
		return fmt.Errorf("代理池账号 %q 缺少厂商", c.Name)
	}
	if c.FetchNum < 1 || c.FetchNum > 100 {
		return fmt.Errorf("代理池账号 %q 提取数量须在 1-100", c.Name)
	}
	switch c.AuthMode {
	case PoolAuthAuto, PoolAuthUserPass, PoolAuthWhitelist:
	default:
		return fmt.Errorf("代理池账号 %q 鉴权方式须为 auto|userpass|whitelist", c.Name)
	}
	return nil
}
