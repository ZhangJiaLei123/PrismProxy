// Package settings 用户配置的加载与持久化（方案 §4.8）。
// 存放于用户数据目录（%APPDATA%/PrismProxy/settings.json）。
package settings

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"prismproxy/internal/rules"
)

const fileName = "settings.json"

// 上游代理模式（方案 §4.5）
const (
	UpstreamDirect = "direct" // 直连（默认）
	UpstreamManual = "manual" // 手动 HTTP 代理
	UpstreamSystem = "system" // 跟随系统代理（跳过自身，防环路）
)

// Settings 全部可配项（端口/绑定/上游代理/三层规则/绕过列表/存储预算）
type Settings struct {
	ListenAddr    string `json:"listenAddr"`    // 监听地址，默认 127.0.0.1:9090
	UpstreamMode  string `json:"upstreamMode"`  // direct | manual | system
	UpstreamProxy string `json:"upstreamProxy"` // manual 模式的 HTTP 代理 host:port
	MaxFlows      int    `json:"maxFlows"`      // 环形缓冲条数，默认 2000
	MaxBodyMB     int    `json:"maxBodyMB"`     // body 字节预算（MB），默认 256；0=不限

	// BypassList 系统代理 ProxyOverride 绕过列表（内置默认，可增删并持久化）。
	// 语义：裸域名匹配自身+全部子域；代理崩溃残留时这些域名仍直连（方案 §4.6）。
	BypassList []string `json:"bypassList"`

	CaptureRules []rules.CaptureRule `json:"captureRules"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`
	ProcessRules []rules.ProcessRule `json:"processRules"`
}

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
		ListenAddr:    "127.0.0.1:9090",
		UpstreamMode:  UpstreamDirect,
		MaxFlows:      2000,
		MaxBodyMB:     256,
		BypassList:    append([]string(nil), BuiltinBypass...),
		CaptureRules:  []rules.CaptureRule{},
		DecryptRules:  []rules.DecryptRule{},
		ProcessRules:  []rules.ProcessRule{},
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

// Validate 保存前校验（监听地址/上游模式/规则可编译）
func (s *Settings) Validate() error {
	if _, _, err := net.SplitHostPort(s.ListenAddr); err != nil {
		return fmt.Errorf("监听地址 %q 非法: %v", s.ListenAddr, err)
	}
	switch s.UpstreamMode {
	case UpstreamDirect:
	case UpstreamManual:
		if s.UpstreamProxy == "" {
			return fmt.Errorf("手动上游代理模式须填写代理地址")
		}
		if _, _, err := net.SplitHostPort(s.UpstreamProxy); err != nil {
			return fmt.Errorf("上游代理地址 %q 非法: %v", s.UpstreamProxy, err)
		}
	case UpstreamSystem:
	default:
		return fmt.Errorf("非法上游模式 %q", s.UpstreamMode)
	}
	if s.MaxFlows <= 0 {
		return fmt.Errorf("MaxFlows 须 > 0")
	}
	if s.MaxBodyMB < 0 {
		return fmt.Errorf("MaxBodyMB 须 >= 0")
	}
	_, err := rules.NewEngine(s.CaptureRules, s.DecryptRules, s.ProcessRules, nil)
	return err
}
