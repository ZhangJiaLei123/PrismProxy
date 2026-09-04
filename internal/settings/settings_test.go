package settings

import (
	"os"
	"path/filepath"
	"testing"

	"prismproxy/internal/rules"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ListenAddr != "127.0.0.1:9090" || s.MaxFlows != 2000 || s.MaxBodyMB != 256 {
		t.Fatalf("默认值不对: %+v", s)
	}
	if len(s.BypassList) != len(BuiltinBypass) {
		t.Fatal("默认绕过列表应为内置列表")
	}
	if s.FilterGroups == nil {
		t.Fatal("FilterGroups 应初始化为空切片（不序列化出 null）")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	s.ListenAddr = "0.0.0.0:8888"
	s.UpstreamMode = UpstreamManual
	s.UpstreamProxy = "127.0.0.1:7890"
	s.MaxFlows = 500
	s.MaxBodyMB = 64
	s.BypassList = append(s.BypassList, "my.corp.com")
	s.FilterGroups = []rules.FilterGroup{{
		ID: "1", Name: "只抓网易", Enabled: true, Mode: rules.ModeWhitelist,
		Hosts: []string{"netease.com", "@netease"}, Paths: []string{"/api/*"}, Processes: []string{"dnplayer.exe"},
	}}
	s.DecryptRules = []rules.DecryptRule{{Action: rules.ActionBypass, Host: "pin.example.com"}}

	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != s.ListenAddr || got.UpstreamMode != s.UpstreamMode ||
		got.UpstreamProxy != s.UpstreamProxy || got.MaxFlows != 500 || got.MaxBodyMB != 64 {
		t.Fatalf("往返后基础字段不一致: %+v", got)
	}
	if len(got.BypassList) != len(s.BypassList) {
		t.Fatal("绕过列表丢失")
	}
	if len(got.FilterGroups) != 1 || got.FilterGroups[0].Name != "只抓网易" ||
		got.FilterGroups[0].Mode != rules.ModeWhitelist || len(got.FilterGroups[0].Paths) != 1 {
		t.Fatalf("过滤规则组丢失: %+v", got.FilterGroups)
	}
	if len(got.DecryptRules) != 1 || got.DecryptRules[0].Host != "pin.example.com" {
		t.Fatalf("解密规则丢失: %+v", got.DecryptRules)
	}
}

func TestLoadLegacyMissingFieldsFallback(t *testing.T) {
	dir := t.TempDir()
	if err := Default().Save(dir); err != nil {
		t.Fatal(err)
	}
	// 手工截断成旧版最小 JSON
	minJSON := []byte(`{"listenAddr":"127.0.0.1:9999"}`)
	if err := os.WriteFile(filepath.Join(dir, fileName), minJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
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
	s := Default()
	s.CaptureRules = []rules.CaptureRule{
		{Action: rules.ActionExclude, Host: "tracker.com"},
		{Action: rules.ActionExclude, Host: "ads.com", URLRe: "/telemetry/", Method: "POST"}, // urlRe/method 丢弃
		{Action: rules.ActionInclude, Host: "netease.com"},
		{Action: rules.ActionExclude, Host: "tracker.com"}, // 去重
	}
	s.ProcessRules = []rules.ProcessRule{
		{Action: rules.ActionExclude, Name: "dnplayer.exe"},
	}
	s.DecryptRules = []rules.DecryptRule{{Action: rules.ActionBypass, Host: "pin.example.com"}}

	if !s.Migrate() {
		t.Fatal("应发生迁移")
	}
	if len(s.CaptureRules) != 0 || len(s.ProcessRules) != 0 {
		t.Fatal("迁移后旧字段应清空")
	}
	if len(s.DecryptRules) != 1 {
		t.Fatal("decryptRules 不应改动")
	}
	if len(s.FilterGroups) != 3 {
		t.Fatalf("应生成 3 个迁移组（捕获黑/白 + 进程黑）: %+v", s.FilterGroups)
	}
	var cb, cw, pb *rules.FilterGroup
	for i := range s.FilterGroups {
		g := &s.FilterGroups[i]
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
	if s.Migrate() {
		t.Fatal("二次 Migrate 应返回 false")
	}
	if len(s.FilterGroups) != 3 {
		t.Fatal("迁移应幂等，不重复生成迁移组")
	}
}

// 迁移后落盘 → 二次 Load 不重复迁移（GUI 验收 #4 的存储层保障）
func TestMigratePersistedIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	s.CaptureRules = []rules.CaptureRule{{Action: rules.ActionExclude, Host: "tracker.com"}}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Migrate() {
		t.Fatal("首轮应迁移")
	}
	if err := got.Save(dir); err != nil {
		t.Fatal(err)
	}
	got2, err := Load(dir)
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

func TestValidate(t *testing.T) {
	gmap := map[string][]string{"ai": {"trae.cn"}}
	assertErr := func(s *Settings, msg string) {
		t.Helper()
		if err, _ := s.Validate(gmap); err == nil {
			t.Fatal(msg)
		}
	}

	bad := Default()
	bad.ListenAddr = "not-an-addr"
	assertErr(bad, "非法监听地址应报错")

	bad = Default()
	bad.UpstreamMode = UpstreamManual
	assertErr(bad, "manual 模式空代理地址应报错")

	bad = Default()
	bad.UpstreamMode = "bogus"
	assertErr(bad, "非法上游模式应报错")

	bad = Default()
	bad.FilterGroups = []rules.FilterGroup{{Name: "g", Enabled: true, Mode: "nope"}}
	assertErr(bad, "非法 mode 应报错")

	bad = Default()
	bad.FilterGroups = []rules.FilterGroup{{Name: "", Enabled: true, Mode: rules.ModeBlacklist}}
	assertErr(bad, "空组名应报错")

	bad = Default()
	bad.FilterGroups = []rules.FilterGroup{{Name: "g", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"*"}}}
	assertErr(bad, "裸 * host 应报错")

	// 未知 @引用 → warning 不阻塞
	warn := Default()
	warn.FilterGroups = []rules.FilterGroup{{Name: "g", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"@nope", "@ai"}}}
	err, warns := warn.Validate(gmap)
	if err != nil {
		t.Fatalf("未知 @引用 不应阻塞保存: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("应产生 1 条 warning（仅 @nope）: %v", warns)
	}

	good := Default()
	good.UpstreamMode = UpstreamManual
	good.UpstreamProxy = "127.0.0.1:7890"
	if err, _ := good.Validate(gmap); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
}
