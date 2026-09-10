package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// ---------- AI 配置（M13 P0，计划 §二） ----------

func TestAIConfigDefaults(t *testing.T) {
	g := DefaultGlobal()
	if g.AI.Enabled {
		t.Fatal("AI 默认应未启用")
	}
	if g.AI.Provider != "custom" || g.AI.Temperature != 0.3 || g.AI.TimeoutSec != 120 ||
		g.AI.MaxFlows != 50 || g.AI.MaxKB != 64 || !g.AI.Redact {
		t.Fatalf("DefaultGlobal AI 默认子值异常: %+v", g.AI)
	}
}

func TestAIConfigLegacyJSONWithDefaults(t *testing.T) {
	// 旧 settings.json 无 ai 字段：反序列化零值不报错
	var legacy GlobalSettings
	if err := json.Unmarshal([]byte(`{"listenAddr":"127.0.0.1:9090","maxFlows":2000}`), &legacy); err != nil {
		t.Fatalf("旧配置反序列化不应报错: %v", err)
	}
	if legacy.AI.Enabled || legacy.AI.Redact || legacy.AI.MaxKB != 0 || legacy.AI.TimeoutSec != 0 {
		t.Fatalf("旧配置 AI 应为全零值: %+v", legacy.AI)
	}
	// WithDefaults 兜底：脱敏默认开 + 四项数值默认（高危 H2：防 Redact=false 隐私倒退）
	d := legacy.AI.WithDefaults()
	if !d.Redact || d.Temperature != 0.3 || d.TimeoutSec != 120 || d.MaxFlows != 50 || d.MaxKB != 64 {
		t.Fatalf("整体未初始化兜底异常: %+v", d)
	}
	// 兜底不回写原值（仅读取侧）
	if legacy.AI.Redact || legacy.AI.MaxKB != 0 {
		t.Fatalf("WithDefaults 不应修改接收者: %+v", legacy.AI)
	}

	// 部分初始化：各数值字段独立兜底，显式值不动
	p := AIConfig{Provider: "deepseek", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"}
	p = p.WithDefaults()
	if p.Temperature != 0.3 || p.TimeoutSec != 120 || p.MaxFlows != 50 || p.MaxKB != 64 {
		t.Fatalf("部分初始化兜底异常: %+v", p)
	}
	if p.Provider != "deepseek" || p.BaseURL != "https://api.deepseek.com" || p.Model != "deepseek-chat" {
		t.Fatalf("兜底不应覆盖显式字段: %+v", p)
	}
	// Redact 无哨兵可辨（bool 零值=false）：仅整体未初始化分支兜底 true，
	// 部分初始化保持原值（防覆盖用户显式关闭的脱敏，见 WithDefaults 注释）
	if p.Redact {
		t.Fatalf("部分初始化不应改写 Redact: %+v", p)
	}
	e := AIConfig{Provider: "custom", Temperature: 0.7, TimeoutSec: 300, MaxFlows: 10, MaxKB: 32}
	e = e.WithDefaults()
	if e.Temperature != 0.7 || e.TimeoutSec != 300 || e.MaxFlows != 10 || e.MaxKB != 32 {
		t.Fatalf("显式数值不应被兜底覆盖: %+v", e)
	}
	// 手工配置 {"enabled":true} 引导走默认参数且保留启用位
	m := AIConfig{Enabled: true, BaseURL: "http://127.0.0.1:11434"}
	m = m.WithDefaults()
	if !m.Enabled || m.Provider != "custom" || m.Temperature != 0.3 || !m.Redact {
		t.Fatalf("手工半配置兜底异常: %+v", m)
	}
}

func TestAIConfigValidate(t *testing.T) {
	base := AIConfig{Enabled: true, BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat",
		Temperature: 0.3, TimeoutSec: 120, MaxFlows: 50, MaxKB: 64, Redact: true}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}

	assertErr := func(c AIConfig, msg string) {
		t.Helper()
		if err := c.Validate(); err == nil {
			t.Fatal(msg)
		}
	}
	bad := base
	bad.BaseURL = "ftp://api.deepseek.com"
	assertErr(bad, "非 http(s) BaseURL 应报错")
	bad = base
	bad.BaseURL = "api.deepseek.com"
	assertErr(bad, "无 scheme BaseURL 应报错")
	bad = base
	bad.Model = "  "
	assertErr(bad, "空模型名应报错")
	bad = base
	bad.Temperature = 0
	assertErr(bad, "温度 0 为哨兵不显式保存，应报错")
	bad = base
	bad.Temperature = 2.1
	assertErr(bad, "温度越上界应报错")
	bad = base
	bad.TimeoutSec = 9
	assertErr(bad, "超时越下界应报错")
	bad = base
	bad.TimeoutSec = 601
	assertErr(bad, "超时越上界应报错")
	bad = base
	bad.MaxFlows = 0
	assertErr(bad, "流数越下界应报错")
	bad = base
	bad.MaxFlows = 101
	assertErr(bad, "流数越上界应报错")
	bad = base
	bad.MaxKB = 7
	assertErr(bad, "预算越下界应报错")
	bad = base
	bad.MaxKB = 257
	assertErr(bad, "预算越上界应报错")

	// 未启用不校验结构（配置半途也能保存）
	if err := (AIConfig{}).Validate(); err != nil {
		t.Fatalf("Enabled=false 不应校验: %v", err)
	}
}

func TestNormalizeAIBaseURL(t *testing.T) {
	cases := [][2]string{
		{"https://api.deepseek.com", "https://api.deepseek.com/v1"},
		{"https://api.deepseek.com/", "https://api.deepseek.com/v1"},
		{"https://api.deepseek.com//", "https://api.deepseek.com/v1"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/v1"},
		{"https://api.deepseek.com/v1/", "https://api.deepseek.com/v1"},
		{"http://127.0.0.1:11434", "http://127.0.0.1:11434/v1"},
		{"  https://x.com  ", "https://x.com/v1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeAIBaseURL(c[0]); got != c[1] {
			t.Fatalf("NormalizeAIBaseURL(%q)=%q, want %q", c[0], got, c[1])
		}
	}
}

func TestAISettingsProjectionNoKey(t *testing.T) {
	c := AIConfig{Enabled: true, Provider: "deepseek", BaseURL: "https://api.deepseek.com",
		APIKey: "sk-super-secret", Model: "deepseek-chat", Temperature: 0.3,
		TimeoutSec: 120, MaxFlows: 50, MaxKB: 64, Redact: true}
	s := c.AISettings()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// 投影 JSON 不得含 apiKey 字段或原值（防深拷贝/日志/DTO 链路泄漏）
	if strings.Contains(string(data), "apiKey") || strings.Contains(string(data), "sk-super-secret") {
		t.Fatalf("AISettings 投影泄漏密钥: %s", data)
	}
	if s.Model != "deepseek-chat" || !s.Redact || s.Temperature != 0.3 {
		t.Fatalf("投影字段不一致: %+v", s)
	}
}

func TestWarnNoKey(t *testing.T) {
	g := DefaultGlobal()
	if g.WarnNoKey() != "" {
		t.Fatal("未启用不应产生 warning")
	}
	g.AI.Enabled = true
	if g.WarnNoKey() == "" {
		t.Fatal("启用但无 key 应产生 warning")
	}
	g.AI.Provider = AIProviderOllama
	if g.WarnNoKey() != "" {
		t.Fatal("ollama 无 key 属正常形态不应 warning")
	}
	g.AI.Provider = "custom"
	g.AI.APIKey = "sk-x"
	if g.WarnNoKey() != "" {
		t.Fatal("已配置 key 不应 warning")
	}
}

func TestValidateEnvAI(t *testing.T) {
	g := DefaultGlobal()
	g.AI.Enabled = true
	g.AI.BaseURL = "not a url"
	if err := g.ValidateEnv(); err == nil {
		t.Fatal("ValidateEnv 应转发 AI 校验（非法 BaseURL）")
	}
	g.AI.BaseURL = "https://api.deepseek.com"
	g.AI.Model = "deepseek-chat"
	g.AI.Temperature = 5
	if err := g.ValidateEnv(); err == nil {
		t.Fatal("ValidateEnv 应转发 AI 校验（温度越界）")
	}
	g.AI.Temperature = 0.3
	if err := g.ValidateEnv(); err != nil {
		t.Fatalf("合法 AI 配置不应报错: %v", err)
	}
	// key 空不算错误（走 warnings 通道），这里仅确认不阻塞
	g.AI.APIKey = ""
	if err := g.ValidateEnv(); err != nil {
		t.Fatalf("key 空不应阻塞保存: %v", err)
	}
}
