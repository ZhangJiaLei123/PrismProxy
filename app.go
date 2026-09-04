package main

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/capture"
	"prismproxy/internal/domains"
	"prismproxy/internal/mitm"
	"prismproxy/internal/proxy"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/store"
	"prismproxy/internal/sysproxy"
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
	Running   bool
	Addr      string
	Mode      string // MITM | tunnel-only
	FlowCount int
}

// ---------- App ----------

type App struct {
	ctx context.Context // wails 运行时（startup 后可用）

	st  *store.Store
	rec *capture.Recorder

	cfg    *settings.Settings
	cfgDir string
	eng    *rules.Holder   // 规则引擎热更新容器（proxy 与 Recorder.Filter 共享）
	groups *domains.Groups // 内置域名组（@组名 引用源）

	mu     sync.Mutex
	srv    *proxy.Server
	ca     *mitm.CA
	addr   string
	noMITM bool

	// 事件合帧缓冲（~50ms 窗口，方案 §4.4）
	pendMu   sync.Mutex
	pendUp   map[string]FlowMeta // 同 ID new/update 合并，取最新快照
	pendEv   []string
	flushDue bool
}

// NewApp addr 为空时使用持久化配置里的监听地址；dfs 为内嵌域名组（go:embed domains）
func NewApp(addr string, noMITM bool, dfs fs.FS) *App {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		cfgDir = "."
	}
	cfgDir = filepath.Join(cfgDir, "PrismProxy")

	cfg, err := settings.Load(cfgDir)
	if err != nil {
		cfg = settings.Default()
	}

	groups, gerr := domains.Load(dfs, "domains")
	if gerr != nil {
		groups = nil // 域名组缺失不致命：@组名 引用将不匹配
	}

	eng := &rules.Holder{}
	var gmap map[string][]string
	if groups != nil {
		gmap = groups.Domains
	}
	if e, err := rules.NewEngine(cfg.CaptureRules, cfg.DecryptRules, cfg.ProcessRules, gmap); err == nil {
		eng.Set(e)
	}

	if addr == "" {
		addr = cfg.ListenAddr
	}
	a := &App{
		cfg:    cfg,
		cfgDir: cfgDir,
		eng:    eng,
		groups: groups,
		addr:   addr,
		noMITM: noMITM,
		pendUp: make(map[string]FlowMeta),
	}
	a.st = store.New(cfg.MaxFlows)
	a.st.SetLimits(cfg.MaxFlows, int64(cfg.MaxBodyMB)<<20)
	a.rec = capture.NewRecorder(a.st)
	proxy.ApplyRulesFilter(a.rec, a.eng) // 捕获/进程规则 exclude → 不记录
	a.st.Subscribe(a.onStoreEvent)
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 崩溃自愈（验收 #13）：上次接管系统代理期间被强杀 → 按备份还原
	if healed, err := sysproxy.SelfHeal(a.backupFile()); err != nil {
		runtime.LogErrorf(ctx, "系统代理自愈失败: %v", err)
	} else if healed {
		runtime.LogWarning(ctx, "检测到上次异常退出，已恢复原系统代理设置")
	}
	// GUI 启动即抓包
	if err := a.StartProxy(""); err != nil {
		runtime.LogErrorf(ctx, "auto start proxy: %v", err)
	}
}

// shutdown 退出清理：若系统代理正指向本工具则按备份恢复（OnShutdown 钩子）
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	addr := a.addr
	a.mu.Unlock()
	if st, _, err := sysproxy.Status(addr); err == nil && st == sysproxy.StateOn {
		if err := sysproxy.Disable(addr, a.backupFile()); err != nil {
			runtime.LogErrorf(ctx, "恢复系统代理失败: %v", err)
		}
	}
	_ = a.StopProxy()
}

func (a *App) backupFile() string { return filepath.Join(a.cfgDir, "sysproxy-backup.json") }

// ---------- 事件总线：store → 50ms 合帧 → 前端 ----------

func (a *App) onStoreEvent(ev store.Event) {
	a.pendMu.Lock()
	switch ev.Type {
	case "new", "update":
		a.pendUp[ev.Flow.ID] = toMeta(ev.Flow) // 值拷贝快照，规避并发读 Flow
	case "evict":
		for _, id := range ev.IDs {
			delete(a.pendUp, id)
		}
		a.pendEv = append(a.pendEv, ev.IDs...)
	}
	if !a.flushDue {
		a.flushDue = true
		time.AfterFunc(50*time.Millisecond, a.flush)
	}
	a.pendMu.Unlock()
}

func (a *App) flush() {
	a.pendMu.Lock()
	ups := make([]FlowMeta, 0, len(a.pendUp))
	for _, m := range a.pendUp {
		ups = append(ups, m)
	}
	evs := a.pendEv
	a.pendUp = make(map[string]FlowMeta)
	a.pendEv = nil
	a.flushDue = false
	a.pendMu.Unlock()

	if a.ctx == nil {
		return // 未 startup：丢弃，前端挂载后用 ListFlows 拉全量
	}
	if len(ups) > 0 {
		runtime.EventsEmit(a.ctx, "flow:upsert", ups)
	}
	if len(evs) > 0 {
		runtime.EventsEmit(a.ctx, "flow:evict", evs)
	}
}

// ---------- Wails Bindings（前端可直接调用） ----------

// StartProxy 启动代理（同步 listen，端口占用立即报错）；addr 为空用配置里的监听地址
func (a *App) StartProxy(addr string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		return fmt.Errorf("proxy already running on %s", a.addr)
	}
	if addr == "" {
		addr = a.cfg.ListenAddr
	}

	var ca *mitm.CA
	if !a.noMITM {
		var err error
		ca, err = mitm.LoadOrCreateCA(filepath.Join(a.cfgDir, "ca"))
		if err != nil {
			return fmt.Errorf("init root ca: %w", err)
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv, err := proxy.NewServerOpts(addr, a.rec, ca, &proxy.Options{
		UpstreamProxy: a.resolveUpstream(addr),
		Engine:        a.eng,
	})
	if err != nil {
		ln.Close()
		return err
	}
	go func() {
		if err := srv.Serve(ln); err != nil && a.ctx != nil {
			runtime.LogErrorf(a.ctx, "proxy exited: %v", err)
		}
	}()

	a.srv, a.ca, a.addr = srv, ca, addr
	return nil
}

func (a *App) StopProxy() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv == nil {
		return nil
	}
	err := a.srv.Close()
	a.srv = nil
	return err
}

func (a *App) GetProxyStatus() ProxyStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode := "MITM"
	if a.ca == nil {
		mode = "tunnel-only"
	}
	return ProxyStatus{Running: a.srv != nil, Addr: a.addr, Mode: mode, FlowCount: len(a.st.List())}
}

func (a *App) ListFlows() []FlowMeta {
	flows := a.st.List()
	out := make([]FlowMeta, 0, len(flows))
	for _, f := range flows {
		out = append(out, toMeta(f))
	}
	return out
}

func (a *App) GetFlowDetail(id string) (*FlowDetail, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return nil, fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
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
	return d, nil
}

// GetFlowBody 取消息体：which = "req" | "resp"；展示层解压（方案 §4.1）
func (a *App) GetFlowBody(id, which string) (*BodyPayload, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return nil, fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	var msg *capture.Message
	switch which {
	case "req":
		msg = f.Request
	case "resp":
		msg = f.Response
	default:
		return nil, fmt.Errorf("which 须为 req|resp")
	}
	if msg == nil {
		return &BodyPayload{}, nil // 响应尚未到达
	}
	p := &BodyPayload{
		Encoding:    msg.ContentEncoding,
		ContentType: msg.Header.Get("Content-Type"),
		Truncated:   msg.BodyTruncated,
		Raw:         msg.Body,
	}
	if len(msg.Body) > 0 {
		dec, err := capture.DecodeBody(msg.ContentEncoding, msg.Body)
		if err != nil {
			p.DecodeErr = err.Error()
		} else {
			p.Body = dec
		}
	}
	return p, nil
}

func (a *App) ClearFlows() { a.st.Clear() }

// ---------- M4：设置 / 系统代理 / 规则 Bindings（方案 §4.8） ----------

// resolveUpstream 按配置解析上游代理地址（空=直连）
func (a *App) resolveUpstream(selfAddr string) string {
	switch a.cfg.UpstreamMode {
	case settings.UpstreamManual:
		return a.cfg.UpstreamProxy
	case settings.UpstreamSystem:
		return sysproxy.UpstreamFromSystem(selfAddr)
	}
	return ""
}

// rebuildEngine 按当前配置重编译规则引擎并热替换（持锁外调用安全：Holder 原子替换）
func (a *App) rebuildEngine() error {
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	e, err := rules.NewEngine(a.cfg.CaptureRules, a.cfg.DecryptRules, a.cfg.ProcessRules, gmap)
	if err != nil {
		return err
	}
	a.eng.Set(e)
	return nil
}

// GetSettings 读取当前配置（前端设置页）
func (a *App) GetSettings() *settings.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// SaveSettings 校验并持久化配置，随后热应用：规则/存储预算立即生效；
// 监听地址或上游变化且代理运行中时自动重启代理
func (a *App) SaveSettings(nu *settings.Settings) error {
	if nu == nil {
		return fmt.Errorf("配置为空")
	}
	if err := nu.Validate(); err != nil {
		return err
	}
	if err := nu.Save(a.cfgDir); err != nil {
		return fmt.Errorf("保存配置: %w", err)
	}

	a.mu.Lock()
	needRestart := a.srv != nil && (nu.ListenAddr != a.cfg.ListenAddr ||
		nu.UpstreamMode != a.cfg.UpstreamMode || nu.UpstreamProxy != a.cfg.UpstreamProxy)
	a.cfg = nu
	a.mu.Unlock()

	a.st.SetLimits(nu.MaxFlows, int64(nu.MaxBodyMB)<<20)
	if err := a.rebuildEngine(); err != nil {
		return err // 理论上 Validate 已拦截，双保险
	}
	if needRestart {
		if err := a.StopProxy(); err != nil {
			return err
		}
		return a.StartProxy("")
	}
	return nil
}

// SystemProxyStatus 系统代理状态（DTO）
type SystemProxyStatus struct {
	State    string `json:"state"` // off | on | occupied
	Server   string `json:"server"`
	Override string `json:"override"`
}

// GetSystemProxyStatus 查询系统代理状态（验收 #10）
func (a *App) GetSystemProxyStatus() SystemProxyStatus {
	a.mu.Lock()
	addr := a.addr
	a.mu.Unlock()
	st, c, err := sysproxy.Status(addr)
	if err != nil {
		return SystemProxyStatus{State: sysproxy.StateOff}
	}
	return SystemProxyStatus{State: st, Server: c.Server, Override: c.Override}
}

// SetSystemProxy 一键接管/恢复系统代理（ProxyOverride 合并不覆盖）。
// 接管时代理未运行则先启动（否则系统流量会断）。
func (a *App) SetSystemProxy(enable bool) error {
	a.mu.Lock()
	addr := a.addr
	running := a.srv != nil
	bypass := append([]string(nil), a.cfg.BypassList...)
	a.mu.Unlock()

	if enable {
		if !running {
			if err := a.StartProxy(""); err != nil {
				return fmt.Errorf("启动代理失败，未接管系统代理: %w", err)
			}
		}
		return sysproxy.Enable(addr, bypass, a.backupFile())
	}
	return sysproxy.Disable(addr, a.backupFile())
}

// AddDecryptBypass 一键排除域名：追加解密 bypass 规则并热生效（应对 SSL Pinning，列表右键入口）
func (a *App) AddDecryptBypass(host string) error {
	if host == "" {
		return fmt.Errorf("host 为空")
	}
	a.mu.Lock()
	a.cfg.DecryptRules = append(a.cfg.DecryptRules, rules.DecryptRule{Action: rules.ActionBypass, Host: host})
	cfg := a.cfg
	a.mu.Unlock()
	if err := cfg.Save(a.cfgDir); err != nil {
		return err
	}
	return a.rebuildEngine()
}

// ListDomainGroups 域名组清单（规则编辑器 @组名 引用候选）
func (a *App) ListDomainGroups() map[string]interface{} {
	if a.groups == nil {
		return map[string]interface{}{"names": []string{}, "meta": []domains.GroupMeta{}}
	}
	return map[string]interface{}{"names": a.groups.Names(), "meta": a.groups.Meta}
}

// GetLocalAddrs 本机 IPv4 地址候选（绑定地址设置项）
func (a *App) GetLocalAddrs() []string {
	out := []string{"127.0.0.1"}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, ad := range addrs {
			ip, _, err := net.ParseCIDR(ad.String())
			if err != nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}

// InstallRootCA 把根证书装入当前用户受信根存储（certutil -user，免管理员）
func (a *App) InstallRootCA() error {
	a.mu.Lock()
	ca := a.ca
	a.mu.Unlock()
	if ca == nil {
		return fmt.Errorf("MITM 未启用，无根证书可安装")
	}
	out, err := exec.Command("certutil", "-user", "-addstore", "Root", ca.CertPEMPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil: %v: %s", err, string(out))
	}
	return nil
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
