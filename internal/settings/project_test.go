package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prismproxy/internal/rules"
)

func TestLoadProjectConfigMissingReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	pc, err := LoadProjectConfig(dir, "p1", "项目一")
	if err != nil {
		t.Fatal(err)
	}
	if pc.ID != "p1" || pc.Name != "项目一" {
		t.Fatalf("默认项目配置 id/name 不对: %+v", pc)
	}
	if pc.FilterGroups == nil || pc.DecryptRules == nil {
		t.Fatal("规则切片应初始化为空（不序列化出 null）")
	}
}

func TestLoadProjectConfigCorrupted(t *testing.T) {
	dir := t.TempDir()
	pdir := ProjectDir(dir, "p1")
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, projectFileName), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectConfig(dir, "p1", "项目一"); err == nil {
		t.Fatal("损坏的 project.json 应返回错误（调用方降级默认）")
	}
}

func TestLoadProjectConfigIDDrift(t *testing.T) {
	dir := t.TempDir()
	pc := &ProjectConfig{ID: "other", Name: "项目一", FilterGroups: []rules.FilterGroup{}, DecryptRules: []rules.DecryptRule{}}
	if err := SaveProjectConfig(dir, pc); err != nil {
		t.Fatal(err)
	}
	// 手工搬到另一个目录名，模拟手工改名漂移
	if err := os.MkdirAll(ProjectDir(dir, "p2"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(ProjectDir(dir, "other"), projectFileName))
	if err := os.WriteFile(filepath.Join(ProjectDir(dir, "p2"), projectFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProjectConfig(dir, "p2", "清单名")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "p2" {
		t.Fatalf("id 漂移应以目录名为准: %q", got.ID)
	}
	if got.Name != "项目一" {
		t.Fatalf("文件内 name 应保留: %q", got.Name)
	}
}

func TestSaveLoadProjectRoundtrip(t *testing.T) {
	dir := t.TempDir()
	pc := DefaultProjectConfig("p1", "项目一")
	pc.FilterGroups = []rules.FilterGroup{{
		ID: "1", Name: "只抓网易", Enabled: true, Mode: rules.ModeWhitelist,
		Hosts: []string{"netease.com", "@netease"}, Paths: []string{"/api/*"}, Processes: []string{"dnplayer.exe"},
	}}
	pc.DecryptRules = []rules.DecryptRule{{Action: rules.ActionBypass, Host: "pin.example.com"}}

	if err := SaveProjectConfig(dir, pc); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProjectConfig(dir, "p1", "项目一")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FilterGroups) != 1 || got.FilterGroups[0].Name != "只抓网易" ||
		got.FilterGroups[0].Mode != rules.ModeWhitelist || len(got.FilterGroups[0].Paths) != 1 {
		t.Fatalf("过滤规则组丢失: %+v", got.FilterGroups)
	}
	if len(got.DecryptRules) != 1 || got.DecryptRules[0].Host != "pin.example.com" {
		t.Fatalf("解密规则丢失: %+v", got.DecryptRules)
	}
}

func TestUniqueProjectName(t *testing.T) {
	metas := []ProjectMeta{{ID: "a", Name: "项目"}, {ID: "b", Name: "项目 (2)"}}
	if got := UniqueProjectName("  ", metas, ""); got != "" {
		t.Fatalf("空白名称应返回空串: %q", got)
	}
	if got := UniqueProjectName("新项目", metas, ""); got != "新项目" {
		t.Fatalf("无重名应原样返回: %q", got)
	}
	if got := UniqueProjectName("项目", metas, ""); got != "项目 (3)" {
		t.Fatalf("重名应加序号: %q", got)
	}
	// 重命名自身不算重名
	if got := UniqueProjectName("项目", metas, "a"); got != "项目" {
		t.Fatalf("排除自身后不应加序号: %q", got)
	}
}

func TestEnsureDefaultProject(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGlobal()
	if err := EnsureDefaultProject(dir, g); err != nil {
		t.Fatal(err)
	}
	if len(g.Projects) != 1 || g.Projects[0].Name != "默认项目" || g.CurrentProject != g.Projects[0].ID {
		t.Fatalf("应创建默认项目并指向: %+v", g.Projects)
	}
	id := g.Projects[0].ID
	if _, err := os.Stat(filepath.Join(ProjectDir(dir, id), projectFileName)); err != nil {
		t.Fatal("默认项目 project.json 应已创建")
	}
	if info, err := os.Stat(ProjectDomainsDir(dir, id)); err != nil || !info.IsDir() {
		t.Fatal("默认项目 domains/ 应已创建")
	}
	// 幂等：清单非空不再创建
	if err := EnsureDefaultProject(dir, g); err != nil {
		t.Fatal(err)
	}
	if len(g.Projects) != 1 || g.Projects[0].ID != id {
		t.Fatal("EnsureDefaultProject 应幂等")
	}
}

func TestPruneMissingProjects(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGlobal()
	// 建两个真实项目
	if err := EnsureDefaultProject(dir, g); err != nil {
		t.Fatal(err)
	}
	keep, err := CreateProjectDir(dir, g, "存活项目", "")
	if err != nil {
		t.Fatal(err)
	}
	// 幽灵条目：在清单里但无目录（半迁移/手工删目录）
	ghost := ProjectMeta{ID: "9999999999999-9", Name: "幽灵项目"}
	g.Projects = append(g.Projects, ghost)
	g.CurrentProject = ghost.ID // 当前指向幽灵

	if changed := PruneMissingProjects(dir, g); !changed {
		t.Fatal("存在目录缺失条目时应返回 changed=true")
	}
	if len(g.Projects) != 2 {
		t.Fatalf("幽灵条目应被剔除，剩余 2 个，实际 %d: %+v", len(g.Projects), g.Projects)
	}
	for _, m := range g.Projects {
		if m.ID == ghost.ID {
			t.Fatal("幽灵条目仍在清单中")
		}
	}
	if g.CurrentProject != "" {
		t.Fatalf("当前项目是幽灵时指针应清空交回退，实际 %q", g.CurrentProject)
	}

	// 当前项目存活时指针保留
	g.CurrentProject = keep.ID
	if changed := PruneMissingProjects(dir, g); changed {
		t.Fatal("清单全部存活时不应变化")
	}
	if g.CurrentProject != keep.ID {
		t.Fatalf("存活的当前项目指针不应被改动: %q", g.CurrentProject)
	}

	// 全部目录缺失 → 清单清空（调用方据此进入欢迎页）
	if err := os.RemoveAll(ProjectsRoot(dir)); err != nil {
		t.Fatal(err)
	}
	g.CurrentProject = keep.ID
	if changed := PruneMissingProjects(dir, g); !changed || len(g.Projects) != 0 || g.CurrentProject != "" {
		t.Fatalf("全部缺失应清空清单与指针: changed 后 projects=%+v current=%q", g.Projects, g.CurrentProject)
	}
}

func TestCreateProjectDirBlank(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGlobal()
	if err := EnsureDefaultProject(dir, g); err != nil {
		t.Fatal(err)
	}
	meta, err := CreateProjectDir(dir, g, "  新项目  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "新项目" {
		t.Fatalf("名称应去空白: %q", meta.Name)
	}
	pc, err := LoadProjectConfig(dir, meta.ID, meta.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.FilterGroups) != 0 || len(pc.DecryptRules) != 0 {
		t.Fatal("空白起点规则应为空")
	}
	if len(g.Projects) != 2 {
		t.Fatal("新项目应加入清单")
	}
	// 重名自动加序号
	meta2, err := CreateProjectDir(dir, g, "新项目", "")
	if err != nil {
		t.Fatal(err)
	}
	if meta2.Name != "新项目 (2)" {
		t.Fatalf("重名应加序号: %q", meta2.Name)
	}
	// 空白名称报错
	if _, err := CreateProjectDir(dir, g, "   ", ""); err == nil {
		t.Fatal("空白名称应报错")
	}
	// fromID 不存在报错
	if _, err := CreateProjectDir(dir, g, "x", "no-such-id"); err == nil {
		t.Fatal("fromID 不存在应报错")
	}
}

func TestCreateProjectDirCopy(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGlobal()
	if err := EnsureDefaultProject(dir, g); err != nil {
		t.Fatal(err)
	}
	src := g.Projects[0]
	// 源项目：规则 + 用户域名组 + prism.db
	pc := DefaultProjectConfig(src.ID, src.Name)
	pc.FilterGroups = []rules.FilterGroup{{ID: "1", Name: "组", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"a.com"}}}
	if err := SaveProjectConfig(dir, pc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProjectDomainsDir(dir, src.ID), "my.json"), []byte(`{"hosts":["a.com"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProjectDir(dir, src.ID), DefaultDBPath), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}

	meta, err := CreateProjectDir(dir, g, "副本", src.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadProjectConfig(dir, meta.ID, meta.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FilterGroups) != 1 || got.FilterGroups[0].Hosts[0] != "a.com" {
		t.Fatalf("复制应携带规则: %+v", got.FilterGroups)
	}
	if got.ID != meta.ID || got.Name != "副本" {
		t.Fatal("复制的 project.json id/name 应取新值")
	}
	if _, err := os.Stat(filepath.Join(ProjectDomainsDir(dir, meta.ID), "my.json")); err != nil {
		t.Fatal("复制应携带 domains/")
	}
	if _, err := os.Stat(filepath.Join(ProjectDir(dir, meta.ID), DefaultDBPath)); !os.IsNotExist(err) {
		t.Fatal("复制不应携带 prism.db（历史流量不跟走）")
	}
}

// 旧版单配置 → 多项目一次性迁移（项目配置设计 §8.1）
func TestMigrateToProjects(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "listenAddr": "0.0.0.0:8888",
  "upstreamMode": "manual",
  "upstreamProxy": "127.0.0.1:7890",
  "maxFlows": 500,
  "bypassList": ["localhost", "corp.com"],
  "filterGroups": [{"id":"g1","name":"旧组","enabled":true,"mode":"blacklist","hosts":["a.com"]}],
  "captureRules": [{"action":"exclude","host":"tracker.com"}],
  "decryptRules": [{"action":"bypass","host":"pin.example.com"}],
  "persist": {"enabled": true, "retainDays": 3, "maxMB": 100}
}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// 旧资产：domains/ 与 prism.db(±wal)
	if err := os.MkdirAll(filepath.Join(dir, "domains"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "domains", "u.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultDBPath), []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultDBPath+"-wal"), []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MigrateToProjects(dir); err != nil {
		t.Fatal(err)
	}

	// 备份存在且为原始内容
	bak, err := os.ReadFile(filepath.Join(dir, backupFileName))
	if err != nil || !strings.Contains(string(bak), `"captureRules"`) {
		t.Fatalf("备份应为原始旧配置: %v", err)
	}
	// 新全局配置：环境字段照抄、清单与指针正确、无 dbPath
	g, err := LoadGlobal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.ListenAddr != "0.0.0.0:8888" || g.UpstreamProxy != "127.0.0.1:7890" || g.MaxFlows != 500 {
		t.Fatalf("环境字段应照抄: %+v", g)
	}
	if len(g.BypassList) != 2 || g.BypassList[1] != "corp.com" {
		t.Fatalf("bypassList 归全局照抄: %+v", g.BypassList)
	}
	if g.Persist.DBPath != "" || !g.Persist.Enabled || g.Persist.RetainDays != 3 {
		t.Fatalf("persist 策略照抄且无 dbPath: %+v", g.Persist)
	}
	if len(g.Projects) != 1 || g.Projects[0].Name != "默认项目" || g.CurrentProject != g.Projects[0].ID {
		t.Fatalf("清单/当前指针不对: %+v", g.Projects)
	}
	id := g.Projects[0].ID
	// project.json：旧规则 + captureRules 已迁移
	pc, err := LoadProjectConfig(dir, id, "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.DecryptRules) != 1 || pc.DecryptRules[0].Host != "pin.example.com" {
		t.Fatalf("decryptRules 应保留: %+v", pc.DecryptRules)
	}
	var hasOld, hasMigrated bool
	for _, fg := range pc.FilterGroups {
		if fg.ID == "g1" {
			hasOld = true
		}
		if fg.ID == "_migrated_capture_black" {
			hasMigrated = true
		}
	}
	if !hasOld || !hasMigrated {
		t.Fatalf("旧组保留 + captureRules 迁移应同时成立: %+v", pc.FilterGroups)
	}
	// 资产已移动
	if _, err := os.Stat(filepath.Join(ProjectDomainsDir(dir, id), "u.json")); err != nil {
		t.Fatal("domains/ 应移入项目目录")
	}
	if _, err := os.Stat(filepath.Join(ProjectDir(dir, id), DefaultDBPath)); err != nil {
		t.Fatal("prism.db 应移入项目目录")
	}
	if _, err := os.Stat(filepath.Join(ProjectDir(dir, id), DefaultDBPath+"-wal")); err != nil {
		t.Fatal("prism.db-wal 应一并移入")
	}
	if _, err := os.Stat(filepath.Join(dir, "domains")); !os.IsNotExist(err) {
		t.Fatal("旧 domains/ 应已移走")
	}
	if _, err := os.Stat(filepath.Join(dir, DefaultDBPath)); !os.IsNotExist(err) {
		t.Fatal("旧 prism.db 应已移走")
	}

	// 幂等：二次执行跳过（清单不变、备份不被覆盖）
	if err := MigrateToProjects(dir); err != nil {
		t.Fatal(err)
	}
	g2, _ := LoadGlobal(dir)
	if len(g2.Projects) != 1 || g2.Projects[0].ID != id {
		t.Fatal("迁移应幂等，二次执行不重复建项目")
	}
}

// 自定义 dbPath 的旧配置：DB 不迁移仅告警
func TestMigrateToProjectsCustomDBPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(t.TempDir(), "custom.db")
	if err := os.WriteFile(custom, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := `{"listenAddr":"127.0.0.1:9090","persist":{"enabled":true,"dbPath":` +
		strings.ReplaceAll(`"`+custom+`"`, `\`, `\\`) + `}}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateToProjects(dir); err != nil {
		t.Fatal(err)
	}
	g, _ := LoadGlobal(dir)
	if len(g.Projects) != 1 {
		t.Fatal("迁移应完成")
	}
	if _, err := os.Stat(filepath.Join(ProjectDir(dir, g.Projects[0].ID), DefaultDBPath)); !os.IsNotExist(err) {
		t.Fatal("自定义路径 DB 不应迁移进项目目录")
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatal("自定义路径 DB 应原样保留")
	}
}

// 全新安装：无 settings.json，迁移为 no-op
func TestMigrateToProjectsFreshInstall(t *testing.T) {
	dir := t.TempDir()
	if err := MigrateToProjects(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Fatal("全新安装不应生成 settings.json（由后续默认流程写）")
	}
}
