package ctlapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// 代理池 sentinel（代理池设计 v3 §8.4 错误码约定；P3-6 路由handler 经 poolErrCode 映射）。
// app 接线层（internal/app/pool_bridge.go）以 fmt.Errorf("%w: 细节", ErrPoolXxx) 包装，
// 文案前缀随错误透传给 CLI/前端，与 AI 半边 ErrAIConflict/ErrAIBadReq 同范式。
var (
	ErrPoolBadReq   = errors.New("代理池请求非法")     // → 400 参数/校验失败（含厂商未注册、模式非法）
	ErrPoolNotFound = errors.New("代理池账号不存在")   // → 404 id/名称不存在，或名称重名需改用 id
	ErrPoolConflict = errors.New("代理池状态冲突")     // → 409 非 pool 模式/代理未运行做 rotate
	ErrPoolUpstream = errors.New("代理池厂商接口失败") // → 502 提取/白名单操作失败（厂商 msg 透传）
)

// poolErrCode 同步错误 → HTTP 状态码（未识别错误视为内部错误，同 aiErrCode 口径）
func poolErrCode(err error) int {
	switch {
	case errors.Is(err, ErrPoolBadReq):
		return http.StatusBadRequest
	case errors.Is(err, ErrPoolNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrPoolConflict):
		return http.StatusConflict
	case errors.Is(err, ErrPoolUpstream):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// ---------- 代理池 HTTP handler（设计 §8.4；统一错误映射 poolErrCode） ----------

func (s *Server) handlePoolStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	v, err := s.svc.PoolStatus()
	if err != nil {
		writeErr(w, poolErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePoolVendors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, s.svc.PoolVendors())
}

// handlePoolProviders GET 清单 / POST 新增（PUT 整体替换走 /providers/{id}）
func (s *Server) handlePoolProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		v, err := s.svc.PoolProviders()
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPost:
		raw, err := readBody(r, 1<<20)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		v, err := s.svc.PoolSaveProvider(raw)
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST（更新请用 PUT /proxy/pool/providers/{id}）")
	}
}

// handlePoolProviderSub /providers/{id}：PUT 整体替换 / DELETE 删除（parts 防御 404）
func (s *Server) handlePoolProviderSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/proxy/pool/providers/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 1 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "未知路径（可用：PUT/DELETE /proxy/pool/providers/{id}）")
		return
	}
	id := parts[0]
	switch r.Method {
	case http.MethodPut:
		raw, err := readBody(r, 1<<20)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// 路径 id 为准注入请求体（body 可不带 id）
		raw, err = injectJSONID(raw, id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		v, err := s.svc.PoolSaveProvider(raw)
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		v, err := s.svc.PoolDeleteProvider(id)
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 PUT/DELETE")
	}
}

func (s *Server) handlePoolUse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	v, err := s.svc.PoolUseProvider(req.ID)
	if err != nil {
		writeErr(w, poolErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePoolMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	v, err := s.svc.PoolSetMode(req.Mode)
	if err != nil {
		writeErr(w, poolErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePoolOnEmpty(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		OnEmpty string `json:"onEmpty"`
		Policy  string `json:"policy"` // 兼容别名
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.OnEmpty == "" {
		req.OnEmpty = req.Policy
	}
	v, err := s.svc.PoolSetOnEmpty(req.OnEmpty)
	if err != nil {
		writeErr(w, poolErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePoolTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	raw, err := readBody(r, 1<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := s.svc.PoolTestProvider(raw)
	if err != nil {
		writeErr(w, poolErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePoolRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	v, err := s.svc.PoolRotate()
	if err != nil {
		writeErr(w, poolErrCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handlePoolWhitelist 当前生效账号白名单：GET 列表 / POST {ip?,auto,yes} 添加 / DELETE ?ip= 删除
func (s *Server) handlePoolWhitelist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		v, err := s.svc.PoolWhitelist()
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodPost:
		var req struct {
			IP   string `json:"ip"`
			Auto bool   `json:"auto"`
			Yes  bool   `json:"yes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		v, err := s.svc.PoolAddWhitelist(req.IP, req.Auto, req.Yes)
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		ip := r.URL.Query().Get("ip")
		if strings.TrimSpace(ip) == "" {
			writeErr(w, http.StatusBadRequest, "缺少 ip 参数（DELETE /proxy/pool/whitelist?ip=）")
			return
		}
		v, err := s.svc.PoolDelWhitelist(ip)
		if err != nil {
			writeErr(w, poolErrCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/POST/DELETE")
	}
}
