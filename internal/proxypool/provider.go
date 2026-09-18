package proxypool

import (
	"context"
	"time"

	"prismproxy/internal/settings"
)

// VendorID 厂商标识（注册表键，设计 §5.3）
type VendorID = string

// FetchResult 统一提取结果（设计 §5.3）
type FetchResult struct {
	Entries      []ProxyEntry
	SurplusTotal int // 套餐余量（厂商支持时；-1=未知）
	FetchedAt    time.Time
}

// AccountInfo 账号信息（余量/自动账密/白名单出口 IP）。
// AuthPassword 为敏感字段：仅供 Manager 补全 entry 账密用，
// 不进任何 DTO/日志（对齐 ProxyEntry.Password 红线）。
type AccountInfo struct {
	SurplusTotal int    // 套餐余量（-1=未知）
	AuthUsername string // order/get-info 自动下发的账密（条目未带 pw 时兜底）
	AuthPassword string // 敏感：不进 DTO/日志
	WhitelistIP  string // 白名单模式下当前绑定的出口 IP（可空）
}

// WhitelistInfo 白名单清单
type WhitelistInfo struct {
	IPs []string
}

// VendorMeta 厂商元信息（设计 §8.2 ListProxyVendors 出参；
// 前端厂商下拉不硬编码清单，由 registry 提供）。
type VendorMeta struct {
	ID               VendorID `json:"id"`
	Name             string   `json:"name"`             // 显示名
	SupportUserPass  bool     `json:"supportUserPass"`  // 是否支持账密鉴权
	SupportWhitelist bool     `json:"supportWhitelist"` // 是否支持白名单鉴权
	SupportRegion    bool     `json:"supportRegion"`    // 是否支持地区筛选
}

// MyIPer 可选能力：查询当前公网出口 IP（白名单辅助，设计 §2 官方校验地址）。
// 用类型断言判定厂商是否支持，不进 Provider 主接口。
type MyIPer interface {
	GetMyIP(ctx context.Context) (string, error)
}

// Provider 厂商能力接口（设计 §5.3）。
// conf 为单条账号配置（settings.ProxyProviderConf，凭据已在 CRUD/装配层
// 合并完整，adapter 直接使用）；n 为期望提取数量。
// 实现方必须遵守「厂商 HTTP client 三铁律」（强制直连/独立超时/日志脱敏）。
type Provider interface {
	Vendor() VendorID
	// Fetch 提取 n 个代理 IP
	Fetch(ctx context.Context, conf settings.ProxyProviderConf, n int) (*FetchResult, error)
	// AccountInfo 查询账号余量/账密/白名单绑定 IP
	AccountInfo(ctx context.Context, conf settings.ProxyProviderConf) (*AccountInfo, error)
	// AddWhitelist 将 ip 加入终端白名单
	AddWhitelist(ctx context.Context, conf settings.ProxyProviderConf, ip string) error
	// DelWhitelist 将 ip 移出终端白名单
	DelWhitelist(ctx context.Context, conf settings.ProxyProviderConf, ip string) error
	// ListWhitelist 查询终端白名单
	ListWhitelist(ctx context.Context, conf settings.ProxyProviderConf) (*WhitelistInfo, error)
}
