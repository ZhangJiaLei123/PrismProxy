package ctlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeService 测试用 Service 实现，记录调用参数
type fakeService struct {
	mu sync.Mutex

	cleared   int
	uiCleared bool
	uiTab     string
	uiOn      bool

	ignoredTarget string
	ignoredValue  string
	groupID       string
	groupEnabled  bool
	decryptAction string
	decryptHost   string
	sysproxyAct   string
	savedSettings []byte
	projSeen      string // 最近一次规则/设置调用的 project 参数

	pinID     string // 最近一次 SetFlowPinned 调用
	pinPinned bool
	pinCalls  int
	curlID    string
	curlShell string

	lastTagID   string // 最近一次 ListFlowsByTag 的查询参数
	lastScope   string
	lastStart   int64
	lastEnd     int64
	lastQ       string
	lastSort    string
	lastDir     string
	lastShowIgn bool
	ignoreKind  string // 最近一次忽略名单写/删参数
	ignoreValue string
	ignoreCalls int

	// AI 桩状态（M13）
	aiCfg      map[string]any         // GetAIConfig 返回值（nil=默认）
	aiCfgErr   error                  // GetAIConfig 同步错误
	aiSaveErr  error                  // SaveAIConfig 同步错误
	aiSavedRaw []byte                 // 最近一次 SaveAIConfig 请求体
	aiTestErr  error                  // AITestConnection 同步错误
	chatReq    AIChatRequest          // 最近一次 StreamAIChat 请求
	chatScript func(AIChatEmit) error // 自定义帧脚本；nil=默认 meta→delta→done

	// 代理池桩状态（P3-6/P3-8）
	poolErr     error          // 非 nil 时各 PoolXxx 方法统一返回（错误码映射测试用）
	poolSaveRaw []byte         // 最近一次 PoolSaveProvider 请求体
	poolTestRaw []byte         // 最近一次 PoolTestProvider 请求体
	poolID      string         // 最近一次 use/delete 的 id
	poolMode    string         // 最近一次 PoolSetMode 的 mode
	poolOnEmpty string         // 最近一次 PoolSetOnEmpty 的策略
	poolWlIP    string         // 最近一次白名单增删的 ip
	poolWlAuto  bool           // 最近一次 PoolAddWhitelist 的 auto
	poolWlYes   bool           // 最近一次 PoolAddWhitelist 的 yes
}

func (f *fakeService) Status() map[string]any {
	return map[string]any{"proxy": map[string]any{"running": true}, "ui": f.uiOn, "headless": !f.uiOn}
}
func (f *fakeService) ListFlows(limit int, filter string) any {
	return map[string]any{"limit": limit, "filter": filter, "flows": []any{}}
}
func (f *fakeService) GetFlow(id string) (any, error) {
	return map[string]any{"id": id}, nil
}
func (f *fakeService) GetFlowBody(id, which string) (any, error) {
	return map[string]any{"id": id, "which": which}, nil
}
func (f *fakeService) ClearFlows() int {
	f.cleared++
	return 3
}
func (f *fakeService) ListRules(project string) (any, error) {
	return map[string]any{"filterGroups": []any{}, "project": project}, nil
}
func (f *fakeService) RuleIgnore(project, target, value string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projSeen = project
	f.ignoredTarget, f.ignoredValue = target, value
	return true, nil
}
func (f *fakeService) RuleGroupSetEnabled(project, id string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projSeen = project
	f.groupID, f.groupEnabled = id, enabled
	return nil
}
func (f *fakeService) RuleDecrypt(project, action, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projSeen = project
	f.decryptAction, f.decryptHost = action, host
	return nil
}
func (f *fakeService) SysProxy(action string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sysproxyAct = action
	if action == "status" {
		return "off", nil
	}
	return action, nil
}
func (f *fakeService) GetSettings(project string) (any, error) {
	return map[string]any{"maxFlows": 2000, "project": project}, nil
}
func (f *fakeService) SaveSettings(project string, raw json.RawMessage) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projSeen = project
	f.savedSettings = raw
	return []string{"测试 warning"}, nil
}

// ---------- 项目（M9） ----------

func (f *fakeService) ListProjects() any {
	return map[string]any{
		"projects":       []map[string]any{{"id": "p1", "name": "默认项目"}},
		"currentProject": "p1",
	}
}
func (f *fakeService) CreateProject(name, from string) (any, error) {
	return map[string]any{"id": "p2", "name": name, "from": from}, nil
}
func (f *fakeService) RenameProject(idOrName, name string) (any, error) {
	return map[string]any{"ok": true, "id": idOrName, "name": name}, nil
}
func (f *fakeService) DeleteProject(idOrName string) error { return nil }
func (f *fakeService) UIClear() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uiCleared = true
	return 3, f.uiOn
}
func (f *fakeService) UISettings(tab string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uiTab = tab
	return f.uiOn
}

// ---------- M10 补面方法桩 ----------

func (f *fakeService) StartProxy() error                      { return nil }
func (f *fakeService) StopProxy() error                       { return nil }
func (f *fakeService) InstallCA() error                       { return nil }
func (f *fakeService) ResetProxyBypass() int                  { return 0 }
func (f *fakeService) AdbTest(adbPath string) (string, error) { return "ok", nil }
func (f *fakeService) AdbSetProxy(adbPath, serial string) (string, error) {
	return "已设置设备代理", nil
}
func (f *fakeService) AdbClearProxy(adbPath, serial string) (string, error) {
	return "已清除设备代理", nil
}
func (f *fakeService) AdbInstallSystemCert(adbPath, serial string) (string, error) {
	return "系统证书已注入", nil
}
func (f *fakeService) AdbSystemCertStatus(adbPath, serial string) (string, error) {
	return "installed", nil
}
func (f *fakeService) AdbDevices() any { return map[string]any{"devices": []any{}} }
func (f *fakeService) ListDomainGroups(project string) (any, error) {
	return map[string]any{"project": project, "groups": []any{}}, nil
}
func (f *fakeService) GetDomainGroup(project, id string) (any, error) {
	return map[string]any{"id": id, "text": ""}, nil
}
func (f *fakeService) SaveDomainGroup(project, id, content string) (any, error) {
	return map[string]any{"ok": true, "id": id}, nil
}
func (f *fakeService) DeleteDomainGroup(project, id string) error { return nil }
func (f *fakeService) ImportDomainGroup(project, id, source string) (any, error) {
	return map[string]any{"ok": true, "id": id}, nil
}
func (f *fakeService) ExportRules(project string, embedGroups bool) (json.RawMessage, error) {
	return json.RawMessage(`{"version":1,"filterGroups":[],"decryptRules":[]}`), nil
}
func (f *fakeService) ImportRules(project, src string) ([]string, error) {
	return nil, nil
}
func (f *fakeService) SetFlowPinned(id string, pinned bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinID, f.pinPinned, f.pinCalls = id, pinned, f.pinCalls+1
	return nil
}
func (f *fakeService) BuildCurl(id, shell string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.curlID, f.curlShell = id, shell
	return map[string]any{"command": "curl " + id, "shell": shell}, nil
}
func (f *fakeService) Compose(raw json.RawMessage) (any, error) {
	return map[string]any{"ID": "composer-1"}, nil
}
func (f *fakeService) ListProcesses() []string { return []string{"powershell.exe"} }

// ---------- M12 标签/复盘方法桩 ----------

func (f *fakeService) ListTagsForReview() (any, error) {
	return map[string]any{"tags": []any{}}, nil
}
func (f *fakeService) TagFlowsForReview(raw json.RawMessage) (any, error) {
	return map[string]any{"ok": true}, nil
}
func (f *fakeService) ListFlowsByTag(tagID, scope string, start, end int64, limit, offset int, q, sort, dir string, showIgnored bool) (any, error) {
	f.mu.Lock()
	f.lastTagID, f.lastScope, f.lastStart, f.lastEnd, f.lastQ = tagID, scope, start, end, q
	f.lastSort, f.lastDir, f.lastShowIgn = sort, dir, showIgnored
	f.mu.Unlock()
	return map[string]any{
		"flows": []any{}, "total": 0, "tag": tagID, "scope": scope,
		"start": start, "end": end, "limit": limit, "offset": offset, "q": q,
		"sort": sort, "dir": dir, "showIgnored": showIgnored,
	}, nil
}
func (f *fakeService) ListReviewIgnores() (any, error) {
	return map[string]any{"ignores": []any{}}, nil
}
func (f *fakeService) AddReviewIgnore(raw json.RawMessage) (any, error) {
	var req struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	f.ignoreKind, f.ignoreValue, f.ignoreCalls = req.Kind, req.Value, f.ignoreCalls+1
	f.mu.Unlock()
	return map[string]any{"added": true}, nil
}
func (f *fakeService) DeleteReviewIgnore(kind, value string) (any, error) {
	f.mu.Lock()
	f.ignoreKind, f.ignoreValue, f.ignoreCalls = kind, value, f.ignoreCalls+1
	f.mu.Unlock()
	return map[string]any{"deleted": true}, nil
}
func (f *fakeService) TagHistogram(tagID, scope string, start, end int64, buckets int) (any, error) {
	return map[string]any{"start": 0, "end": 0, "buckets": []any{}}, nil
}
func (f *fakeService) GetTaggedFlow(id string) (any, error) {
	return map[string]any{"id": id}, nil
}
func (f *fakeService) GetTaggedFlowBody(id, which string) (any, error) {
	return map[string]any{"id": id, "which": which}, nil
}
func (f *fakeService) RenameTag(raw json.RawMessage) error { return nil }
func (f *fakeService) DeleteTag(tagID string, deleteFlows bool) (int, error) {
	return 0, nil
}

// ---------- AI 桩（M13 P2-11 路由测试用） ----------

func (f *fakeService) GetAIConfig() (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.aiCfgErr != nil {
		return nil, f.aiCfgErr
	}
	if f.aiCfg != nil {
		return f.aiCfg, nil
	}
	return map[string]any{"enabled": false, "model": "", "hasApiKey": false, "apiKeyMasked": ""}, nil
}
func (f *fakeService) SaveAIConfig(raw json.RawMessage) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aiSavedRaw = []byte(raw)
	if f.aiSaveErr != nil {
		return nil, f.aiSaveErr
	}
	return map[string]any{"enabled": true, "model": "m", "hasApiKey": true, "apiKeyMasked": "sk-***"}, nil
}
func (f *fakeService) AITestConnection() (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.aiTestErr != nil {
		return nil, f.aiTestErr
	}
	return map[string]any{"ok": true, "model": "m", "latencyMs": 120, "message": "ok"}, nil
}
func (f *fakeService) StreamAIChat(ctx context.Context, req AIChatRequest, emit AIChatEmit) error {
	f.mu.Lock()
	f.chatReq = req
	script := f.chatScript
	f.mu.Unlock()
	if script != nil {
		return script(emit)
	}
	// 默认脚本：meta → delta → done
	if err := emit(AIEventMeta, map[string]any{"mode": req.Mode, "total": 1, "sent": 1,
		"budget": map[string]int{"flows": 50, "kb": 64}, "truncated": false}); err != nil {
		return err
	}
	if err := emit(AIEventDelta, map[string]string{"text": "你好"}); err != nil {
		return err
	}
	return emit(AIEventDone, map[string]any{"finishReason": "stop", "truncated": false})
}

// ---------- 代理池桩（P3-6/P3-8） ----------

func (f *fakeService) PoolStatus() (any, error) {
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"mode": "direct", "activeProvider": "", "running": false}, nil
}

func (f *fakeService) PoolVendors() any {
	return []any{map[string]any{"id": "91http", "name": "91HTTP",
		"supportUserPass": true, "supportWhitelist": true, "supportRegion": true}}
}

func (f *fakeService) PoolProviders() (any, error) {
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return []any{}, nil
}

func (f *fakeService) PoolSaveProvider(raw json.RawMessage) (any, error) {
	f.mu.Lock()
	f.poolSaveRaw = append([]byte(nil), raw...)
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"provider": map[string]any{"id": "p1"}, "restarted": false}, nil
}

func (f *fakeService) PoolDeleteProvider(id string) (any, error) {
	f.mu.Lock()
	f.poolID = id
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"restarted": false}, nil
}

func (f *fakeService) PoolUseProvider(id string) (any, error) {
	f.mu.Lock()
	f.poolID = id
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"status": map[string]any{"mode": "pool"}, "restarted": false}, nil
}

func (f *fakeService) PoolSetMode(mode string) (any, error) {
	f.mu.Lock()
	f.poolMode = mode
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"status": map[string]any{"mode": mode}, "restarted": false}, nil
}

func (f *fakeService) PoolSetOnEmpty(onEmpty string) (any, error) {
	f.mu.Lock()
	f.poolOnEmpty = onEmpty
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"status": map[string]any{"onEmpty": onEmpty}, "restarted": false}, nil
}

func (f *fakeService) PoolTestProvider(raw json.RawMessage) (any, error) {
	f.mu.Lock()
	f.poolTestRaw = append([]byte(nil), raw...)
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"addr": "1.2.3.4:8080", "count": 1, "withAuth": false}, nil
}

func (f *fakeService) PoolRotate() (any, error) {
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return map[string]any{"running": true}, nil
}

func (f *fakeService) PoolWhitelist() (any, error) {
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return []any{"1.2.3.4"}, nil
}

func (f *fakeService) PoolAddWhitelist(ip string, auto, yes bool) (any, error) {
	f.mu.Lock()
	f.poolWlIP, f.poolWlAuto, f.poolWlYes = ip, auto, yes
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return []any{"1.2.3.4"}, nil
}

func (f *fakeService) PoolDelWhitelist(ip string) (any, error) {
	f.mu.Lock()
	f.poolWlIP = ip
	f.mu.Unlock()
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return []any{}, nil
}

func startTestServer(t *testing.T, svc Service) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	epFile := filepath.Join(dir, endpointFileName)
	srv := NewServer("127.0.0.1:0", "test-token-abc123", epFile, svc, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("启动控制服务失败: %v", err)
	}
	t.Cleanup(srv.Close)
	ep, err := ReadEndpoint(epFile)
	if err != nil {
		t.Fatalf("读取 endpoint 文件失败: %v", err)
	}
	return srv, ep.Addr, ep.Token
}

func doRequest(t *testing.T, method, addr, token, path string, body io.Reader) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+addr+"/api/v1"+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return resp.StatusCode, m
}

// 无 token / 错误 token → 401
func TestAuthRejectsBadToken(t *testing.T) {
	svc := &fakeService{}
	_, addr, _ := startTestServer(t, svc)

	if code, m := doRequest(t, "GET", addr, "", "/status", nil); code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，得 %d (%v)", code, m)
	}
	if code, _ := doRequest(t, "GET", addr, "wrong-token", "/status", nil); code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，得 %d", code)
	}
	if code, m := doRequest(t, "GET", addr, "test-token-abc123", "/status", nil); code != http.StatusOK {
		t.Fatalf("正确 token 应 200，得 %d (%v)", code, m)
	}
}

// GET /status 返回代理/UI 状态
func TestStatusEndpoint(t *testing.T) {
	svc := &fakeService{uiOn: true}
	_, addr, token := startTestServer(t, svc)
	code, m := doRequest(t, "GET", addr, token, "/status", nil)
	if code != 200 {
		t.Fatalf("status 应 200，得 %d", code)
	}
	if m["ui"] != true {
		t.Fatalf("ui 应为 true，得 %v", m["ui"])
	}
}

// POST /ui/clear → UIClear；POST /ui/settings → UISettings（含 tab 校验）
func TestUIEndpoints(t *testing.T) {
	svc := &fakeService{uiOn: true}
	_, addr, token := startTestServer(t, svc)

	code, m := doRequest(t, "POST", addr, token, "/ui/clear", nil)
	if code != 200 || m["cleared"] != float64(3) || m["ui"] != true {
		t.Fatalf("ui/clear 响应异常: %d %v", code, m)
	}
	if !svc.uiCleared {
		t.Fatal("UIClear 未被调用")
	}

	code, m = doRequest(t, "POST", addr, token, "/ui/settings", strings.NewReader(`{"tab":"network"}`))
	if code != 200 || m["ui"] != true {
		t.Fatalf("ui/settings 响应异常: %d %v", code, m)
	}
	if svc.uiTab != "network" {
		t.Fatalf("tab 应为 network，得 %q", svc.uiTab)
	}

	// 非法 tab → 400
	code, _ = doRequest(t, "POST", addr, token, "/ui/settings", strings.NewReader(`{"tab":"bogus"}`))
	if code != http.StatusBadRequest {
		t.Fatalf("非法 tab 应 400，得 %d", code)
	}

	// headless（uiOn=false）→ ui:false 但不报错
	svc.uiOn = false
	code, m = doRequest(t, "POST", addr, token, "/ui/settings", strings.NewReader(`{}`))
	if code != 200 || m["ui"] != false {
		t.Fatalf("headless ui/settings 应 200+ui:false，得 %d %v", code, m)
	}
}

// POST /flows/clear → ClearFlows（回归：ServeMux 精确模式 /flows 不匹配 /flows/clear，须显式注册）
func TestFlowsClearEndpoint(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	code, m := doRequest(t, "POST", addr, token, "/flows/clear", nil)
	if code != 200 {
		t.Fatalf("POST /flows/clear 应 200，得 %d (%v)", code, m)
	}
	if m["cleared"] == nil || m["pinnedKept"] != true {
		t.Fatalf("clear 响应异常: %v", m)
	}
	if svc.cleared != 1 {
		t.Fatalf("ClearFlows 应被调用 1 次，得 %d", svc.cleared)
	}
}

// POST /flows/{id}/pin → SetFlowPinned；GET /flows/{id}/curl → BuildCurl
// 回归（H1）：handleFlowSub 曾有顶层 GET 守卫把 POST pin 一律 405，方法校验须下沉各分支
func TestFlowPinCurlEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	// pin 置顶（POST）
	code, m := doRequest(t, "POST", addr, token, "/flows/f1/pin", strings.NewReader(`{"pinned":true}`))
	if code != 200 {
		t.Fatalf("POST /flows/f1/pin 应 200，得 %d (%v)", code, m)
	}
	if m["pinned"] != true || m["id"] != "f1" {
		t.Fatalf("pin 响应异常: %v", m)
	}
	if svc.pinID != "f1" || !svc.pinPinned || svc.pinCalls != 1 {
		t.Fatalf("SetFlowPinned 调用错误: %s %v %d", svc.pinID, svc.pinPinned, svc.pinCalls)
	}

	// pin 取消（POST pinned:false）
	code, _ = doRequest(t, "POST", addr, token, "/flows/f1/pin", strings.NewReader(`{"pinned":false}`))
	if code != 200 || svc.pinPinned || svc.pinCalls != 2 {
		t.Fatalf("取消置顶错误: code=%d pinned=%v calls=%d", code, svc.pinPinned, svc.pinCalls)
	}

	// pin 用 GET 应 405（校验下沉到 pin 分支）
	if code, _ := doRequest(t, "GET", addr, token, "/flows/f1/pin", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET pin 应 405，得 %d", code)
	}

	// curl（GET，默认 cmd）
	code, m = doRequest(t, "GET", addr, token, "/flows/f1/curl", nil)
	if code != 200 || m["shell"] != "cmd" || svc.curlID != "f1" || svc.curlShell != "cmd" {
		t.Fatalf("curl 响应/调用异常: code=%d m=%v id=%s shell=%s", code, m, svc.curlID, svc.curlShell)
	}
	// curl 指定 shell
	code, _ = doRequest(t, "GET", addr, token, "/flows/f2/curl?shell=bash", nil)
	if code != 200 || svc.curlID != "f2" || svc.curlShell != "bash" {
		t.Fatalf("curl?shell=bash 错误: code=%d id=%s shell=%s", code, svc.curlID, svc.curlShell)
	}

	// curl 用 POST 应 405
	if code, _ := doRequest(t, "POST", addr, token, "/flows/f1/curl", strings.NewReader("{}")); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST curl 应 405，得 %d", code)
	}

	// GET /flows/{id} 单流详情仍正常（GET 校验下沉到 len(parts)==1 分支）
	if code, _ := doRequest(t, "GET", addr, token, "/flows/f1", nil); code != 200 {
		t.Fatalf("GET /flows/f1 应 200，得 %d", code)
	}
}

// rules 写入路径（项目操作显式化：rules 类请求必须携带 ?project=）
func TestRulesEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	code, _ := doRequest(t, "POST", addr, token, "/rules?project=p1",
		strings.NewReader(`{"action":"ignore","target":"host","value":"example.com"}`))
	if code != 200 {
		t.Fatalf("rules ignore 应 200，得 %d", code)
	}
	if svc.ignoredTarget != "host" || svc.ignoredValue != "example.com" || svc.projSeen != "p1" {
		t.Fatalf("RuleIgnore 参数错误: %s %s project=%s", svc.ignoredTarget, svc.ignoredValue, svc.projSeen)
	}

	code, _ = doRequest(t, "POST", addr, token, "/rules/groups/g1/enabled?project=p1",
		strings.NewReader(`{"enabled":false}`))
	if code != 200 {
		t.Fatalf("group enabled 应 200，得 %d", code)
	}
	if svc.groupID != "g1" || svc.groupEnabled != false {
		t.Fatalf("RuleGroupSetEnabled 参数错误: %s %v", svc.groupID, svc.groupEnabled)
	}

	code, _ = doRequest(t, "POST", addr, token, "/rules?project=p1",
		strings.NewReader(`{"action":"decrypt","kind":"bypass","host":"pinned.app"}`))
	if code != 200 || svc.decryptAction != "bypass" || svc.decryptHost != "pinned.app" {
		t.Fatalf("decrypt 参数/状态错误: code=%d %s %s", code, svc.decryptAction, svc.decryptHost)
	}

	// sysproxy
	code, _ = doRequest(t, "POST", addr, token, "/sysproxy", strings.NewReader(`{"action":"on"}`))
	if code != 200 || svc.sysproxyAct != "on" {
		t.Fatalf("sysproxy on 错误: code=%d act=%s", code, svc.sysproxyAct)
	}
}

// PUT /settings 透传到 SaveSettings
func TestSettingsPut(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)
	code, m := doRequest(t, "PUT", addr, token, "/settings?project=p1", strings.NewReader(`{"maxFlows":3000}`))
	if code != 200 {
		t.Fatalf("PUT settings 应 200，得 %d", code)
	}
	if svc.projSeen != "p1" {
		t.Fatalf("SaveSettings project 透传错误: %q", svc.projSeen)
	}
	if !strings.Contains(string(svc.savedSettings), "3000") {
		t.Fatalf("SaveSettings 未收到新配置: %s", svc.savedSettings)
	}
	warns, _ := m["warnings"].([]any)
	if len(warns) != 1 {
		t.Fatalf("warnings 应透传 1 条，得 %v", m["warnings"])
	}
}

// 项目操作显式化（doc/CLI项目操作显式化设计.md §10）：
// POST /projects action=switch/close → 400 + 引导文案；rules/settings/domains 缺 project query → 400
func TestProjectExplicitness(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	// switch / close action 已移除 → 400 + 引导文案
	for _, action := range []string{"switch", "close"} {
		code, m := doRequest(t, "POST", addr, token, "/projects",
			strings.NewReader(`{"action":"`+action+`"}`))
		if code != http.StatusBadRequest {
			t.Fatalf("projects action=%s 应 400，得 %d (%v)", action, code, m)
		}
		if !strings.Contains(fmt.Sprint(m["error"]), "已移除") {
			t.Fatalf("projects action=%s 文案应含「已移除」: %v", action, m["error"])
		}
	}

	// 缺 project query → 400 + 提示文案
	for _, tc := range []struct {
		method, path string
		body         io.Reader
	}{
		{"GET", "/rules", nil},
		{"PUT", "/settings", strings.NewReader(`{}`)},
		{"GET", "/domains", nil},
	} {
		code, m := doRequest(t, tc.method, addr, token, tc.path, tc.body)
		if code != http.StatusBadRequest {
			t.Fatalf("%s %s 缺 project 应 400，得 %d (%v)", tc.method, tc.path, code, m)
		}
		if !strings.Contains(fmt.Sprint(m["error"]), "缺少 project") {
			t.Fatalf("%s %s 文案应含「缺少 project」: %v", tc.method, tc.path, m["error"])
		}
	}
}

// CLI 侧显式化校验：project switch/close 前置拦截 exit 2；rules/settings 缺 --project exit 2
func TestCLIExplicitProject(t *testing.T) {
	svc := &fakeService{uiOn: true}
	srv, _, _ := startTestServer(t, svc)
	dir := filepath.Dir(srv.endpoint)

	if code := RunCLI(dir, []string{"project", "switch", "p1"}); code != 2 {
		t.Fatalf("cli project switch 应 exit 2（已移除），得 %d", code)
	}
	if code := RunCLI(dir, []string{"project", "close"}); code != 2 {
		t.Fatalf("cli project close 应 exit 2（已移除），得 %d", code)
	}
	if code := RunCLI(dir, []string{"rules", "list"}); code != 2 {
		t.Fatalf("cli rules list 缺 --project 应 exit 2，得 %d", code)
	}
	if code := RunCLI(dir, []string{"settings", "get"}); code != 2 {
		t.Fatalf("cli settings get 缺 --project 应 exit 2，得 %d", code)
	}
	// exit 2 路径不应触达 Service（无副作用）
	if svc.projSeen != "" || svc.uiTab != "" {
		t.Fatalf("exit 2 路径不应触达 Service: projSeen=%q uiTab=%q", svc.projSeen, svc.uiTab)
	}

	// 审计修订回归（2026-09-19）：缺 --project 校验前置于 newClient，实例未启动
	// （无 endpoint 文件）时同一用法错误仍 exit 2，而非 exit 1 连接错误
	if code := RunCLI(t.TempDir(), []string{"rules", "list"}); code != 2 {
		t.Fatalf("离线时 rules list 缺 --project 仍应 exit 2，得 %d", code)
	}
	if code := RunCLI(t.TempDir(), []string{"domains", "list"}); code != 2 {
		t.Fatalf("离线时 domains list 缺 --project 仍应 exit 2，得 %d", code)
	}
}

// endpoint 文件：关闭后删除
func TestEndpointFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	epFile := filepath.Join(dir, endpointFileName)
	srv := NewServer("127.0.0.1:0", "", epFile, &fakeService{}, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if _, err := os.Stat(epFile); err != nil {
		t.Fatalf("endpoint 文件应存在: %v", err)
	}
	ep, _ := ReadEndpoint(epFile)
	if ep.Token == "" || ep.Addr == "" {
		t.Fatalf("endpoint 内容不完整: %+v", ep)
	}
	srv.Close()
	if _, err := os.Stat(epFile); !os.IsNotExist(err) {
		t.Fatalf("关闭后 endpoint 文件应删除，得 err=%v", err)
	}
}

// RunCLI 端到端：status / flows list / ui settings / 未发现实例
func TestRunCLIEndToEnd(t *testing.T) {
	svc := &fakeService{uiOn: true}
	srv, _, _ := startTestServer(t, svc)
	dir := filepath.Dir(srv.endpoint)

	// status
	if code := RunCLI(dir, []string{"status"}); code != 0 {
		t.Fatalf("cli status 退出码应 0，得 %d", code)
	}
	// flows list --filter --limit
	if code := RunCLI(dir, []string{"flows", "list", "--filter", "baidu", "--limit", "10"}); code != 0 {
		t.Fatalf("cli flows list 退出码应 0，得 %d", code)
	}
	// ui settings network
	if code := RunCLI(dir, []string{"ui", "settings", "network"}); code != 0 {
		t.Fatalf("cli ui settings 退出码应 0，得 %d", code)
	}
	if svc.uiTab != "network" {
		t.Fatalf("ui tab 应为 network，得 %q", svc.uiTab)
	}
	// rules ignore host（-P 显式指定目标项目；缺省时 RunCLI 直接 exit 2，见 TestCLIExplicitProject）
	if code := RunCLI(dir, []string{"rules", "ignore", "host", "cli.example.com", "-P", "默认项目"}); code != 0 {
		t.Fatalf("cli rules ignore 退出码应 0，得 %d", code)
	}
	if svc.ignoredValue != "cli.example.com" || svc.projSeen != "默认项目" {
		t.Fatalf("ignore 透传错误: value=%q project=%q", svc.ignoredValue, svc.projSeen)
	}

	// 无实例（空目录）→ 非零退出码
	empty := t.TempDir()
	if code := RunCLI(empty, []string{"status"}); code == 0 {
		t.Fatal("无实例时 cli 应非零退出")
	}
}

// token 生成：长度与唯一性
func TestNewToken(t *testing.T) {
	a, b := newToken(), newToken()
	if len(a) != 64 || a == b {
		t.Fatalf("token 应为 64 字符且唯一: len(a)=%d equal=%v", len(a), a == b)
	}
}

// ---------- P2-11：AI 路由（设计 §5.3/§5.5，M13） ----------

// sseFrame 单帧（SSE 文本解析用）
type sseFrame struct {
	event string
	data  string
}

// parseSSEFrames 解析 SSE 文本为帧序列（event/data 对，跳过注释行与空块）
func parseSSEFrames(t *testing.T, body string) []sseFrame {
	t.Helper()
	var frames []sseFrame
	for _, block := range strings.Split(body, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, ":") {
			continue
		}
		var f sseFrame
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.data = strings.TrimPrefix(line, "data: ")
			}
		}
		frames = append(frames, f)
	}
	return frames
}

// GET /ai/config 掩码视图；POST 透传部分更新原文；错误映射（ErrAIBadReq→400）；PUT→405
func TestAIConfigEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	code, m := doRequest(t, "GET", addr, token, "/ai/config", nil)
	if code != 200 || m["hasApiKey"] != false {
		t.Fatalf("GET ai/config 应 200 且掩码视图: %d %v", code, m)
	}

	// POST 只发 {apiKey}：路由层透传原文（部分更新语义由实现层保证）
	code, m = doRequest(t, "POST", addr, token, "/ai/config", strings.NewReader(`{"apiKey":"sk-test-1"}`))
	if code != 200 || m["hasApiKey"] != true || m["apiKeyMasked"] != "sk-***" {
		t.Fatalf("POST ai/config 响应异常: %d %v", code, m)
	}
	svc.mu.Lock()
	raw := string(svc.aiSavedRaw)
	svc.mu.Unlock()
	if raw != `{"apiKey":"sk-test-1"}` {
		t.Fatalf("SaveAIConfig 应透传原文，得 %s", raw)
	}

	// GET 错误 → 400（ErrAIBadReq 映射）
	svc.mu.Lock()
	svc.aiCfgErr = fmt.Errorf("%w: 配置损坏", ErrAIBadReq)
	svc.mu.Unlock()
	if code, _ := doRequest(t, "GET", addr, token, "/ai/config", nil); code != http.StatusBadRequest {
		t.Fatalf("GET ai/config ErrAIBadReq 应 400，得 %d", code)
	}

	if code, _ := doRequest(t, "PUT", addr, token, "/ai/config", strings.NewReader(`{}`)); code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT ai/config 应 405，得 %d", code)
	}
}

// POST /ai/test → {ok,model,latencyMs,message}；ErrAIBadReq→400；GET→405
func TestAITestEndpoint(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	code, m := doRequest(t, "POST", addr, token, "/ai/test", nil)
	if code != 200 || m["ok"] != true || m["latencyMs"] != float64(120) {
		t.Fatalf("POST ai/test 应 200: %d %v", code, m)
	}

	svc.mu.Lock()
	svc.aiTestErr = fmt.Errorf("%w: 未配置", ErrAIBadReq)
	svc.mu.Unlock()
	if code, _ := doRequest(t, "POST", addr, token, "/ai/test", nil); code != http.StatusBadRequest {
		t.Fatalf("ai/test 未配置应 400，得 %d", code)
	}

	if code, _ := doRequest(t, "GET", addr, token, "/ai/test", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET ai/test 应 405，得 %d", code)
	}
}

// POST /ai/chat SSE 帧序：meta → delta → done（默认脚本）；请求参数透传
func TestAIChatSSEFrames(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	req, _ := http.NewRequest("POST", "http://"+addr+"/api/v1/ai/chat",
		strings.NewReader(`{"mode":"explain","flowId":"f1"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat 请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chat 应 200，得 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream，得 %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	frames := parseSSEFrames(t, string(body))
	if len(frames) != 3 {
		t.Fatalf("应 3 帧（meta/delta/done），得 %d: %q", len(frames), body)
	}
	if frames[0].event != AIEventMeta || frames[1].event != AIEventDelta || frames[2].event != AIEventDone {
		t.Fatalf("帧序应 meta→delta→done: %s %s %s", frames[0].event, frames[1].event, frames[2].event)
	}
	svc.mu.Lock()
	got := svc.chatReq
	svc.mu.Unlock()
	if got.FlowID != "f1" || got.Mode != "explain" {
		t.Fatalf("StreamAIChat 请求参数错误: %+v", got)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(frames[0].data), &meta); err != nil {
		t.Fatalf("meta 帧解析失败: %v", err)
	}
	if meta["mode"] != "explain" || meta["total"] != float64(1) {
		t.Fatalf("meta 帧内容异常: %v", meta)
	}
}

// chat 同步错误映射：ErrAIBadReq→400 / ErrAIConflict→409 / 其他→500（JSON，无 SSE 头）；
// meta 后出错流已开始（200 + 已发帧不转 JSON）；GET→405
func TestAIChatSyncErrors(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"未配置", fmt.Errorf("%w: AI 分析未启用", ErrAIBadReq), 400},
		{"并发", fmt.Errorf("%w", ErrAIConflict), 409},
		{"内部", fmt.Errorf("boom"), 500},
	}
	for _, c := range cases {
		svc.mu.Lock()
		svc.chatScript = func(emit AIChatEmit) error { return c.err }
		svc.mu.Unlock()
		code, m := doRequest(t, "POST", addr, token, "/ai/chat", strings.NewReader(`{"mode":"explain","flowId":"f1"}`))
		if code != c.want {
			t.Fatalf("%s: 应 %d，得 %d (%v)", c.name, c.want, code, m)
		}
	}
	svc.mu.Lock()
	svc.chatScript = nil
	svc.mu.Unlock()

	// 已开流（meta 后）错误：200 + 帧已发不转 JSON；error 帧由实现层发出（契约 §5.5）
	svc.mu.Lock()
	svc.chatScript = func(emit AIChatEmit) error {
		_ = emit(AIEventMeta, map[string]any{"total": 1})
		_ = emit(AIEventError, map[string]any{"message": "流中异常"})
		return fmt.Errorf("流中异常")
	}
	svc.mu.Unlock()
	req, _ := http.NewRequest("POST", "http://"+addr+"/api/v1/ai/chat", strings.NewReader(`{"mode":"explain","flowId":"f1"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat 请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	frames := parseSSEFrames(t, string(body))
	if resp.StatusCode != 200 || len(frames) < 2 ||
		frames[0].event != AIEventMeta || frames[len(frames)-1].event != AIEventError {
		t.Fatalf("meta 后错误应 200+meta/error 帧: %d %q", resp.StatusCode, body)
	}

	if code, _ := doRequest(t, "GET", addr, token, "/ai/chat", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET ai/chat 应 405，得 %d", code)
	}
}

// UISettingsTabs 含 ai（P2-11）：ui/settings tab=ai 合法
func TestUISettingsTabsAI(t *testing.T) {
	if !contains(UISettingsTabs, "ai") {
		t.Fatalf("UISettingsTabs 应含 ai: %v", UISettingsTabs)
	}
	svc := &fakeService{uiOn: true}
	_, addr, token := startTestServer(t, svc)
	code, _ := doRequest(t, "POST", addr, token, "/ui/settings", strings.NewReader(`{"tab":"ai"}`))
	if code != 200 {
		t.Fatalf("tab=ai 应 200，得 %d", code)
	}
}
