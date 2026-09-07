package app

import (
	"net/url"

	"prismproxy/internal/capture"
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
	Source      string // capture | composer（M6 调试重发标记）
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

// ---------- 转换 ----------

func toMeta(f *capture.Flow) FlowMeta {
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
func flowDetail(f *capture.Flow) *FlowDetail {
	d := &FlowDetail{FlowMeta: toMeta(f), ServerAddr: f.ServerAddr, TLS: f.TLS}
	if f.Request != nil {
		d.ReqURL = f.Request.URL
		d.ReqProto = f.Request.Proto
		d.ReqHeader = f.Request.Header
		d.ReqBodySize = len(f.Request.Body)
	}
	if f.Response != nil {
		d.RespProto = f.Response.Proto
		d.RespHeader = f.Response.Header
		d.RespBodySize = len(f.Response.Body)
	}
	if f.Process != nil {
		d.ProcessPath = f.Process.Path
	}
	return d
}
