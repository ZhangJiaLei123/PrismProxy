package ctlapi

import (
	"encoding/json"
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
	switchTo      string

	pinID      string // 最近一次 SetFlowPinned 调用
	pinPinned  bool
	pinCalls   int
	curlID     string
	curlShell  string

	lastTagID string // 最近一次 ListFlowsByTag 的查询参数
	lastScope string
	lastStart int64
	lastEnd   int64
	lastQ     string
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
func (f *fakeService) SwitchProject(idOrName string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switchTo = idOrName
	return map[string]any{"id": "p1", "name": "默认项目"}, nil
}
func (f *fakeService) CreateProject(name, from string) (any, error) {
	return map[string]any{"id": "p2", "name": name, "from": from}, nil
}
func (f *fakeService) RenameProject(idOrName, name string) (any, error) {
	return map[string]any{"ok": true, "id": idOrName, "name": name}, nil
}
func (f *fakeService) DeleteProject(idOrName string) error { return nil }
func (f *fakeService) CloseProject() error                 { return nil }
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

func (f *fakeService) StartProxy() error                          { return nil }
func (f *fakeService) StopProxy() error                            { return nil }
func (f *fakeService) InstallCA() error                            { return nil }
func (f *fakeService) AdbTest(adbPath string) (string, error)      { return "ok", nil }
func (f *fakeService) AdbSetProxy(adbPath, serial string) (string, error) {
	return "已设置设备代理", nil
}
func (f *fakeService) AdbClearProxy(adbPath, serial string) (string, error) {
	return "已清除设备代理", nil
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
func (f *fakeService) ListFlowsByTag(tagID, scope string, start, end int64, limit, offset int, q string) (any, error) {
	f.mu.Lock()
	f.lastTagID, f.lastScope, f.lastStart, f.lastEnd, f.lastQ = tagID, scope, start, end, q
	f.mu.Unlock()
	return map[string]any{
		"flows": []any{}, "total": 0, "tag": tagID, "scope": scope,
		"start": start, "end": end, "limit": limit, "offset": offset, "q": q,
	}, nil
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

	// curl（GET，默认 powershell）
	code, m = doRequest(t, "GET", addr, token, "/flows/f1/curl", nil)
	if code != 200 || m["shell"] != "powershell" || svc.curlID != "f1" || svc.curlShell != "powershell" {
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

// rules 写入路径
func TestRulesEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	code, _ := doRequest(t, "POST", addr, token, "/rules",
		strings.NewReader(`{"action":"ignore","target":"host","value":"example.com"}`))
	if code != 200 {
		t.Fatalf("rules ignore 应 200，得 %d", code)
	}
	if svc.ignoredTarget != "host" || svc.ignoredValue != "example.com" {
		t.Fatalf("RuleIgnore 参数错误: %s %s", svc.ignoredTarget, svc.ignoredValue)
	}

	code, _ = doRequest(t, "POST", addr, token, "/rules/groups/g1/enabled",
		strings.NewReader(`{"enabled":false}`))
	if code != 200 {
		t.Fatalf("group enabled 应 200，得 %d", code)
	}
	if svc.groupID != "g1" || svc.groupEnabled != false {
		t.Fatalf("RuleGroupSetEnabled 参数错误: %s %v", svc.groupID, svc.groupEnabled)
	}

	code, _ = doRequest(t, "POST", addr, token, "/rules",
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
	code, m := doRequest(t, "PUT", addr, token, "/settings", strings.NewReader(`{"maxFlows":3000}`))
	if code != 200 {
		t.Fatalf("PUT settings 应 200，得 %d", code)
	}
	if !strings.Contains(string(svc.savedSettings), "3000") {
		t.Fatalf("SaveSettings 未收到新配置: %s", svc.savedSettings)
	}
	warns, _ := m["warnings"].([]any)
	if len(warns) != 1 {
		t.Fatalf("warnings 应透传 1 条，得 %v", m["warnings"])
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
	// rules ignore host
	if code := RunCLI(dir, []string{"rules", "ignore", "host", "cli.example.com"}); code != 0 {
		t.Fatalf("cli rules ignore 退出码应 0，得 %d", code)
	}
	if svc.ignoredValue != "cli.example.com" {
		t.Fatalf("ignore value 应为 cli.example.com，得 %q", svc.ignoredValue)
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
