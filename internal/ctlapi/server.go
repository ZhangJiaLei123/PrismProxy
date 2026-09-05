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
var UISettingsTabs = []string{"general", "network", "decrypt", "capture", "domains"}

// Service 控制面服务层：接线层（main 包）实现，ctlapi 不依赖 Wails/具体业务包。
// 返回值以可 JSON 序列化类型为主；error 非空时 HTTP 响应 4xx/5xx。
type Service interface {
	// 状态与流量
	Status() map[string]any                 // 代理状态 + 流计数 + 系统代理状态 + ui/headless 标记
	ListFlows(limit int, filter string) any // 流摘要（不含 body）
	GetFlow(id string) (any, error)
	GetFlowBody(id, which string) (any, error)
	ClearFlows() int // 返回清除条数（置顶保留）
	// 规则（过滤规则组 / 解密规则）
	ListRules() any
	RuleIgnore(target, value string) (bool, error) // target=host|process；added=false 表示幂等已存在
	RuleGroupSetEnabled(id string, enabled bool) error
	RuleDecrypt(action, host string) error // action=mitm|bypass
	// 系统代理 / 设置
	SysProxy(action string) (string, error) // action=on|off|status → 返回 state
	GetSettings() any
	SaveSettings(raw json.RawMessage) (warnings []string, err error)
	// UI 控制（第二步）：仅 GUI 模式真正生效
	UIClear() (cleared int, ui bool)
	UISettings(tab string) (ui bool)
}

// Server 控制 API HTTP 服务（仅回环）
type Server struct {
	addr     string
	token    string
	endpoint string // 落盘文件（endpoint.json）
	svc      Service

	mu      sync.Mutex
	listener net.Listener
	httpSrv *http.Server
	uiOn    bool // 当前是否有 GUI（影响 ui:* 事件回调可用性）
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
	s.httpSrv = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
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

// Close 停止服务并删除 endpoint 文件
func (s *Server) Close() {
	s.mu.Lock()
	srv, ln := s.httpSrv, s.listener
	s.httpSrv, s.listener = nil, nil
	s.mu.Unlock()
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
	mux.HandleFunc("/api/v1/flows/", s.auth(s.handleFlowSub))
	mux.HandleFunc("/api/v1/rules", s.auth(s.handleRules))
	mux.HandleFunc("/api/v1/rules/", s.auth(s.handleRulesSub))
	mux.HandleFunc("/api/v1/sysproxy", s.auth(s.handleSysProxy))
	mux.HandleFunc("/api/v1/settings", s.auth(s.handleSettings))
	mux.HandleFunc("/api/v1/ui/", s.auth(s.handleUI))
	return mux
}

// auth 校验 Bearer token（常量时间比较防侧信道）
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	want := "Bearer " + s.token
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got == "" {
			got = "Bearer " + r.Header.Get("X-Prism-Token")
		}
		if !secureEqual(got, want) {
			writeErr(w, http.StatusUnauthorized, "未授权：token 缺失或错误")
			return
		}
		next(w, r)
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
	if parts[0] == "" || parts[0] == "clear" {
		// /flows/clear 落到 handleFlows（mux 精确匹配优先，此处兜底 404）
		writeErr(w, http.StatusNotFound, "未知路径")
		return
	}
	id := parts[0]
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	if len(parts) == 1 {
		v, err := s.svc.GetFlow(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	if parts[1] == "body" {
		which := r.URL.Query().Get("which")
		if which == "" {
			which = "resp"
		}
		v, err := s.svc.GetFlowBody(id, which)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	}
	writeErr(w, http.StatusNotFound, "未知路径")
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.svc.ListRules())
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
			added, err := s.svc.RuleIgnore(req.Target, req.Value)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"added": added})
		case "decrypt":
			if err := s.svc.RuleDecrypt(req.Kind, req.Host); err != nil {
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
	// /api/v1/rules/groups/{id}/enabled  {enabled:bool}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/rules/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 || parts[0] != "groups" || parts[2] != "enabled" {
		writeErr(w, http.StatusNotFound, "未知路径（可用：/rules/groups/{id}/enabled）")
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
	if err := s.svc.RuleGroupSetEnabled(parts[1], req.Enabled); err != nil {
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
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.svc.GetSettings())
	case http.MethodPut:
		raw, err := readBody(r, 4<<20)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		warnings, err := s.svc.SaveSettings(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warnings": warnings})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
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
