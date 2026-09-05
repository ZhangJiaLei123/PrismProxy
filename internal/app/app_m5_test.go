package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prismproxy/internal/capture"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/store"
)

// newTestApp 构造内存态 App（临时配置目录 + store，不走 startup/代理）
func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg := settings.Default()
	a := &App{
		cfg:    cfg,
		cfgDir: t.TempDir(),
		eng:    &rules.Holder{},
		st:     store.New(100),
		pendUp: map[string]FlowMeta{},
	}
	e, err := rules.NewEngine(cfg.FilterGroups, cfg.DecryptRules, nil)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	a.eng.Set(e)
	a.rec = capture.NewRecorder(a.st) // M6 调试重发借用 Recorder 的 ID 分配
	return a
}

func testFlow(id string) *capture.Flow {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("Authorization", "Bearer tok")
	return &capture.Flow{
		ID:         id,
		State:      capture.StateDone,
		Scheme:     "https",
		ServerAddr: "api.example.com:443",
		Request: &capture.Message{
			Method: "POST",
			URL:    "https://api.example.com/v1/order?a=1",
			Proto:  "HTTP/1.1",
			Header: h,
			Body:   []byte(`{"name":"test"}`),
		},
		Response: &capture.Message{
			Proto:      "HTTP/1.1",
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"ok":true}`),
		},
	}
}

func TestAddQuickIgnore_Host(t *testing.T) {
	a := newTestApp(t)
	added, err := a.AddQuickIgnore("host", "API.Example.COM:443")
	if err != nil || !added {
		t.Fatalf("首次添加应成功且新增: added=%v err=%v", added, err)
	}
	// 幂等：归一化后重复添加返回 false
	added, err = a.AddQuickIgnore("host", "api.example.com")
	if err != nil || added {
		t.Fatalf("重复添加应幂等: added=%v err=%v", added, err)
	}
	var g *rules.FilterGroup
	for i := range a.cfg.FilterGroups {
		if a.cfg.FilterGroups[i].ID == QuickIgnoreHostGroupID {
			g = &a.cfg.FilterGroups[i]
		}
	}
	if g == nil {
		t.Fatal("快捷忽略-域名组未创建")
	}
	if g.Mode != rules.ModeBlacklist || !g.Enabled {
		t.Fatalf("组应为启用的黑名单: mode=%s enabled=%v", g.Mode, g.Enabled)
	}
	if len(g.Hosts) != 1 || g.Hosts[0] != "api.example.com" {
		t.Fatalf("host 归一化/去重异常: %#v", g.Hosts)
	}
	// 落盘
	if data, err := os.ReadFile(filepath.Join(a.cfgDir, "settings.json")); err != nil || !strings.Contains(string(data), QuickIgnoreHostGroupID) {
		t.Fatalf("settings.json 应含快捷忽略组: err=%v", err)
	}
	// 引擎热生效：该域后续流量不显示
	if a.eng.Get().ShouldDisplay("sub.api.example.com", "https://sub.api.example.com/x", "") {
		t.Fatal("黑名单组应已生效（子域命中不显示）")
	}
}

// TestAddQuickIgnore_HostAndProcessIndependent 域名忽略与进程忽略须相互独立（组间 OR），
// 不能因混放同一组（组内 AND）而互相收窄——回归 M5 早期 _quick_ignore 混合组缺陷。
func TestAddQuickIgnore_HostAndProcessIndependent(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.AddQuickIgnore("host", "blocked.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddQuickIgnore("process", "curl.exe"); err != nil {
		t.Fatal(err)
	}
	eng := a.eng.Get()
	// 忽略的域名：无论什么进程都过滤
	if eng.ShouldDisplay("blocked.com", "https://blocked.com/", "firefox.exe") {
		t.Fatal("忽略域名应在任意进程下都过滤")
	}
	// 忽略的进程：访问任意（未忽略）域名也过滤
	if eng.ShouldDisplay("other.com", "https://other.com/", "curl.exe") {
		t.Fatal("忽略进程应在任意域名下都过滤")
	}
	// 既非忽略域名也非忽略进程：正常显示
	if !eng.ShouldDisplay("other.com", "https://other.com/", "firefox.exe") {
		t.Fatal("未命中任何忽略项的流量应显示")
	}
}

func TestAddQuickIgnore_Process(t *testing.T) {
	a := newTestApp(t)
	added, err := a.AddQuickIgnore("process", "chrome.exe")
	if err != nil || !added {
		t.Fatalf("首次添加: added=%v err=%v", added, err)
	}
	// 进程名不区分大小写去重
	added, _ = a.AddQuickIgnore("process", "CHROME.EXE")
	if added {
		t.Fatal("进程名应大小写不敏感幂等")
	}
	if !a.eng.Get().ShouldDisplay("any.com", "https://any.com/", "firefox.exe") {
		t.Fatal("其他进程不应被忽略")
	}
	if a.eng.Get().ShouldDisplay("any.com", "https://any.com/", "chrome.exe") {
		t.Fatal("chrome.exe 应被黑名单忽略")
	}
	// 异常 target / 空值
	if _, err := a.AddQuickIgnore("xxx", "v"); err == nil {
		t.Fatal("非法 target 应报错")
	}
	if _, err := a.AddQuickIgnore("host", "  "); err == nil {
		t.Fatal("空 host 应报错")
	}
}

func TestImportRules_FileReplaceAndValidate(t *testing.T) {
	a := newTestApp(t)
	// 现有一条规则，导入后应被整体替换
	a.cfg.FilterGroups = append(a.cfg.FilterGroups, rules.FilterGroup{
		ID: "old", Name: "旧组", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"old.com"},
	})

	doc := rulesFile{
		Version: 1,
		FilterGroups: []rules.FilterGroup{
			{ID: "g1", Name: "导入黑名单", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"blocked.example"}},
		},
		DecryptRules: []rules.DecryptRule{{Action: rules.ActionBypass, Host: "pin.example"}},
		BypassList:   []string{"<-loopback>", "bypass.example"},
	}
	data, _ := json.Marshal(doc)
	path := filepath.Join(a.cfgDir, "rules.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := a.ImportRules(path)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if res == nil {
		t.Fatal("用户取消以外应返回结果")
	}
	if len(a.cfg.FilterGroups) != 1 || a.cfg.FilterGroups[0].ID != "g1" {
		t.Fatalf("规则应整体替换: %#v", a.cfg.FilterGroups)
	}
	if len(a.cfg.DecryptRules) != 1 || a.cfg.DecryptRules[0].Host != "pin.example" {
		t.Fatalf("解密规则应替换: %#v", a.cfg.DecryptRules)
	}
	if len(a.cfg.BypassList) != 2 || a.cfg.BypassList[0] != "<-loopback>" {
		t.Fatalf("绕过列表应替换: %#v", a.cfg.BypassList)
	}
	if a.eng.Get().ShouldDisplay("blocked.example", "https://blocked.example/", "") {
		t.Fatal("导入黑名单应热生效")
	}
	if a.eng.Get().ShouldDecrypt("pin.example") {
		t.Fatal("导入 bypass 解密规则应热生效")
	}
	// 监听地址等非规则字段沿用
	if a.cfg.ListenAddr != "127.0.0.1:9090" {
		t.Fatalf("非规则字段不应被覆盖: %s", a.cfg.ListenAddr)
	}

	// 非法 JSON / 版本不符
	_ = os.WriteFile(filepath.Join(a.cfgDir, "bad.json"), []byte("{not json"), 0o644)
	if _, err := a.ImportRules(filepath.Join(a.cfgDir, "bad.json")); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	badVer := rulesFile{Version: 99}
	d2, _ := json.Marshal(badVer)
	_ = os.WriteFile(filepath.Join(a.cfgDir, "v99.json"), d2, 0o644)
	if _, err := a.ImportRules(filepath.Join(a.cfgDir, "v99.json")); err == nil {
		t.Fatal("不支持的版本应报错")
	}
}

func TestImportRules_EmbeddedGroupsAndWarnings(t *testing.T) {
	a := newTestApp(t)
	// 引用 @adgroup，导入文件内嵌该组清单 → 自动补建且无 warning
	doc := rulesFile{
		Version: 1,
		FilterGroups: []rules.FilterGroup{
			{ID: "g", Name: "引用组", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"@adgroup"}},
		},
		Groups: map[string][]string{"adgroup": {"ads.example", "track.example"}},
	}
	data, _ := json.Marshal(doc)
	path := filepath.Join(a.cfgDir, "emb.json")
	_ = os.WriteFile(path, data, 0o644)
	res, err := a.ImportRules(path)
	if err != nil {
		t.Fatalf("内嵌组导入失败: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("内嵌组补建后不应有缺组 warning: %v", res.Warnings)
	}
	if a.groups == nil || len(a.groups.Domains["adgroup"]) != 2 {
		t.Fatalf("内嵌域名组应补建: %#v", a.groups)
	}
	if a.eng.Get().ShouldDisplay("ads.example", "https://ads.example/", "") {
		t.Fatal("补建组的 @引用应命中")
	}

	// 缺组场景：引用不存在的 @ghost → warning 但不阻塞
	doc2 := rulesFile{
		Version:      1,
		FilterGroups: []rules.FilterGroup{{ID: "g2", Name: "缺组", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"@ghost"}}},
	}
	d2, _ := json.Marshal(doc2)
	p2 := filepath.Join(a.cfgDir, "ghost.json")
	_ = os.WriteFile(p2, d2, 0o644)
	res2, err := a.ImportRules(p2)
	if err != nil {
		t.Fatalf("缺组不应阻塞: %v", err)
	}
	if len(res2.Warnings) == 0 || !strings.Contains(res2.Warnings[0], "@ghost") {
		t.Fatalf("应返回缺组 warning: %v", res2.Warnings)
	}
}

func TestImportRules_URL(t *testing.T) {
	doc := rulesFile{
		Version:      1,
		FilterGroups: []rules.FilterGroup{{ID: "u1", Name: "URL组", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"urlblocked.example"}}},
	}
	data, _ := json.Marshal(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	a := newTestApp(t)
	res, err := a.ImportRules(srv.URL)
	if err != nil {
		t.Fatalf("URL 导入失败: %v", err)
	}
	if res == nil || len(a.cfg.FilterGroups) != 1 || a.cfg.FilterGroups[0].ID != "u1" {
		t.Fatalf("URL 导入内容异常: %#v", a.cfg.FilterGroups)
	}
	if a.eng.Get().ShouldDisplay("urlblocked.example", "https://urlblocked.example/", "") {
		t.Fatal("URL 导入规则应热生效")
	}
}

func TestGetFlowRawText(t *testing.T) {
	a := newTestApp(t)
	f := testFlow("t1")
	// gzip 响应体：复制 body 应为解压后文本
	var zb bytes.Buffer
	gw := gzip.NewWriter(&zb)
	gw.Write([]byte(`{"gzip":true}`))
	gw.Close()
	f.Response.ContentEncoding = "gzip"
	f.Response.Body = zb.Bytes()
	a.st.Add(f)

	hdr, err := a.GetFlowRawText("t1", "req", "headers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hdr, "POST /v1/order?a=1 HTTP/1.1\r\n") {
		t.Fatalf("请求行异常: %q", hdr)
	}
	if !strings.Contains(hdr, "Accept: application/json\r\n") || !strings.Contains(hdr, "Authorization: Bearer tok\r\n") {
		t.Fatalf("首部缺失: %q", hdr)
	}
	// headers 不含 body
	if strings.Contains(hdr, "test") {
		t.Fatalf("headers 不应含 body: %q", hdr)
	}

	rhdr, err := a.GetFlowRawText("t1", "resp", "headers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rhdr, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("状态行异常: %q", rhdr)
	}

	body, err := a.GetFlowRawText("t1", "resp", "body")
	if err != nil {
		t.Fatal(err)
	}
	if body != `{"gzip":true}` {
		t.Fatalf("body 应解压后复制: %q", body)
	}

	all, err := a.GetFlowRawText("t1", "req", "all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "\r\n\r\n") || !strings.HasSuffix(all, `{"name":"test"}`) {
		t.Fatalf("完整报文应含起始行+首部+空行+body: %q", all)
	}

	// 参数与状态边界
	if _, err := a.GetFlowRawText("t1", "xxx", "all"); err == nil {
		t.Fatal("非法 part 应报错")
	}
	if _, err := a.GetFlowRawText("t1", "req", "xxx"); err == nil {
		t.Fatal("非法 kind 应报错")
	}
	if _, err := a.GetFlowRawText("nope", "req", "all"); err == nil {
		t.Fatal("不存在的流应报错")
	}
	f2 := &capture.Flow{ID: "t2", State: capture.StateDone, ServerAddr: "x.com"}
	a.st.Add(f2)
	if _, err := a.GetFlowRawText("t2", "req", "all"); err == nil {
		t.Fatal("无请求的流应报错")
	}
}

func TestSetFlowPinnedBinding(t *testing.T) {
	a := newTestApp(t)
	a.st.Add(testFlow("p1"))
	if err := a.SetFlowPinned("p1", true); err != nil {
		t.Fatal(err)
	}
	got, ok := a.st.Get("p1")
	if !ok || !got.Pinned {
		t.Fatal("置顶应生效")
	}
	if err := a.SetFlowPinned("nope", true); err == nil {
		t.Fatal("不存在的流应报错")
	}
}

func TestBuildCurlBinding(t *testing.T) {
	a := newTestApp(t)
	a.st.Add(testFlow("c1"))
	res, err := a.BuildCurl("c1", capture.ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Command, "curl.exe") || !strings.Contains(res.Command, "--data-raw") {
		t.Fatalf("cmd cURL 异常: %q", res.Command)
	}
	bash, err := a.BuildCurl("c1", capture.ShellBash)
	if err != nil {
		t.Fatalf("bash cURL 错误: %v", err)
	}
	if !strings.Contains(bash.Command, "'curl'") {
		t.Fatalf("bash cURL 异常: %q", bash.Command)
	}
	if _, err := a.BuildCurl("nope", "cmd"); err == nil {
		t.Fatal("不存在的流应报错")
	}
}
