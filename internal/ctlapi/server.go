// Package ctlapi 本地控制 API（M8，方案 §4.12）：供 `PrismProxy.exe cli ...` 子命令
// （以及 AI agent / 脚本）以 HTTP 控制运行中的实例。仅绑 127.0.0.1，token 认证；
// 与 Wails Bindings 共用同一套内部服务层（Service 接口由接线层实现）。
package ctlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultAddr 控制 API 默认监听地址（独立于代理端口 9090）
const DefaultAddr = "127.0.0.1:9595"

// UISettingsTabs 合法的设置面板 tab（cli ui settings 校验用）
var UISettingsTabs = []string{"general", "network", "adb", "decrypt", "capture", "domains"}

// Service 控制面服务层：接线层（main 包）实现，ctlapi 不依赖 Wails/具体业务包。
// 返回值以可 JSON 序列化类型为主；error 非空时 HTTP 响应 4xx/5xx。
type Service interface {
	// 状态与流量
	Status() map[string]any                 // 代理状态 + 流计数 + 系统代理状态 + ui/headless 标记
	ListFlows(limit int, filter string) any // 流摘要（不含 body）
	GetFlow(id string) (any, error)
	GetFlowBody(id, which string) (any, error)
	ClearFlows() int // 返回清除条数（置顶保留）
	// 规则（过滤规则组 / 解密规则）；project 空=当前项目，否则按 id|名称解析（设计 §6.3）
	ListRules(project string) (any, error)
	RuleIgnore(project, target, value string) (bool, error) // target=host|process；added=false 表示幂等已存在
	RuleGroupSetEnabled(project, id string, enabled bool) error
	RuleDecrypt(project, action, host string) error // action=mitm|bypass
	// 系统代理 / 设置
	SysProxy(action string) (string, error) // action=on|off|status → 返回 state
	GetSettings(project string) (any, error)
	SaveSettings(project string, raw json.RawMessage) (warnings []string, err error)
	// 项目（M9，设计 §6.3）
	ListProjects() any
	SwitchProject(idOrName string) (any, error)
	CreateProject(name, from string) (any, error) // from 非空=从该项目复制规则+域名组
	RenameProject(idOrName, name string) (any, error)
	DeleteProject(idOrName string) error
	CloseProject() error // M11：关闭当前项目进入无打开项目态（欢迎页）
	// UI 控制（第二步）：仅 GUI 模式真正生效
	UIClear() (cleared int, ui bool)
	UISettings(tab string) (ui bool)
	// 代理生命周期 / CA（M10 补面）
	StartProxy() error
	StopProxy() error
	InstallCA() error
	// ADB 设备代理（M10 补面）
	AdbTest(adbPath string) (string, error)
	AdbSetProxy(adbPath, serial string) (string, error) // deviceHost 由后端按配置/默认补
	AdbClearProxy(adbPath, serial string) (string, error)
	AdbDevices() any                                    // 已配置的 ADB 设备清单（供 CLI 发现 path/serial）
	// 域名组（M10 补面）；project 空=当前项目，非当前项目写文件不热切换
	ListDomainGroups(project string) (any, error)
	GetDomainGroup(project, id string) (any, error) // 返回 {id,text}
	SaveDomainGroup(project, id, content string) (any, error)
	DeleteDomainGroup(project, id string) error
	ImportDomainGroup(project, id, source string) (any, error) // source=http(s) URL 或本地文件路径
	// 规则导入导出（M10 补面）
	ExportRules(project string, embedGroups bool) (json.RawMessage, error) // 返回 rules JSON 原文
	ImportRules(project, src string) (warnings []string, err error)        // src=http(s) URL 或本地文件路径
	// 流量动作 / 调试（M10 补面）
	SetFlowPinned(id string, pinned bool) error
	BuildCurl(id, shell string) (any, error)
	Compose(raw json.RawMessage) (any, error)
	ListProcesses() []string
}

// Server 控制 API HTTP 服务（仅回环）
type Server struct {
	addr     string
	token    string
	endpoint string // 落盘文件（endpoint.json）
	svc      Service

	mu         sync.Mutex
	listener   net.Listener
	httpSrv    *http.Server
	uiOn       bool   // 当前是否有 GUI（影响 ui:* 事件回调可用性）
	hub        *Hub   // SSE 推送总线（Start 时创建）
	instanceID string // 实例标识（启动时间戳毫秒），open 帧携带，重启即变
}

// NewServer 创建控制服务；addr 为空用 DefaultAddr，token 为空则生成新随机 token。
func NewServer(addr, token, endpointFile string, svc Service) *Server {
	if strings.TrimSpace(addr) == "" {
		addr = DefaultAddr
	}
	if token == "" {
		token = newToken()
	}
	return &Server{addr: addr, token: token, endpoint: endpointFile, svc: svc}
}

// Addr 返回实际监听地址（Start 之后有效）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}

// Hub 返回 SSE 推送总线（Start 之后非 nil）。接线层向它 Publish flows/status 事件。
func (s *Server) Hub() *Hub { return s.hub }

// InstanceID 返回实例标识（启动时间戳毫秒），实例重启即变。
func (s *Server) InstanceID() string { return s.instanceID }

// Token 返回当前 token（接线层据此落盘）
func (s *Server) Token() string { return s.token }

// SetUI 标记 GUI 是否在线（startup 后 true，headless 恒 false）。
// UI 控制请求在 GUI 不在线时返回 ui:false 提示而非报错。
func (s *Server) SetUI(on bool) {
	s.mu.Lock()
	s.uiOn = on
	s.mu.Unlock()
}

// Start 同步监听（端口占用立即返回错误，调用方按非致命处理）。
// 监听成功后写 endpoint 文件（地址 + token，ACL 收紧）。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("控制 API 监听 %s 失败: %w", s.addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.hub = NewHub()
	s.instanceID = strconv.FormatInt(time.Now().UnixMilli(), 10)
	s.httpSrv = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	// 注意：不得设置 WriteTimeout/IdleTimeout——会掐断 SSE 长连接（设计 §4.3）
	s.mu.Unlock()

	if err := WriteEndpoint(s.endpoint, Endpoint{Addr: ln.Addr().String(), Token: s.token}); err != nil {
		log.Printf("warn: 写控制 API endpoint 文件失败: %v", err)
	}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("控制 API 退出: %v", err)
		}
	}()
	log.Printf("控制 API 监听 %s（cli 子命令连接地址）", ln.Addr().String())
	return nil
}

// Close 停止服务并删除 endpoint 文件。
// 顺序：先 Hub.Close() 关闭全部 SSE 订阅者（Shutdown 不会主动断开活跃长连接，
// 先关 Hub 让 handler 写完余帧退出、客户端立即收 EOF），再 Shutdown HTTP server。
func (s *Server) Close() {
	s.mu.Lock()
	srv, ln := s.httpSrv, s.listener
	hub := s.hub
	s.httpSrv, s.listener = nil, nil
	s.mu.Unlock()
	if hub != nil {
		hub.Close()
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	RemoveEndpoint(s.endpoint)
}

func (s *Server) hasUI() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uiOn
}

// ---------- 路由 ----------

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", s.auth(s.handleStatus))
	mux.HandleFunc("/api/v1/flows", s.auth(s.handleFlows))
	// /flows/clear 须显式注册：ServeMux 精确模式 /flows 只匹配该路径，
	// /flows/clear 会落到 /flows/ 子树 handler，故不能只靠 handleFlows 内的路径判断
	mux.HandleFunc("/api/v1/flows/clear", s.auth(s.handleFlows))
	mux.HandleFunc("/api/v1/flows/", s.auth(s.handleFlowSub))
	mux.HandleFunc("/api/v1/rules", s.auth(s.handleRules))
	mux.HandleFunc("/api/v1/rules/", s.auth(s.handleRulesSub))
	mux.HandleFunc("/api/v1/sysproxy", s.auth(s.handleSysProxy))
	mux.HandleFunc("/api/v1/settings", s.auth(s.handleSettings))
	mux.HandleFunc("/api/v1/projects", s.auth(s.handleProjects))
	mux.HandleFunc("/api/v1/ui/", s.auth(s.handleUI))
	mux.HandleFunc("/api/v1/proxy", s.auth(s.handleProxy))
	mux.HandleFunc("/api/v1/ca/install", s.auth(s.handleCA))
	mux.HandleFunc("/api/v1/adb", s.auth(s.handleADB))
	mux.HandleFunc("/api/v1/adb/", s.auth(s.handleADB))
	mux.HandleFunc("/api/v1/domains", s.auth(s.handleDomains))
	mux.HandleFunc("/api/v1/domains/", s.auth(s.handleDomains))
	mux.HandleFunc("/api/v1/processes", s.auth(s.handleProcesses))
	mux.HandleFunc("/api/v1/compose", s.auth(s.handleCompose))
	// SSE 推送（query token 仅此端点接受：EventSource 无法自定义请求头）
	mux.HandleFunc("/api/v1/events", s.auth(s.handleEvents, true))
	return mux
}

// auth 校验 Bearer token（常量时间比较防侧信道）。
// allowQuery=true 时（仅 /events）额外接受 ?token=（浏览器 EventSource 兜底，
// 其余端点只认 header，避免 token 随 URL 扩散）。
func (s *Server) auth(next http.HandlerFunc, allowQuery ...bool) http.HandlerFunc {
	want := "Bearer " + s.token
	queryOK := len(allowQuery) > 0 && allowQuery[0]
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got == "" {
			got = "Bearer " + r.Header.Get("X-Prism-Token")
		}
		if got == "Bearer " && queryOK {
			got = "Bearer " + r.URL.Query().Get("token")
		}
		if !secureEqual(got, want) {
			writeErr(w, http.StatusUnauthorized, "未授权：token 缺失或错误")
			return
		}
		next(w, r)
	}
}

// allowedChannels 本期支持的 SSE 频道白名单
var allowedChannels = map[string]bool{"flows": true, "status": true}

// handleEvents SSE 实时推送：GET /api/v1/events?channels=flows,status&filter=<子串>
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	// 频道白名单校验（缺省 flows）
	var channels []string
	if v := r.URL.Query().Get("channels"); v != "" {
		for _, c := range strings.Split(v, ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if !allowedChannels[c] {
				writeErr(w, http.StatusBadRequest, "未知频道: "+c)
				return
			}
			channels = append(channels, c)
		}
	}
	if len(channels) == 0 {
		channels = []string{"flows"}
	}

	unsub, sink, reset, ok := s.hub.Subscribe(channels, r.URL.Query().Get("filter"))
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "订阅者已满")
		return
	}
	defer unsub()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "服务端不支持流式响应")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	writeFrame := func(event string, data any) bool {
		b, err := json.Marshal(data)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// open 帧（携带 instanceId，供消费者检测实例更换）
	if !writeFrame("open", map[string]any{
		"server":     "prismproxy-ctlapi",
		"version":    1,
		"instanceId": s.instanceID,
		"channels":   channels,
		"time":       time.Now().UnixMilli(),
	}) {
		return
	}
	// status seed 帧：订阅含 status 时立即推一帧当前状态，新订阅者无需等下一次变化
	hasStatus := false
	for _, c := range channels {
		if c == "status" {
			hasStatus = true
		}
	}
	if hasStatus {
		if !writeFrame("status", s.svc.Status()) {
			return
		}
	}

	// 15s 心跳（`: ping` 注释行，防中间超时断连）
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reset:
			// 背压溢出：通知客户端丢弃增量状态、重连重拉快照后断开
			_, _ = fmt.Fprint(w, "event: reset\ndata: {\"reason\":\"backpressure\"}\n\n")
			flusher.Flush()
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case frame, ok := <-sink:
			if !ok {
				return // Hub.Close（实例关闭）
			}
			if frame.ID > 0 {
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", frame.ID, frame.Event, frame.Data); err != nil {
					return
				}
			} else if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.Event, frame.Data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Status())
}

func (s *Server) handleFlows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, s.svc.ListFlows(limit, r.URL.Query().Get("filter")))
	case http.MethodPost:
		// POST /flows/clear
		if r.URL.Path != "/api/v1/flows/clear" && r.URL.Path != "/api/v1/flows" {
			writeErr(w, http.StatusNotFound, "未知路径")
			return
		}
		if r.URL.Query().Get("action") != "clear" && r.URL.Path != "/api/v1/flows/clear" {
			writeErr(w, http.StatusBadRequest, "POST /flows 须带 action=clear")
			return
		}
		n := s.svc.ClearFlows()
		writeJSON(w, http.StatusOK, map[string]any{"cleared": n, "pinnedKept": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
	}
}

func (s *Server) handleFlowSub(w http.ResponseWriter, r *http.Request) {
	// /api/v1/flows/{id} 或 /api/v1/flows/{id}/body?which=req|resp
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/flows/")
	parts := strings.Split(rest, "/")
	if parts[0] == "" {
		writeErr(w, http.StatusNotFound, "未知路径")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		// GET /flows/{id}：单流详情（pin 为 POST、走下方 switch，不能在此顶层拦非 GET）
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
			return
		}
		v, err := s.svc.GetFlow(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	switch parts[1] {
	case "body":
		which := r.URL.Query().Get("which")
		if which == "" {
			which = "resp"
		}
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
			return
		}
		v, err := s.svc.GetFlowBody(id, which)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	case "pin":
		// POST /flows/{id}/pin  {pinned:bool}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
			return
		}
		var req struct {
			Pinned bool `json:"pinned"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if err := s.svc.SetFlowPinned(id, req.Pinned); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "pinned": req.Pinned})
		return
	case "curl":
		// GET /flows/{id}/curl?shell=cmd|powershell|bash
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
			return
		}
		shell := r.URL.Query().Get("shell")
		if shell == "" {
			shell = "powershell"
		}
		v, err := s.svc.BuildCurl(id, shell)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	writeErr(w, http.StatusNotFound, "未知路径（可用：/flows/{id}、/flows/{id}/body、/flows/{id}/pin、/flows/{id}/curl）")
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	switch r.Method {
	case http.MethodGet:
		v, err := s.svc.ListRules(project)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPost:
		var req struct {
			Action string `json:"action"` // ignore | decrypt
			Target string `json:"target"` // host | process（ignore）
			Value  string `json:"value"`  // 域名 / 进程名
			Host   string `json:"host"`   // decrypt 用
			Kind   string `json:"kind"`   // mitm | bypass（decrypt 用）
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		switch req.Action {
		case "ignore":
			added, err := s.svc.RuleIgnore(project, req.Target, req.Value)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"added": added})
		case "decrypt":
			if err := s.svc.RuleDecrypt(project, req.Kind, req.Host); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeErr(w, http.StatusBadRequest, "action 须为 ignore|decrypt（组启停请用 /rules/groups/{id}）")
		}
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
	}
}

func (s *Server) handleRulesSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/rules/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	project := r.URL.Query().Get("project")

	// /rules/export?embed=1  → GET，返回规则 JSON 文件原文（供 CLI 重定向保存）
	if len(parts) == 1 && parts[0] == "export" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
			return
		}
		embed := r.URL.Query().Get("embed") != "" && r.URL.Query().Get("embed") != "0" && r.URL.Query().Get("embed") != "false"
		raw, err := s.svc.ExportRules(project, embed)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	// /rules/import  → POST {src: 本地路径|http(s) URL}
	if len(parts) == 1 && parts[0] == "import" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
			return
		}
		var req struct {
			Src string `json:"src"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		warnings, err := s.svc.ImportRules(project, req.Src)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warnings": warnings})
		return
	}

	// /rules/groups/{id}/enabled  {enabled:bool}
	if len(parts) != 3 || parts[0] != "groups" || parts[2] != "enabled" {
		writeErr(w, http.StatusNotFound, "未知路径（可用：/rules/groups/{id}/enabled、/rules/export、/rules/import）")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.svc.RuleGroupSetEnabled(project, parts[1], req.Enabled); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": parts[1], "enabled": req.Enabled})
}

func (s *Server) handleSysProxy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		state, err := s.svc.SysProxy("status")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": state})
	case http.MethodPost:
		var req struct {
			Action string `json:"action"` // on | off
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if req.Action != "on" && req.Action != "off" {
			writeErr(w, http.StatusBadRequest, "action 须为 on|off")
			return
		}
		state, err := s.svc.SysProxy(req.Action)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": state})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	switch r.Method {
	case http.MethodGet:
		v, err := s.svc.GetSettings(project)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPut:
		raw, err := readBody(r, 4<<20)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		warnings, err := s.svc.SaveSettings(project, raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warnings": warnings})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
	}
}

// handleProjects 项目 CRUD（M9，设计 §6.3）：GET 列表；POST action=switch|create|rename|delete
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.svc.ListProjects())
	case http.MethodPost:
		var req struct {
			Action string `json:"action"` // switch | create | rename | delete
			ID     string `json:"id"`     // switch/rename/delete 目标（id|名称）
			Name   string `json:"name"`   // create/rename 名称
			From   string `json:"from"`   // create 复制源（id|名称，可空）
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		switch req.Action {
		case "switch":
			v, err := s.svc.SwitchProject(req.ID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "current": v})
		case "create":
			v, err := s.svc.CreateProject(req.Name, req.From)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project": v})
		case "rename":
			v, err := s.svc.RenameProject(req.ID, req.Name)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, v)
		case "delete":
			if err := s.svc.DeleteProject(req.ID); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		case "close":
			if err := s.svc.CloseProject(); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeErr(w, http.StatusBadRequest, "action 须为 switch|create|rename|delete|close")
		}
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
	}
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/ui/")
	switch rest {
	case "clear":
		cleared, ui := s.svc.UIClear()
		writeJSON(w, http.StatusOK, map[string]any{"cleared": cleared, "ui": ui})
	case "settings":
		var req struct {
			Tab string `json:"tab"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Tab != "" && !contains(UISettingsTabs, req.Tab) {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("tab 须为 %v 之一", UISettingsTabs))
			return
		}
		ui := s.svc.UISettings(req.Tab)
		writeJSON(w, http.StatusOK, map[string]any{"ui": ui, "tab": req.Tab})
	default:
		writeErr(w, http.StatusNotFound, "未知路径（可用：/ui/clear、/ui/settings）")
	}
}

// ---------- 代理生命周期 / CA ----------

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Action string `json:"action"` // start | stop
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		var err error
		switch req.Action {
		case "start":
			err = s.svc.StartProxy()
		case "stop":
			err = s.svc.StopProxy()
		default:
			writeErr(w, http.StatusBadRequest, "action 须为 start|stop")
			return
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.svc.Status())
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
	}
}

func (s *Server) handleCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if err := s.svc.InstallCA(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- ADB 设备代理 ----------

func (s *Server) handleADB(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/adb")
	rest = strings.Trim(rest, "/")
	if r.Method != http.MethodPost {
		// GET /adb → 已配置设备清单（供 CLI 发现 path/serial）
		if r.Method == http.MethodGet && rest == "" {
			writeJSON(w, http.StatusOK, s.svc.AdbDevices())
			return
		}
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		return
	}
	var req struct {
		Action  string `json:"action"` // test | set | clear
		AdbPath string `json:"adbPath"`
		Serial  string `json:"serial"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	// 允许 /adb/<action> 子路径覆盖请求体 action（CLI 走 RESTful 子路径）
	if rest != "" && (rest == "test" || rest == "set" || rest == "clear") {
		req.Action = rest
	}
	var msg string
	var err error
	switch req.Action {
	case "test":
		msg, err = s.svc.AdbTest(req.AdbPath)
	case "set":
		msg, err = s.svc.AdbSetProxy(req.AdbPath, req.Serial)
	case "clear":
		msg, err = s.svc.AdbClearProxy(req.AdbPath, req.Serial)
	default:
		writeErr(w, http.StatusBadRequest, "action 须为 test|set|clear")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// ---------- 域名组 ----------

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/domains")
	id := strings.Trim(rest, "/")
	project := r.URL.Query().Get("project")

	switch r.Method {
	case http.MethodGet:
		if id == "" {
			v, err := s.svc.ListDomainGroups(project)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
		v, err := s.svc.GetDomainGroup(project, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPost:
		// /domains/import {id, source}：从本地路径或 http(s) URL 导入
		if id == "import" {
			var req struct {
				ID     string `json:"id"`
				Source string `json:"source"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
				return
			}
			if strings.TrimSpace(req.Source) == "" {
				writeErr(w, http.StatusBadRequest, "source 为空（本地文件路径或 http(s) URL）")
				return
			}
			v, err := s.svc.ImportDomainGroup(project, req.ID, req.Source)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
		// /domains/{id} {content}：保存/新建（覆盖）
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		v, err := s.svc.SaveDomainGroup(project, id, req.Content)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if id == "" || id == "import" {
			writeErr(w, http.StatusBadRequest, "缺少域名组 id")
			return
		}
		if err := s.svc.DeleteDomainGroup(project, id); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST/DELETE")
	}
}

// ---------- 进程枚举 / 调试重发 ----------

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"processes": s.svc.ListProcesses()})
}

func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	raw, err := readBody(r, 2<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := s.svc.Compose(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// ---------- 响应辅助 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		log.Printf("控制 API 响应编码失败: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func readBody(r *http.Request, max int64) (json.RawMessage, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, max))
	if err != nil {
		return nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	return json.RawMessage(data), nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
