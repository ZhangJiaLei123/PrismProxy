// Package proxypool 动态代理池运行时（代理池设计 §5.3/§6）。
// 多厂商架构：Provider 能力接口 + registry 注册表（首期 91http）+ Manager
// （提取/续期/轮换/单飞/Resolving 窗口/OnEmpty/坏点归因/Degraded 上报）。
// 依赖方向：proxypool → settings（配置类型）；不反向依赖 internal/proxy
// （Endpoint 与 proxy.UpstreamEndpoint 由 App 装配层薄适配，设计 §7.1）。
package proxypool

import (
	"net"
	"strconv"
	"time"
)

// ProxyEntry 池内一条代理 IP（代理池设计 §5.3）。
// Raw 仅内存排障用：pw=1 时 Raw 含账密明文，序列化输出（PoolStatus/日志/CLI）
// 前必须剔除——一切 DTO 不含 Raw 字段（v3 G3）。
type ProxyEntry struct {
	Host     string    // 必填
	Port     int       // 必填
	Username string    // 账密鉴权时
	Password string    // 敏感：不进任何 DTO/日志
	ExpireAt time.Time // 未知则零值（不做到期续期，仅失败换）
	Region   string    // 归属地（展示用，可空）
	Source   string    // VendorID
	Raw      string    // 原始行（排障，不进 UI/日志明文）
}

// Addr 返回 host:port
func (e ProxyEntry) Addr() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// Endpoint Resolver 出参：当前应使用的上游（代理池设计 §6.2）。
// Resolver 返回 (nil, nil)=直连（OnEmpty=direct 回退）；err 非空=拒绝出站。
// 字段直拷给 proxy.UpstreamEndpoint（App 层一次性薄适配）。
type Endpoint struct {
	Addr     string // host:port
	Username string
	Password string
}
