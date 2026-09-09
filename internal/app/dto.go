package app

import (
	"net/url"

	"prismproxy/internal/capture"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
)

// ---------- DTO（Wails 序列化给前端） ----------

// FlowMeta 列表行：事件增量推送与 ListFlows 全量共用
type FlowMeta struct {
	ID          string
	State       string
	Scheme      string
	Method      string
	Host        string
	Path        string
	URL         string
	Status      int
	DurationMS  int64
	BytesUp     int64
	BytesDown   int64
	ProcessName string
	PID         uint32
	ClientAddr  string
	StartedAt   int64 // unix 毫秒
	Err         string
	Pinned      bool   // M5 置顶：固定顶部、不参与淘汰、Clear 保留（会话内不持久化）
	Source      string // capture | composer（M6 调试重发标记）| history（M7 历史库加载）
	Historical  bool   // M7：是否为从 SQLite 历史库加载的历史流（正文惰性回查 DB）
	Tags        []string // M12：该流所属标签名列表（会话态，由 App tagIndex COW 快照注入，不来自 flows.data）
}

// FilterFields 实现 ctlapi.Filterable：供 SSE Hub 按订阅者 filter 过滤 flows upsert
// （ctlapi 不能 import app，经此接口暴露 host/url/path；口径与 flows list --filter 一致）。
func (m FlowMeta) FilterFields() (host, urlStr, path string) {
	return m.Host, m.URL, m.Path
}

// FlowDetail 详情面板：Meta + 首部 + TLS + 进程全量
type FlowDetail struct {
	FlowMeta
	ReqURL       string
	ReqProto     string
	ReqHeader    map[string][]string
	ReqBodySize  int
	RespProto    string
	RespHeader   map[string][]string
	RespBodySize int
	ProcessPath  string
	ServerAddr   string
	TLS          *capture.TLSInfo
}

// BodyPayload 消息体：Raw 原始字节 + Body 解压后字节（[]byte 经 JSON 编为 base64）
type BodyPayload struct {
	Encoding    string
	ContentType string
	Truncated   bool
	Raw         []byte
	Body        []byte
	DecodeErr   string
}

// ProxyStatus 代理运行状态
type ProxyStatus struct {
	Running    bool
	Addr       string
	Mode       string // MITM | tunnel-only
	FlowCount  int
	StartError string // 启动自动抓包失败原因（如端口占用），空为正常
}

// SettingsView GetSettings/SaveSettings 面向前端的合并 DTO（项目配置设计 §6.1）：
// 全局环境字段 + 当前项目规则字段，前端表单字段名与旧单配置一致。
type SettingsView struct {
	// 环境（全局 settings.json）
	ListenAddr         string `json:"listenAddr"`
	UpstreamMode       string `json:"upstreamMode"`
	UpstreamProxy      string `json:"upstreamProxy"`
	MaxFlows           int    `json:"maxFlows"`
	MaxBodyMB          int    `json:"maxBodyMB"`
	ShowSysProxySwitch bool   `json:"showSysProxySwitch"`
	AutoSysProxy       bool   `json:"autoSysProxy"`
	// BypassList 系统代理 ProxyOverride 绕过列表（本机环境属性，全局唯一，见 §3.2）
	BypassList []string               `json:"bypassList"`
	Persist    settings.PersistConfig `json:"persist"`
	ADB        settings.ADBConfig     `json:"adb"`
	// 规则（当前项目 project.json）
	FilterGroups []rules.FilterGroup `json:"filterGroups"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`
	// CurrentProject 只读项目上下文（SaveSettings 忽略入参该字段）
	CurrentProject settings.ProjectMeta `json:"currentProject"`
	// RulesProject 规则字段所属项目 id（GetSettings 回填=当前项目 id）；
	// SaveSettings 校验其与当前项目一致，不一致拒绝保存（并发令牌，防 TOCTOU，见 §5.5）
	RulesProject string `json:"rulesProject"`
}

// ---------- 转换 ----------

// toMeta Flow → 列表 DTO。M12 起为 App 方法：从 tagIndex COW 快照注入 Tags
// （零锁；未命中索引视为无标签，列表热路径不查 DB，设计 §4.5）。
func (a *App) toMeta(f *capture.Flow) FlowMeta {
	m := FlowMeta{
		ID:         string(f.ID),
		State:      string(f.State),
		Scheme:     f.Scheme,
		Host:       f.ServerAddr,
		BytesUp:    f.BytesUp,
		BytesDown:  f.BytesDown,
		ClientAddr: f.ClientAddr,
		Err:        f.Err,
		Pinned:     f.Pinned,
		Source:     f.Source,
		Historical: f.Source == capture.SourceHistory,
		Tags:       a.tagsOf(string(f.ID)),
	}
	if f.Timing != nil {
		m.StartedAt = f.Timing.Start.UnixMilli()
		m.DurationMS = f.Timing.Duration.Milliseconds()
	}
	if f.Request != nil {
		m.Method = f.Request.Method
		m.URL = f.Request.URL
		if u, err := url.Parse(f.Request.URL); err == nil {
			m.Path = u.Path
			if u.RawQuery != "" {
				m.Path += "?" + u.RawQuery
			}
		}
	}
	if f.Response != nil {
		m.Status = f.Response.StatusCode
	}
	if f.Process != nil {
		m.ProcessName = f.Process.Name
		m.PID = f.Process.PID
	}
	return m
}

// flowDetail Flow → 详情 DTO（GetFlowDetail 与 M6 SendComposed 共用）
func (a *App) flowDetail(f *capture.Flow) *FlowDetail {
	d := &FlowDetail{FlowMeta: a.toMeta(f), ServerAddr: f.ServerAddr, TLS: f.TLS}
	if f.Request != nil {
		d.ReqURL = f.Request.URL
		d.ReqProto = f.Request.Proto
		d.ReqHeader = f.Request.Header
		// 历史流 body 不在内存（M7 惰性回查），用落盘时记录的 BodyLen 显示正文大小
		if n := len(f.Request.Body); n > 0 {
			d.ReqBodySize = n
		} else {
			d.ReqBodySize = f.Request.BodyLen
		}
	}
	if f.Response != nil {
		d.RespProto = f.Response.Proto
		d.RespHeader = f.Response.Header
		if n := len(f.Response.Body); n > 0 {
			d.RespBodySize = n
		} else {
			d.RespBodySize = f.Response.BodyLen
		}
	}
	if f.Process != nil {
		d.ProcessPath = f.Process.Path
	}
	return d
}
