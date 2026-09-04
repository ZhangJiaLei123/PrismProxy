package rules

import "testing"

func fg(mode string, hosts, paths, procs []string) FilterGroup {
	return FilterGroup{Name: "g", Enabled: true, Mode: mode, Hosts: hosts, Paths: paths, Processes: procs}
}

func mustEngine(t *testing.T, groups []FilterGroup, dec []DecryptRule, gmap map[string][]string) *Engine {
	t.Helper()
	e, err := NewEngine(groups, dec, gmap)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// 无组 → 全显示（黑名单默认语义）；nil Engine 同样默认
func TestDefaultsAllDisplay(t *testing.T) {
	e := mustEngine(t, nil, nil, nil)
	if !e.ShouldDisplay("example.com", "https://example.com/", "") {
		t.Fatal("无组应全显示")
	}
	if !e.ShouldDecrypt("example.com") {
		t.Fatal("解密默认应为 MITM")
	}
	var ne *Engine
	if !ne.ShouldDisplay("a", "https://a/x", "p") || !ne.ShouldDecrypt("a") {
		t.Fatal("nil Engine 应返回默认动作")
	}
}

// 单黑名单组：域名（裸域/子域/@组名）、路径、进程
func TestBlacklistBasics(t *testing.T) {
	gmap := map[string][]string{"ai": {"trae.cn", "doubao.com"}}
	e := mustEngine(t, []FilterGroup{
		fg(ModeBlacklist, []string{"example.com", "@ai"}, nil, nil),
	}, nil, gmap)
	for _, h := range []string{"example.com", "api.example.com", "EXAMPLE.com", "api.trae.cn", "doubao.com"} {
		if e.ShouldDisplay(h, "https://"+h+"/x", "") {
			t.Fatalf("%s 应命中黑名单", h)
		}
	}
	for _, h := range []string{"notexample.com", "example.com.evil.com", "other.com"} {
		if !e.ShouldDisplay(h, "https://"+h+"/", "") {
			t.Fatalf("%s 不应命中黑名单", h)
		}
	}
	// *. 前缀等价裸域名
	e2 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, []string{"*.example.com"}, nil, nil)}, nil, nil)
	if e2.ShouldDisplay("example.com", "https://example.com/", "") || e2.ShouldDisplay("a.example.com", "https://a.example.com/", "") {
		t.Fatal("*.example.com 应等价裸域名")
	}
	// 进程：精确匹配不区分大小写
	e3 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, nil, nil, []string{"DNPlayer.exe"})}, nil, nil)
	if e3.ShouldDisplay("x.com", "https://x.com/", "dnplayer.exe") {
		t.Fatal("进程名匹配应不区分大小写")
	}
	if !e3.ShouldDisplay("x.com", "https://x.com/", "other.exe") {
		t.Fatal("进程不命中应显示")
	}
	// 引用不存在的组：展开为空 → 组不匹配 → 显示
	e4 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, []string{"@nope"}, nil, nil)}, nil, gmap)
	if !e4.ShouldDisplay("anything.com", "https://anything.com/", "") {
		t.Fatal("不存在的 @组 引用应不命中")
	}
}

// 路径 glob：前缀、*、?、段边界
func TestPathMatching(t *testing.T) {
	e := mustEngine(t, []FilterGroup{
		fg(ModeBlacklist, nil, []string{"/api/v1/*", "/telemetry", "*/heartbeat", "/quo?e"}, nil),
	}, nil, nil)
	hide := []string{
		"https://x.com/api/v1/user/info", "https://x.com/api/v1/",
		"https://x.com/telemetry", "https://x.com/telemetry/upload",
		"https://x.com/v1/heartbeat", "https://x.com/quote", "https://x.com/quore",
	}
	for _, u := range hide {
		if e.ShouldDisplay("x.com", u, "") {
			t.Fatalf("%s 应命中黑名单路径", u)
		}
	}
	show := []string{
		"https://x.com/api/v2/x", "https://x.com/api/v1x",
		"https://x.com/telemetryx", // 段边界：无通配符条目不误伤
		"https://x.com/api/telemetry2",
		"https://x.com/heartbeatx", "https://x.com/quo",
	}
	for _, u := range show {
		if !e.ShouldDisplay("x.com", u, "") {
			t.Fatalf("%s 不应命中黑名单路径", u)
		}
	}
	// query 不参与匹配：path=/api/v1/user 命中 glob
	if e.ShouldDisplay("x.com", "https://x.com/api/v1/user?debug=1", "") {
		t.Fatal("glob 应只匹配 path 不含 query")
	}
	// "/" 单独一条 = 匹配所有路径
	e2 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, nil, []string{"/"}, nil)}, nil, nil)
	if e2.ShouldDisplay("x.com", "https://x.com/anything", "") {
		t.Fatal("/ 应匹配所有路径")
	}
}

// 组内维度 AND、同维度多条目 OR、组间 OR
func TestGroupSemantics(t *testing.T) {
	// AND：host+path 需同时命中
	e := mustEngine(t, []FilterGroup{
		fg(ModeBlacklist, []string{"x.com"}, []string{"/api"}, nil),
	}, nil, nil)
	if !e.ShouldDisplay("x.com", "https://x.com/other", "") {
		t.Fatal("host 命中但 path 未命中 → 组不命中，应显示")
	}
	if !e.ShouldDisplay("y.com", "https://y.com/api", "") {
		t.Fatal("path 命中但 host 未命中 → 组不命中")
	}
	if e.ShouldDisplay("x.com", "https://x.com/api/v1", "") {
		t.Fatal("host+path 同时命中 → 组命中")
	}
	// 同维度多条目 OR
	e2 := mustEngine(t, []FilterGroup{
		fg(ModeBlacklist, []string{"a.com", "b.com"}, nil, nil),
	}, nil, nil)
	if e2.ShouldDisplay("a.com", "https://a.com/", "") || e2.ShouldDisplay("b.com", "https://b.com/", "") {
		t.Fatal("同维度多条目应 OR")
	}
	// 组间 OR：任一黑名单组命中即屏蔽
	e3 := mustEngine(t, []FilterGroup{
		fg(ModeBlacklist, []string{"a.com"}, nil, nil),
		fg(ModeBlacklist, []string{"b.com"}, nil, nil),
	}, nil, nil)
	if e3.ShouldDisplay("b.com", "https://b.com/", "") {
		t.Fatal("组间应 OR")
	}
}

// 单白名单组：只显示命中；未命中不显示；空组等价不存在
func TestWhitelistBasics(t *testing.T) {
	e := mustEngine(t, []FilterGroup{
		fg(ModeWhitelist, []string{"netease.com"}, nil, nil),
	}, nil, nil)
	if !e.ShouldDisplay("api.netease.com", "https://api.netease.com/", "") {
		t.Fatal("白名单命中应显示")
	}
	if e.ShouldDisplay("other.com", "https://other.com/", "") {
		t.Fatal("白名单未命中应不显示")
	}
	// 空组等价不存在（无启用中的非空白名单组 → 全显示）
	e2 := mustEngine(t, []FilterGroup{
		{Name: "empty", Enabled: true, Mode: ModeWhitelist},
	}, nil, nil)
	if !e2.ShouldDisplay("other.com", "https://other.com/", "") {
		t.Fatal("空白名单组应等价不存在")
	}
}

// 黑白并存：白名单收窄 + 黑名单 deny-override
func TestBlacklistOverridesWhitelist(t *testing.T) {
	e := mustEngine(t, []FilterGroup{
		fg(ModeWhitelist, []string{"netease.com"}, nil, nil),
		fg(ModeBlacklist, nil, []string{"/telemetry"}, nil),
	}, nil, nil)
	if !e.ShouldDisplay("api.netease.com", "https://api.netease.com/v1", "") {
		t.Fatal("白名单命中且未命中黑名单应显示")
	}
	if e.ShouldDisplay("api.netease.com", "https://api.netease.com/telemetry/x", "") {
		t.Fatal("黑名单命中应覆盖白名单")
	}
	if e.ShouldDisplay("other.com", "https://other.com/v1", "") {
		t.Fatal("白名单外的域名应不显示")
	}
	// enabled=false 整组跳过
	e2 := mustEngine(t, []FilterGroup{
		{Name: "w", Enabled: false, Mode: ModeWhitelist, Hosts: []string{"netease.com"}},
	}, nil, nil)
	if !e2.ShouldDisplay("other.com", "https://other.com/", "") {
		t.Fatal("停用的白名单组应不生效（恢复全显示）")
	}
}

// 维度移除（规则设计 §2.3）：procName 空 / path 空
func TestDimensionRemoval(t *testing.T) {
	// 纯进程黑名单组 × procName 空 → 组不命中 → 不误伤
	e := mustEngine(t, []FilterGroup{fg(ModeBlacklist, nil, nil, []string{"dnplayer.exe"})}, nil, nil)
	if !e.ShouldDisplay("x.com", "https://x.com/", "") {
		t.Fatal("未知进程流不应被纯进程黑名单组误伤")
	}
	// 纯进程白名单组 × procName 空 → 组不命中 → 隐藏
	e2 := mustEngine(t, []FilterGroup{fg(ModeWhitelist, nil, nil, []string{"dnplayer.exe"})}, nil, nil)
	if e2.ShouldDisplay("x.com", "https://x.com/", "") {
		t.Fatal("纯进程白名单组不应放行未知进程流")
	}
	if !e2.ShouldDisplay("x.com", "https://x.com/", "dnplayer.exe") {
		t.Fatal("进程命中白名单应显示")
	}
	// 纯 path 组 × 隧道流（rawURL 无 path）→ 组不匹配
	e3 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, nil, []string{"/api"}, nil)}, nil, nil)
	if !e3.ShouldDisplay("x.com", "https://x.com", "") {
		t.Fatal("隧道流不应被纯 path 组命中")
	}
	// hosts+paths 混合白名单组 × 隧道流 → 只剩 hosts 约束，hosts 命中即显示
	e4 := mustEngine(t, []FilterGroup{fg(ModeWhitelist, []string{"x.com"}, []string{"/api"}, nil)}, nil, nil)
	if !e4.ShouldDisplay("x.com", "https://x.com", "") {
		t.Fatal("隧道流只剩 hosts 约束，hosts 命中应显示")
	}
	if e4.ShouldDisplay("y.com", "https://y.com", "") {
		t.Fatal("hosts 未命中应不显示")
	}
	// hosts+processes 混合组 × procName 空 → 只剩 hosts
	e5 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, []string{"x.com"}, nil, []string{"dnplayer.exe"})}, nil, nil)
	if e5.ShouldDisplay("x.com", "https://x.com/", "") {
		t.Fatal("procName 空时 hosts 命中即组命中")
	}
	// 明文请求 procName 空 + host 未命中 → 不命中
	if !e5.ShouldDisplay("y.com", "https://y.com/", "") {
		t.Fatal("hosts 未命中应显示")
	}
	// url.Parse 失败的 rawURL → 空 path 走维度移除（非原始串匹配）
	e6 := mustEngine(t, []FilterGroup{fg(ModeBlacklist, nil, []string{"*"}, nil)}, nil, nil)
	if !e6.ShouldDisplay("x.com", "://bad-url/api", "") {
		t.Fatal("解析失败应走维度移除而非原始串匹配")
	}
}

// 解密层不动：首条命中 + @组名 引用
func TestDecryptUnchanged(t *testing.T) {
	e := mustEngine(t, nil, []DecryptRule{{Action: ActionBypass, Host: "example.com"}}, nil)
	for _, h := range []string{"example.com", "a.example.com", "deep.a.example.com"} {
		if e.ShouldDecrypt(h) {
			t.Fatalf("%s 应命中 bypass", h)
		}
	}
	if !e.ShouldDecrypt("other.com") {
		t.Fatal("未命中应默认 MITM")
	}
	gmap := map[string][]string{"ai": {"trae.cn"}}
	e2 := mustEngine(t, nil, []DecryptRule{{Action: ActionBypass, Host: "@ai"}}, gmap)
	if e2.ShouldDecrypt("api.trae.cn") {
		t.Fatal("解密规则 @组名 引用应命中")
	}
}

// 校验：非法 mode/空组名/裸 */空条目/解密规则
// （glob 经 QuoteMeta 构造，编译不会失败，compilePathEntry 的 error 为防御性路径）
func TestValidation(t *testing.T) {
	bad := []FilterGroup{
		fg("nope", nil, nil, nil),
		{Name: "", Enabled: true, Mode: ModeBlacklist},
		fg(ModeBlacklist, []string{"*"}, nil, nil),
		fg(ModeBlacklist, []string{""}, nil, nil),
		fg(ModeBlacklist, nil, nil, []string{""}),
	}
	for i, g := range bad {
		if _, err := NewEngine([]FilterGroup{g}, nil, nil); err == nil {
			t.Fatalf("bad[%d] 应报错: %+v", i, g)
		}
	}
	if _, err := NewEngine(nil, []DecryptRule{{Action: ActionBypass}}, nil); err == nil {
		t.Fatal("解密规则空 host 应报错")
	}
	if _, err := NewEngine(nil, []DecryptRule{{Action: ActionInclude, Host: "x.com"}}, nil); err == nil {
		t.Fatal("解密规则动作只允许 mitm|bypass")
	}
}

func TestHolderHotSwap(t *testing.T) {
	h := &Holder{}
	if !h.Get().ShouldDisplay("a.com", "https://a.com/", "") {
		t.Fatal("空 Holder 应走默认")
	}
	e, _ := NewEngine([]FilterGroup{fg(ModeBlacklist, []string{"a.com"}, nil, nil)}, nil, nil)
	h.Set(e)
	if h.Get().ShouldDisplay("a.com", "https://a.com/", "") {
		t.Fatal("热替换后黑名单应生效")
	}
}
