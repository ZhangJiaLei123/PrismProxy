package settings

import (
	"os"
	"path/filepath"
	"testing"

	"prismproxy/internal/rules"
)

func TestLoadGlobalMissingReturnsDefault(t *testing.T) {
	g, err := LoadGlobal(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if g.ListenAddr != "127.0.0.1:9090" || g.MaxFlows != 2000 || g.MaxBodyMB != 256 {
		t.Fatalf("默认值不对: %+v", g)
	}
	if len(g.BypassList) != len(BuiltinBypass) {
		t.Fatal("默认绕过列表应为内置列表")
	}
	if g.Projects == nil {
		t.Fatal("Projects 应初始化为空切片（不序列化出 null）")
	}
}

func TestSaveLoadGlobalRoundtrip(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGlobal()
	g.ListenAddr = "0.0.0.0:8888"
	g.UpstreamMode = UpstreamManual
	g.UpstreamProxy = "127.0.0.1:7890"
	g.MaxFlows = 500
	g.MaxBodyMB = 64
	g.BypassList = append(g.BypassList, "my.corp.com")
	g.Persist.DBPath = `C:\somewhere\custom.db` // 废弃字段，保存须强制清空
	g.Projects = []ProjectMeta{{ID: "1700000000000-1", Name: "默认项目"}}
	g.CurrentProject = "1700000000000-1"

	if err := g.SaveGlobal(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != g.ListenAddr || got.UpstreamMode != g.UpstreamMode ||
		got.UpstreamProxy != g.UpstreamProxy || got.MaxFlows != 500 || got.MaxBodyMB != 64 {
		t.Fatalf("往返后基础字段不一致: %+v", got)
	}
	if len(got.BypassList) != len(g.BypassList) {
		t.Fatal("绕过列表丢失")
	}
	if got.Persist.DBPath != "" {
		t.Fatalf("dbPath 已废弃，不应落盘/读出: %q", got.Persist.DBPath)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "1700000000000-1" || got.CurrentProject != "1700000000000-1" {
		t.Fatalf("项目清单/当前指针丢失: %+v", got.Projects)
	}
}

func TestLoadGlobalLegacyMissingFieldsFallback(t *testing.T) {
	dir := t.TempDir()
	// 手工截断成旧版最小 JSON
	minJSON := []byte(`{"listenAddr":"127.0.0.1:9999"}`)
	if err := os.WriteFile(filepath.Join(dir, fileName), minJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != "127.0.0.1:9999" {
		t.Fatal("旧版字段应保留")
	}
	if got.MaxFlows != 2000 || len(got.BypassList) != len(BuiltinBypass) {
		t.Fatal("缺失字段应以默认值兜底")
	}
}

// 旧 captureRules/processRules → filterGroups 迁移（规则设计 §六）
func TestMigrate(t *testing.T) {
	pc := DefaultProjectConfig("t1", "测试")
	pc.CaptureRules = []rules.CaptureRule{
		{Action: rules.ActionExclude, Host: "tracker.com"},
		{Action: rules.ActionExclude, Host: "ads.com", URLRe: "/telemetry/", Method: "POST"}, // urlRe/method 丢弃
		{Action: rules.ActionInclude, Host: "netease.com"},
		{Action: rules.ActionExclude, Host: "tracker.com"}, // 去重
	}
	pc.ProcessRules = []rules.ProcessRule{
		{Action: rules.ActionExclude, Name: "dnplayer.exe"},
	}
	pc.DecryptRules = []rules.DecryptRule{{Action: rules.ActionBypass, Host: "pin.example.com"}}

	if !pc.Migrate() {
		t.Fatal("应发生迁移")
	}
	if len(pc.CaptureRules) != 0 || len(pc.ProcessRules) != 0 {
		t.Fatal("迁移后旧字段应清空")
	}
	if len(pc.DecryptRules) != 1 {
		t.Fatal("decryptRules 不应改动")
	}
	if len(pc.FilterGroups) != 3 {
		t.Fatalf("应生成 3 个迁移组（捕获黑/白 + 进程黑）: %+v", pc.FilterGroups)
	}
	var cb, cw, pb *rules.FilterGroup
	for i := range pc.FilterGroups {
		g := &pc.FilterGroups[i]
		switch g.ID {
		case "_migrated_capture_black":
			cb = g
		case "_migrated_capture_white":
			cw = g
		case "_migrated_process_black":
			pb = g
		}
		if g.Name == "" || !g.Enabled {
			t.Fatalf("迁移组应命名且启用: %+v", g)
		}
	}
	if cb == nil || cw == nil || pb == nil {
		t.Fatal("迁移组 id/命名缺失")
	}
	if cb.Mode != rules.ModeBlacklist || len(cb.Hosts) != 2 {
		t.Fatalf("捕获黑名单组应含去重后的 2 个域名: %+v", cb)
	}
	if len(cb.Paths) != 0 {
		t.Fatal("urlRe 应丢弃而非转为 paths")
	}
	if cw.Mode != rules.ModeWhitelist || len(cw.Hosts) != 1 || cw.Hosts[0] != "netease.com" {
		t.Fatalf("捕获白名单组不对: %+v", cw)
	}
	if len(pb.Processes) != 1 || pb.Processes[0] != "dnplayer.exe" {
		t.Fatalf("进程黑名单组不对: %+v", pb)
	}
	// 迁移幂等：二次调用不再生成
	if pc.Migrate() {
		t.Fatal("二次 Migrate 应返回 false")
	}
	if len(pc.FilterGroups) != 3 {
		t.Fatal("迁移应幂等，不重复生成迁移组")
	}
}

// 迁移后落盘 → 二次 Load 不重复迁移（GUI 验收 #4 的存储层保障）
func TestMigratePersistedIdempotent(t *testing.T) {
	dir := t.TempDir()
	pc := DefaultProjectConfig("t1", "测试")
	pc.CaptureRules = []rules.CaptureRule{{Action: rules.ActionExclude, Host: "tracker.com"}}
	if err := SaveProjectConfig(dir, pc); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProjectConfig(dir, "t1", "测试")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Migrate() {
		t.Fatal("首轮应迁移")
	}
	if err := SaveProjectConfig(dir, got); err != nil {
		t.Fatal(err)
	}
	got2, err := LoadProjectConfig(dir, "t1", "测试")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Migrate() {
		t.Fatal("落盘后二次启动不应重复迁移")
	}
	if len(got2.FilterGroups) != 1 {
		t.Fatalf("迁移组应恰好一个: %+v", got2.FilterGroups)
	}
}

// M5 早期混合内置组 _quick_ignore（hosts+processes 同组）须拆分为两个独立组（组间 OR）
func TestMigrateSplitQuickIgnore(t *testing.T) {
	pc := DefaultProjectConfig("t1", "测试")
	pc.FilterGroups = append(pc.FilterGroups, rules.FilterGroup{
		ID: "_quick_ignore", Name: "快捷忽略", Enabled: true, Mode: rules.ModeBlacklist,
		Hosts: []string{"www.bilibili.com"}, Processes: []string{"curl.exe"},
	})
	if !pc.Migrate() {
		t.Fatal("应发生拆分迁移")
	}
	var hosts, procs *rules.FilterGroup
	for i := range pc.FilterGroups {
		switch pc.FilterGroups[i].ID {
		case "_quick_ignore":
			t.Fatal("旧混合组应已移除")
		case "_quick_ignore_hosts":
			hosts = &pc.FilterGroups[i]
		case "_quick_ignore_procs":
			procs = &pc.FilterGroups[i]
		}
	}
	if hosts == nil || procs == nil {
		t.Fatalf("应生成域名组与进程组: %+v", pc.FilterGroups)
	}
	if len(hosts.Hosts) != 1 || hosts.Hosts[0] != "www.bilibili.com" || len(hosts.Processes) != 0 {
		t.Fatalf("域名组内容异常: %+v", hosts)
	}
	if len(procs.Processes) != 1 || procs.Processes[0] != "curl.exe" || len(procs.Hosts) != 0 {
		t.Fatalf("进程组内容异常: %+v", procs)
	}
	// 幂等：二次调用不再变化
	if pc.Migrate() {
		t.Fatal("拆分迁移应幂等")
	}
}

func TestValidateEnv(t *testing.T) {
	assertErr := func(g *GlobalSettings, msg string) {
		t.Helper()
		if err := g.ValidateEnv(); err == nil {
			t.Fatal(msg)
		}
	}

	bad := DefaultGlobal()
	bad.ListenAddr = "not-an-addr"
	assertErr(bad, "非法监听地址应报错")

	bad = DefaultGlobal()
	bad.UpstreamMode = UpstreamManual
	assertErr(bad, "manual 模式空代理地址应报错")

	bad = DefaultGlobal()
	bad.UpstreamMode = "bogus"
	assertErr(bad, "非法上游模式应报错")

	good := DefaultGlobal()
	good.UpstreamMode = UpstreamManual
	good.UpstreamProxy = "127.0.0.1:7890"
	if err := good.ValidateEnv(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	gmap := map[string][]string{"ai": {"trae.cn"}}
	assertErr := func(fg []rules.FilterGroup, msg string) {
		t.Helper()
		if err, _ := ValidateRules(fg, nil, gmap); err == nil {
			t.Fatal(msg)
		}
	}

	assertErr([]rules.FilterGroup{{Name: "g", Enabled: true, Mode: "nope"}}, "非法 mode 应报错")
	assertErr([]rules.FilterGroup{{Name: "", Enabled: true, Mode: rules.ModeBlacklist}}, "空组名应报错")
	assertErr([]rules.FilterGroup{{Name: "g", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"*"}}}, "裸 * host 应报错")

	// 未知 @引用 → warning 不阻塞
	err, warns := ValidateRules([]rules.FilterGroup{{Name: "g", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"@nope", "@ai"}}}, nil, gmap)
	if err != nil {
		t.Fatalf("未知 @引用 不应阻塞保存: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("应产生 1 条 warning（仅 @nope）: %v", warns)
	}

	if err, _ := ValidateRules([]rules.FilterGroup{{Name: "g", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"@ai"}}}, nil, gmap); err != nil {
		t.Fatalf("合法规则不应报错: %v", err)
	}
}
