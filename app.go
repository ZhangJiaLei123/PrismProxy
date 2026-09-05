package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/capture"
	"prismproxy/internal/domains"
	"prismproxy/internal/mitm"
	"prismproxy/internal/procs"
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
	Pinned      bool // M5 置顶：固定顶部、不参与淘汰、Clear 保留（会话内不持久化）
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

// ---------- App ----------

type App struct {
	ctx context.Context // wails 运行时（startup 后可用）

	st  *store.Store
	rec *capture.Recorder

	cfg    *settings.Settings
	cfgDir string
	eng    *rules.Holder   // 规则引擎热更新容器（proxy 与 Recorder.Filter 共享）
	groups *domains.Groups // 域名组（用户导入，@组名 引用源）

	mu       sync.Mutex
	srv      *proxy.Server
	ca       *mitm.CA
	addr     string
	noMITM   bool
	startErr string // 启动自动抓包失败原因（GetProxyStatus 暴露给前端，事件竞态兜底）

	// 事件合帧缓冲（~50ms 窗口，方案 §4.4）
	pendMu   sync.Mutex
	pendUp   map[string]FlowMeta // 同 ID new/update 合并，取最新快照
	pendEv   []string
	flushDue bool
}

// NewApp addr 为空时使用持久化配置里的监听地址
func NewApp(addr string, noMITM bool) *App {
	cfgDir := settings.DefaultConfigDir()

	cfg, err := settings.Load(cfgDir)
	if err != nil {
		cfg = settings.Default()
	}
	// 旧 captureRules/processRules → filterGroups 迁移（规则设计 §六）：迁移即落盘一次
	if cfg.Migrate() {
		if serr := cfg.Save(cfgDir); serr != nil {
			log.Printf("迁移配置落盘失败（内存态已迁移）: %v", serr)
		}
	}

	groups, gerr := domains.LoadUser(filepath.Join(cfgDir, "domains"))
	if gerr != nil {
		groups = nil // 域名组缺失不致命：@组名 引用将不匹配
	}

	eng := &rules.Holder{}
	var gmap map[string][]string
	if groups != nil {
		gmap = groups.Domains
	}
	// 编译失败兜底：log warn + 空引擎全放行（规则设计 §5.3）
	if e, err := rules.NewEngine(cfg.FilterGroups, cfg.DecryptRules, gmap); err == nil {
		eng.Set(e)
	} else {
		log.Printf("过滤规则编译失败，当前过滤未生效（全量显示）: %v", err)
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
	// GUI 启动即抓包；失败（如端口占用）记录状态并通知前端弹提示
	if err := a.StartProxy(""); err != nil {
		runtime.LogErrorf(ctx, "auto start proxy: %v", err)
		a.mu.Lock()
		a.startErr = err.Error()
		a.mu.Unlock()
		runtime.EventsEmit(ctx, "proxy:start-error", err.Error())
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
		return fmt.Errorf("监听 %s 失败：%w（端口可能被占用，可在「设置」中更换监听地址/端口）", addr, err)
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
	a.startErr = "" // 任何一次成功启动都清除此前的启动失败标记（含手动重启）
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
	return ProxyStatus{Running: a.srv != nil, Addr: a.addr, Mode: mode, FlowCount: len(a.st.List()), StartError: a.startErr}
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

// ---------- M5：cURL / 置顶 / 快捷忽略 / 规则导入导出 / 原文复制（方案 §4.7-4.9） ----------

// BuildCurl 生成可直接执行的 cURL 命令：shell = cmd | powershell | bash（转义规则见 capture.BuildCurl）
func (a *App) BuildCurl(id, shell string) (*capture.CurlResult, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return nil, fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	return capture.BuildCurl(f, shell)
}

// SetFlowPinned 置顶/取消置顶；置顶流固定顶部、不参与淘汰、Clear 保留（方案 §4.4）
func (a *App) SetFlowPinned(id string, pinned bool) error {
	_, err := a.st.SetPinned(id, pinned)
	return err
}

// 快捷忽略内置黑名单组（规则设计 §八）：域名与进程拆为两个独立组——
// 引擎组内维度为 AND，若混放同一组会令"忽略域名 X"与"忽略进程 Y"互相收窄
// （仅当 host=X 且 proc=Y 才过滤）；拆成两组后走组间 OR，任一命中即过滤。
const (
	QuickIgnoreHostGroupID = "_quick_ignore_hosts" // 快捷忽略-域名
	QuickIgnoreProcGroupID = "_quick_ignore_procs" // 快捷忽略-进程
)

// AddQuickIgnore 一键忽略：target = host（裸域名=自身+全部子域）| process（进程名，精确不区分大小写）。
// 分别写入内置黑名单组 _quick_ignore_hosts / _quick_ignore_procs（不存在则自动创建），幂等去重；热更新 + 落盘。
// 返回 added=false 表示已存在未重复添加。
func (a *App) AddQuickIgnore(target, value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, fmt.Errorf("忽略内容为空")
	}

	var groupID, groupName string
	var normalize func(string) (string, bool)
	var exists func(rules.FilterGroup, string) bool
	switch target {
	case "host":
		groupID, groupName = QuickIgnoreHostGroupID, "快捷忽略-域名"
		normalize = func(v string) (string, bool) {
			h := normalizeQuickIgnoreHost(v)
			return h, h != ""
		}
		exists = func(g rules.FilterGroup, v string) bool {
			for _, x := range g.Hosts {
				if x == v {
					return true
				}
			}
			return false
		}
	case "process":
		groupID, groupName = QuickIgnoreProcGroupID, "快捷忽略-进程"
		normalize = func(v string) (string, bool) { return v, true }
		exists = func(g rules.FilterGroup, v string) bool {
			for _, x := range g.Processes {
				if strings.EqualFold(x, v) {
					return true
				}
			}
			return false
		}
	default:
		return false, fmt.Errorf("target 须为 host|process")
	}

	nv, ok := normalize(value)
	if !ok {
		return false, fmt.Errorf("域名无效：%q", value)
	}

	a.mu.Lock()
	gi := -1
	for i := range a.cfg.FilterGroups {
		if a.cfg.FilterGroups[i].ID == groupID {
			gi = i
			break
		}
	}
	if gi < 0 {
		a.cfg.FilterGroups = append(a.cfg.FilterGroups, rules.FilterGroup{
			ID: groupID, Name: groupName, Enabled: true, Mode: rules.ModeBlacklist,
		})
		gi = len(a.cfg.FilterGroups) - 1
	}
	g := &a.cfg.FilterGroups[gi]
	if exists(*g, nv) {
		a.mu.Unlock()
		return false, nil // 幂等：已存在
	}
	if target == "host" {
		g.Hosts = append(g.Hosts, nv)
	} else {
		g.Processes = append(g.Processes, nv)
	}
	cfg := a.cfg
	a.mu.Unlock()

	if err := cfg.Save(a.cfgDir); err != nil {
		return false, err
	}
	if err := a.rebuildEngine(); err != nil {
		return false, err
	}
	return true, nil
}

// normalizeQuickIgnoreHost 忽略域名归一化：去端口（ServerAddr 可能带 :port）、小写、去尾点、去 *. 前缀
func normalizeQuickIgnoreHost(h string) string {
	h = strings.TrimSpace(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.ToLower(h)
	h = strings.TrimSuffix(h, ".")
	h = strings.TrimPrefix(h, "*.")
	return h
}

// rulesFile 规则导入导出 JSON 结构（方案 §4.7：含 version + 导出时间 + 规则三层 + 绕过列表）
type rulesFile struct {
	Version      int                 `json:"version"` // 当前 1
	ExportedAt   string              `json:"exportedAt,omitempty"`
	FilterGroups []rules.FilterGroup `json:"filterGroups"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`
	BypassList   []string            `json:"bypassList"`
	Groups       map[string][]string `json:"groups,omitempty"` // 内嵌引用域名组清单（可选）
}

// ExportRules 导出规则为 JSON 文件（弹保存对话框）；embedGroups=true 时内嵌规则 @引用到的域名组清单，
// 便于跨机分享。返回保存路径（用户取消返回空串）。
func (a *App) ExportRules(embedGroups bool) (string, error) {
	a.mu.Lock()
	fg := append([]rules.FilterGroup(nil), a.cfg.FilterGroups...)
	dr := append([]rules.DecryptRule(nil), a.cfg.DecryptRules...)
	bp := append([]string(nil), a.cfg.BypassList...)
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	a.mu.Unlock()

	doc := rulesFile{
		Version:      1,
		ExportedAt:   time.Now().Format(time.RFC3339),
		FilterGroups: fg,
		DecryptRules: dr,
		BypassList:   bp,
	}
	if embedGroups {
		doc.Groups = map[string][]string{}
		for _, g := range fg {
			for _, h := range g.Hosts {
				if !strings.HasPrefix(h, "@") {
					continue
				}
				name := strings.TrimPrefix(h, "@")
				if _, ok := doc.Groups[name]; ok {
					continue
				}
				if list, ok := gmap[name]; ok {
					doc.Groups[name] = append([]string(nil), list...)
				}
			}
		}
		if len(doc.Groups) == 0 {
			doc.Groups = nil
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出规则",
		DefaultFilename: "prismproxy-rules.json",
		Filters:         []runtime.FileFilter{{DisplayName: "规则文件 (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return "", err
	}
	if dest == "" {
		return "", nil // 用户取消
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("写入文件: %w", err)
	}
	return dest, nil
}

// ImportRulesResult 规则导入结果：Warnings 为非阻塞提示（缺组等）
type ImportRulesResult struct {
	Warnings []string `json:"warnings"`
}

// ImportRules 从本地文件或 http(s) URL 导入规则（整体替换 + 校验，方案 §4.7）。
// src 为本地路径或 URL；src 为空时弹文件选择对话框。
// 内嵌域名组清单（groups）自动补建为用户域名组；引用缺失组不阻塞、以 warnings 返回。
func (a *App) ImportRules(src string) (*ImportRulesResult, error) {
	var data []byte
	src = strings.TrimSpace(src)
	switch {
	case src == "":
		file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
			Title:   "选择规则文件",
			Filters: []runtime.FileFilter{{DisplayName: "规则文件 (*.json)", Pattern: "*.json"}, {DisplayName: "所有文件 (*.*)", Pattern: "*.*"}},
		})
		if err != nil {
			return nil, err
		}
		if file == "" {
			return nil, nil // 用户取消
		}
		data, err = os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取文件: %w", err)
		}
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		var err error
		data, err = httpGet(src)
		if err != nil {
			return nil, err
		}
	default:
		var err error
		data, err = os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("读取文件: %w", err)
		}
	}

	var doc rulesFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("不是有效的规则 JSON: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("不支持的规则文件版本: %d（当前支持版本 1）", doc.Version)
	}
	// 缺字段兜底为空切片（防 null 覆盖后前端渲染/引擎编译异常）
	if doc.FilterGroups == nil {
		doc.FilterGroups = []rules.FilterGroup{}
	}
	if doc.DecryptRules == nil {
		doc.DecryptRules = []rules.DecryptRule{}
	}

	// 内嵌域名组 → 补建用户域名组（含内嵌清单则引用不再缺失）
	built := 0
	for id, list := range doc.Groups {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || len(list) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString("# 规则导入内嵌域名组：" + id + "\n")
		for _, d := range list {
			d = strings.TrimSpace(d)
			if d != "" {
				b.WriteString(d + "\n")
			}
		}
		if _, err := domains.WriteUser(a.userDomainsDir(), id, []byte(b.String())); err != nil {
			return nil, fmt.Errorf("补建内嵌域名组 %q: %w", id, err)
		}
		built++
	}

	// 整体替换 + 校验（沿用现有非规则字段：监听/上游/存储预算/开关）
	a.mu.Lock()
	nu := *a.cfg
	a.mu.Unlock()
	nu.FilterGroups = doc.FilterGroups
	nu.DecryptRules = doc.DecryptRules
	if doc.BypassList != nil {
		nu.BypassList = doc.BypassList
	}

	var gmap map[string][]string
	if built > 0 {
		g, err := domains.LoadUser(a.userDomainsDir())
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		a.groups = g
		a.mu.Unlock()
		gmap = g.Domains
	} else if a.groups != nil {
		gmap = a.groups.Domains
	}
	err, warns := nu.Validate(gmap)
	if err != nil {
		return nil, err
	}
	if err := nu.Save(a.cfgDir); err != nil {
		return nil, fmt.Errorf("保存配置: %w", err)
	}
	a.mu.Lock()
	needRestart := a.srv != nil && (nu.ListenAddr != a.cfg.ListenAddr ||
		nu.UpstreamMode != a.cfg.UpstreamMode || nu.UpstreamProxy != a.cfg.UpstreamProxy)
	a.cfg = &nu
	a.mu.Unlock()

	a.st.SetLimits(nu.MaxFlows, int64(nu.MaxBodyMB)<<20)
	if err := a.rebuildEngine(); err != nil {
		return nil, err
	}
	if needRestart {
		if err := a.StopProxy(); err != nil {
			return nil, err
		}
		if err := a.StartProxy(""); err != nil {
			return nil, err
		}
	}
	return &ImportRulesResult{Warnings: warns}, nil
}

// GetFlowRawText 生成详情复制文本（方案 §4.9）。
// part = req | resp；kind = headers（起始行+首部原文）| body（解压后文本）| all（完整报文）。
func (a *App) GetFlowRawText(id, part, kind string) (string, error) {
	f, ok := a.st.Get(id)
	if !ok {
		return "", fmt.Errorf("flow %s not found（可能已淘汰）", id)
	}
	var msg *capture.Message
	switch part {
	case "req":
		msg = f.Request
	case "resp":
		msg = f.Response
	default:
		return "", fmt.Errorf("part 须为 req|resp")
	}
	if msg == nil {
		if part == "req" {
			return "", fmt.Errorf("该流无请求（盲透传隧道）")
		}
		return "", fmt.Errorf("响应尚未到达")
	}
	switch kind {
	case "headers":
		return headerBlock(msg, part), nil
	case "body":
		return string(msgBodyDecoded(msg)), nil
	case "all":
		var b strings.Builder
		b.WriteString(headerBlock(msg, part))
		b.WriteString("\r\n")
		b.Write(msgBodyDecoded(msg))
		return b.String(), nil
	default:
		return "", fmt.Errorf("kind 须为 headers|body|all")
	}
}

// headerBlock 起始行 + 首部（按 key 排序输出，CRLF）
func headerBlock(msg *capture.Message, part string) string {
	var b strings.Builder
	b.WriteString(startLine(msg, part))
	b.WriteString("\r\n")
	keys := make([]string, 0, len(msg.Header))
	for k := range msg.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range msg.Header[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

// startLine 请求行 / 状态行（响应未存 reason phrase，以状态码文本兜底）
func startLine(msg *capture.Message, part string) string {
	proto := msg.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	if part == "req" {
		target := msg.URL
		if u, err := url.Parse(msg.URL); err == nil && u != nil {
			target = u.RequestURI()
		}
		method := msg.Method
		if method == "" {
			method = "GET"
		}
		return method + " " + target + " " + proto
	}
	return proto + " " + strconv.Itoa(msg.StatusCode) + " " + http.StatusText(msg.StatusCode)
}

// msgBodyDecoded 展示层解压后的消息体（解压失败回退原始字节）
func msgBodyDecoded(msg *capture.Message) []byte {
	if len(msg.Body) == 0 {
		return nil
	}
	if dec, err := capture.DecodeBody(msg.ContentEncoding, msg.Body); err == nil {
		return dec
	}
	return msg.Body
}

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

// rebuildEngine 按当前配置重编译规则引擎并热替换（Holder 原子替换，持锁仅做快照）
func (a *App) rebuildEngine() error {
	a.mu.Lock()
	fg, dr := a.cfg.FilterGroups, a.cfg.DecryptRules
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	a.mu.Unlock()
	e, err := rules.NewEngine(fg, dr, gmap)
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

// SaveSettingsResult 保存结果：warnings 为非阻塞提示（如 @引用不存在的域名组）
type SaveSettingsResult struct {
	Warnings []string `json:"warnings"`
}

// SaveSettings 校验并持久化配置，随后热应用：规则/存储预算立即生效；
// 监听地址或上游变化且代理运行中时自动重启代理。
// 组 ID 为空由后端补全（时间戳毫秒+序号，规则设计 §4.2）。
func (a *App) SaveSettings(nu *settings.Settings) (*SaveSettingsResult, error) {
	if nu == nil {
		return nil, fmt.Errorf("配置为空")
	}
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	err, warns := nu.Validate(gmap)
	if err != nil {
		return nil, err
	}
	for _, w := range warns {
		log.Printf("settings warning: %s", w)
	}
	// 补全新组 ID（同批多组加循环序号去重）
	seq := 0
	for i := range nu.FilterGroups {
		if nu.FilterGroups[i].ID == "" {
			seq++
			nu.FilterGroups[i].ID = fmt.Sprintf("%d-%d", time.Now().UnixMilli(), seq)
		}
	}
	if err := nu.Save(a.cfgDir); err != nil {
		return nil, fmt.Errorf("保存配置: %w", err)
	}

	a.mu.Lock()
	needRestart := a.srv != nil && (nu.ListenAddr != a.cfg.ListenAddr ||
		nu.UpstreamMode != a.cfg.UpstreamMode || nu.UpstreamProxy != a.cfg.UpstreamProxy)
	a.cfg = nu
	a.mu.Unlock()

	a.st.SetLimits(nu.MaxFlows, int64(nu.MaxBodyMB)<<20)
	if err := a.rebuildEngine(); err != nil {
		return nil, err // 理论上 Validate 已拦截，双保险
	}
	if needRestart {
		if err := a.StopProxy(); err != nil {
			return nil, err
		}
		if err := a.StartProxy(""); err != nil {
			return nil, err
		}
	}
	return &SaveSettingsResult{Warnings: warns}, nil
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
	a.mu.Lock()
	g := a.groups
	a.mu.Unlock()
	if g == nil {
		return map[string]interface{}{"names": []string{}, "meta": []domains.GroupMeta{}, "titles": map[string]string{}}
	}
	titles := g.Titles
	if titles == nil {
		titles = map[string]string{}
	}
	return map[string]interface{}{"names": g.Names(), "meta": g.Meta, "titles": titles}
}

// ListSystemProcesses 返回系统当前运行的全部进程名（小写、去重、排序），
// 供过滤规则「进程」维度下拉选择；枚举失败时静默返回空列表（不影响手输）。
func (a *App) ListSystemProcesses() []string {
	names, err := procs.List()
	if err != nil {
		log.Printf("枚举系统进程失败：%v", err)
		return []string{}
	}
	return names
}

// ---------- 域名组管理（设置面板：导入/导出/删除，即时生效） ----------

// DomainGroupInfo 域名组管理列表项
type DomainGroupInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"` // 显示名：优先 txt 标准头部「# 域名组：…」，回退 index.json，再回退 id
	Category string `json:"category"`
	Count    int    `json:"count"`
	Custom   bool   `json:"custom"` // 用户导入（同 id 覆盖内置组）
}

// displayName 解析域名组展示名：txt 头部标题 > index.json 中文名 > 组 id
func groupDisplayName(id string, titles map[string]string, meta map[string]domains.GroupMeta) string {
	if t := titles[id]; t != "" {
		return t
	}
	if m, ok := meta[id]; ok && m.Name != "" {
		return m.Name
	}
	return id
}

// DomainGroupImportResult 导入结果
type DomainGroupImportResult struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func (a *App) userDomainsDir() string { return filepath.Join(a.cfgDir, "domains") }

// ListDomainGroupDetails 域名组管理列表（含条数与自定义标记，按 id 排序）
func (a *App) ListDomainGroupDetails() []DomainGroupInfo {
	a.mu.Lock()
	g := a.groups
	a.mu.Unlock()
	if g == nil {
		return []DomainGroupInfo{}
	}
	meta := make(map[string]domains.GroupMeta, len(g.Meta))
	for _, m := range g.Meta {
		meta[m.ID] = m
	}
	titles := g.Titles
	out := make([]DomainGroupInfo, 0, len(g.Domains))
	for id, list := range g.Domains {
		info := DomainGroupInfo{ID: id, Name: groupDisplayName(id, titles, meta), Count: len(list), Custom: g.Custom[id]}
		if m, ok := meta[id]; ok {
			info.Category = m.Category
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ImportDomainGroupFile 本地导入：弹文件对话框选 txt，id 为空取文件名（去扩展名）。用户取消返回 nil
func (a *App) ImportDomainGroupFile(id string) (*DomainGroupImportResult, error) {
	file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择域名组文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "域名组文本 (*.txt)", Pattern: "*.txt"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if file == "" {
		return nil, nil // 用户取消
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("读取文件: %w", err)
	}
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	return a.importDomains(id, data)
}

// ImportDomainGroupURL URL 导入：拉取远程 txt（限 4MB），id 为空取 URL 路径文件名
func (a *App) ImportDomainGroupURL(rawurl, id string) (*DomainGroupImportResult, error) {
	u, err := parseHTTPURL(rawurl)
	if err != nil {
		return nil, err
	}
	data, err := httpGet(rawurl)
	if err != nil {
		return nil, err
	}
	if id == "" {
		base := path.Base(u.Path)
		id = strings.TrimSuffix(base, path.Ext(base))
	}
	return a.importDomains(id, data)
}

// ---------- URL 导入：索引（index.json）支持 ----------

// DomainIndexEntry 索引文件中的单个域名组条目
type DomainIndexEntry struct {
	ID       string `json:"id"`
	File     string `json:"file"` // 相对索引 URL 的组文件路径
	Name     string `json:"name"`
	Category string `json:"category"`
}

// URLImportProbe URL 探测结果：kind = txt（直接域名组文件）| index（索引文件）
type URLImportProbe struct {
	Kind    string             `json:"kind"`
	Entries []DomainIndexEntry `json:"entries,omitempty"`
}

// IndexImportResult 索引批量导入单项结果
type IndexImportResult struct {
	ID      string `json:"id"`
	Count   int    `json:"count"`
	Skipped bool   `json:"skipped,omitempty"` // 本地已存在同名组且选择跳过
	Err     string `json:"err,omitempty"`
}

// domainIndex index.json 结构（仅取导入所需字段）
type domainIndex struct {
	Groups []DomainIndexEntry `json:"groups"`
}

// parseHTTPURL 校验 URL 仅支持 http/https
func parseHTTPURL(rawurl string) (*url.URL, error) {
	u, err := url.Parse(rawurl)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("URL 非法（仅支持 http/https）")
	}
	return u, nil
}

// httpGet 拉取远程内容（20s 超时，限 4MB）
func httpGet(rawurl string) ([]byte, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(rawurl)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}
	return data, nil
}

// ProbeURLImport 探测 URL 内容：能解析为含 groups 数组的 JSON 视为索引，否则视为直接域名组 txt
func (a *App) ProbeURLImport(rawurl string) (*URLImportProbe, error) {
	if _, err := parseHTTPURL(rawurl); err != nil {
		return nil, err
	}
	data, err := httpGet(rawurl)
	if err != nil {
		return nil, err
	}
	var idx domainIndex
	if err := json.Unmarshal(data, &idx); err == nil && idx.Groups != nil {
		entries := make([]DomainIndexEntry, 0, len(idx.Groups))
		for _, e := range idx.Groups {
			if e.ID == "" || e.File == "" {
				continue
			}
			entries = append(entries, e)
		}
		return &URLImportProbe{Kind: "index", Entries: entries}, nil
	}
	return &URLImportProbe{Kind: "txt"}, nil
}

// ImportDomainGroupsFromIndex 按勾选的 id 从索引 URL 批量下载域名组并导入（各组文件相对索引 URL 解析）。
// overwrite=false 时本地已存在的同名组（config/domains/<id>.txt）跳过不下载、不覆盖。
func (a *App) ImportDomainGroupsFromIndex(rawurl string, ids []string, overwrite bool) ([]IndexImportResult, error) {
	base, err := parseHTTPURL(rawurl)
	if err != nil {
		return nil, err
	}
	data, err := httpGet(rawurl)
	if err != nil {
		return nil, err
	}
	var idx domainIndex
	if err := json.Unmarshal(data, &idx); err != nil || idx.Groups == nil {
		return nil, fmt.Errorf("不是有效的索引文件（index.json）")
	}
	byID := make(map[string]DomainIndexEntry, len(idx.Groups))
	for _, e := range idx.Groups {
		byID[e.ID] = e
	}
	// 去重并得到总数（进度条用）
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	total := len(unique)
	results := make([]IndexImportResult, 0, total)
	for i, id := range unique {
		res := IndexImportResult{ID: id}
		emitImportProgress(a.ctx, i, total, id, false)
		entry, ok := byID[id]
		if !ok {
			res.Err = "索引中不存在该组"
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		ref, err := url.Parse(entry.File)
		if err != nil {
			res.Err = "索引中文件路径非法"
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		if !overwrite {
			if _, err := os.Stat(filepath.Join(a.userDomainsDir(), id+".txt")); err == nil {
				res.Skipped = true
				results = append(results, res)
				emitImportProgress(a.ctx, i+1, total, id, false)
				continue
			}
		}
		txt, err := httpGet(base.ResolveReference(ref).String())
		if err != nil {
			res.Err = err.Error()
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		n, err := domains.WriteUser(a.userDomainsDir(), id, txt)
		if err != nil {
			res.Err = err.Error()
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		res.Count = n
		results = append(results, res)
		emitImportProgress(a.ctx, i+1, total, id, false)
	}
	emitImportProgress(a.ctx, total, total, "", true)
	if err := a.reloadGroups(); err != nil {
		return results, fmt.Errorf("热更新失败: %w", err)
	}
	return results, nil
}

// emitImportProgress 发射索引批量导入进度事件（前端 n-progress 监听 index-import-progress）
func emitImportProgress(ctx context.Context, current, total int, id string, done bool) {
	runtime.EventsEmit(ctx, "index-import-progress", map[string]interface{}{
		"current": current,
		"total":   total,
		"id":      id,
		"done":    done,
	})
}

// importDomains 校验并落盘导入内容，随后重载域名组 + 热更新规则引擎
func (a *App) importDomains(id string, data []byte) (*DomainGroupImportResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	n, err := domains.WriteUser(a.userDomainsDir(), id, data)
	if err != nil {
		return nil, err
	}
	if err := a.reloadGroups(); err != nil {
		return nil, err
	}
	return &DomainGroupImportResult{ID: id, Count: n}, nil
}

// ExportDomainGroup 导出域名组到文件（弹保存对话框）；返回保存路径（用户取消返回空串）
func (a *App) ExportDomainGroup(id string) (string, error) {
	data, err := a.groupRaw(id)
	if err != nil {
		return "", err
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出域名组",
		DefaultFilename: id + ".txt",
		Filters:         []runtime.FileFilter{{DisplayName: "域名组文本 (*.txt)", Pattern: "*.txt"}},
	})
	if err != nil {
		return "", err
	}
	if dest == "" {
		return "", nil // 用户取消
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("写入文件: %w", err)
	}
	return dest, nil
}

// groupRaw 取组原始文本（保留注释原貌）：所有组均为用户导入，读用户目录文件
func (a *App) groupRaw(id string) ([]byte, error) {
	a.mu.Lock()
	g := a.groups
	a.mu.Unlock()
	if g == nil {
		return nil, fmt.Errorf("域名组不可用")
	}
	if _, ok := g.Domains[id]; !ok {
		return nil, fmt.Errorf("域名组 %q 不存在", id)
	}
	return os.ReadFile(filepath.Join(a.userDomainsDir(), id+".txt"))
}

// DeleteDomainGroup 删除自定义域名组（内置组不可删除；覆盖同名内置组的删除后内置组恢复生效）
func (a *App) DeleteDomainGroup(id string) error {
	a.mu.Lock()
	custom := a.groups != nil && a.groups.Custom[id]
	a.mu.Unlock()
	if !custom {
		return fmt.Errorf("内置域名组不可删除")
	}
	if err := domains.DeleteUser(a.userDomainsDir(), id); err != nil {
		return err
	}
	return a.reloadGroups()
}

// GetDomainGroupText 取域名组原始文本（含注释/格式）：自定义组读用户目录，内置组读内嵌资源
func (a *App) GetDomainGroupText(id string) (string, error) {
	data, err := a.groupRaw(id)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveDomainGroupText 保存编辑后的域名组文本（落盘为自定义组，同 id 覆盖内置组），随后热更新
func (a *App) SaveDomainGroupText(id, content string) (*DomainGroupImportResult, error) {
	return a.importDomains(id, []byte(content))
}

// reloadGroups 重载用户导入的域名组并热更新规则引擎
func (a *App) reloadGroups() error {
	g, err := domains.LoadUser(a.userDomainsDir())
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.groups = g
	a.mu.Unlock()
	return a.rebuildEngine()
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

// FindFreePort 在 ip 上从 start 起向上逐个探测，返回首个空闲端口
func (a *App) FindFreePort(ip string, start int) (int, error) {
	if start < 1 || start > 65535 {
		return 0, fmt.Errorf("起始端口非法: %d", start)
	}
	if ip == "" {
		ip = "127.0.0.1"
	}
	for p := start; p <= 65535; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(p)))
		if err != nil {
			continue // 被占用（含自身代理监听），继续向上
		}
		_ = ln.Close()
		return p, nil
	}
	return 0, fmt.Errorf("无空闲端口")
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
		Pinned:     f.Pinned,
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
