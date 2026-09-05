package capture

import (
	"net/http"
	"time"
)

// FlowState 流状态机：pending → streaming → done/error
type FlowState string

const (
	StatePending   FlowState = "pending"
	StateStreaming FlowState = "streaming"
	StateDone      FlowState = "done"
	StateError     FlowState = "error"
)

// MaxBodyCapture 单条消息体捕获上限（2MB，超出截断，转发不受影响）
const MaxBodyCapture = 2 << 20

// ProcessInfo 发起连接的本地进程
type ProcessInfo struct {
	PID  uint32
	Name string
	Path string // 权限不足取不到时为空（降级显示）
}

// Timing 计时（M2 起细化为 dns/connect/tls/ttfb 等多段）
type Timing struct {
	Start    time.Time
	Duration time.Duration
}

// Message 请求或响应
type Message struct {
	Method          string
	URL             string
	Proto           string
	Header          http.Header
	StatusCode      int    // 仅响应
	ContentEncoding string // gzip|deflate|br|zstd|""
	Body            []byte // 捕获部分，可能被截断
	BodyTruncated   bool
}

// PeerCert 上游真实证书链中一张证书的摘要（存原始 x509.Certificate 不便序列化）
type PeerCert struct {
	Subject   string
	Issuer    string
	DNSNames  []string
	NotBefore time.Time
	NotAfter  time.Time
}

// TLSInfo MITM 解密后的 TLS 元信息（方案 §4.2：上游真实证书链存此）
type TLSInfo struct {
	ClientVersion string // 客户端<->代理 的 TLS 版本
	ServerVersion string // 代理<->上游 的 TLS 版本
	ServerName    string // 客户端 SNI
	PeerCerts     []PeerCert
}

// Flow 一条网络流（明文 HTTP 事务、CONNECT 隧道或 MITM 后的 HTTPS 事务）
type Flow struct {
	ID         string
	State      FlowState
	Scheme     string // http | https | websocket
	ClientAddr string
	ServerAddr string
	Process    *ProcessInfo
	Request    *Message
	Response   *Message
	TLS        *TLSInfo // 仅 MITM 解密成功的流
	BytesUp    int64
	BytesDown  int64
	Timing     *Timing
	Err        string
	Pinned     bool // M5 置顶：固定顶部展示、不参与环形淘汰、Clear 保留、会话内不持久化（方案 §4.4）
}

func NewFlow(id string) *Flow {
	return &Flow{ID: id, State: StatePending, Timing: &Timing{Start: time.Now()}}
}

// BodyCapture tee 捕获缓冲：写满 max 后丢弃后续字节并标记截断。
// Write 始终返回 len(p)，保证 TeeReader 转发不中断。
type BodyCapture struct {
	buf       []byte
	truncated bool
	max       int
}

func NewBodyCapture(max int) *BodyCapture { return &BodyCapture{max: max} }

func (b *BodyCapture) Write(p []byte) (int, error) {
	remain := b.max - len(b.buf)
	switch {
	case remain >= len(p):
		b.buf = append(b.buf, p...)
	case remain > 0:
		b.buf = append(b.buf, p[:remain]...)
		b.truncated = true
	default:
		b.truncated = true
	}
	return len(p), nil
}

func (b *BodyCapture) Bytes() []byte   { return b.buf }
func (b *BodyCapture) Truncated() bool { return b.truncated }
