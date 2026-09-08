package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prismproxy/internal/capture"
	"prismproxy/internal/ctlapi"
	"prismproxy/internal/domains"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
)

// ---------- 项目解析（设计 §6.3：id 精确 → 名称匹配 → 报错列清单） ----------

func TestResolveProjectID(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.Projects = []settings.ProjectMeta{
		{ID: "p1", Name: "商城联调"},
		{ID: "p2", Name: "商城联调"}, // 故意重名
		{ID: "p3", Name: "小游戏"},
	}
	a.proj.ID = "p1"

	a.projMu.Lock()
	defer a.projMu.Unlock()

	// 空串 → 当前项目
	if id, err := a.resolveProjectIDLocked(""); err != nil || id != "p1" {
		t.Fatalf("空串应解析为当前项目: id=%s err=%v", id, err)
	}
	// id 精确
	if id, err := a.resolveProjectIDLocked("p3"); err != nil || id != "p3" {
		t.Fatalf("id 精确解析失败: id=%s err=%v", id, err)
	}
	// 唯一名称
	if id, err := a.resolveProjectIDLocked("小游戏"); err != nil || id != "p3" {
		t.Fatalf("名称解析失败: id=%s err=%v", id, err)
	}
	// 重名 → 提示改用 id
	if _, err := a.resolveProjectIDLocked("商城联调"); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("重名应提示改用 id: err=%v", err)
	}
	// 不存在 → 列出清单
	if _, err := a.resolveProjectIDLocked("nope"); err == nil || !strings.Contains(err.Error(), "小游戏") {
		t.Fatalf("不存在应报错并列清单: err=%v", err)
	}
}

// ---------- 切换项目热切换（设计 §5.2） ----------

func TestSwitchProjectHotSwap(t *testing.T) {
	a := newTestApp(t)
	a.rec.GenFunc = func() uint64 { return a.projGen.Load() }
	a.gcfg.Projects = []settings.ProjectMeta{
		{ID: "default", Name: "A"},
		{ID: "pb", Name: "B"},
	}
	a.gcfg.CurrentProject = "default"

	// 项目 B 落盘黑名单规则 b.example
	pb := settings.DefaultProjectConfig("pb", "B")
	pb.FilterGroups = []rules.FilterGroup{{
		ID: "gb", Name: "B 黑名单", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"b.example"},
	}}
	if err := settings.SaveProjectConfig(a.cfgDir, pb); err != nil {
		t.Fatal(err)
	}

	// 当前项目放两条流：一条置顶 + 一条普通
	a.st.Add(testFlow("keep-pin"))
	if err := a.SetFlowPinned("keep-pin", true); err != nil {
		t.Fatal(err)
	}
	a.st.Add(testFlow("keep-normal"))

	if err := a.SwitchProject("pb"); err != nil {
		t.Fatalf("切换失败: %v", err)
	}

	// 列表清空（置顶无残留）；B 规则热生效；指针落盘
	if flows := a.st.List(); len(flows) != 0 {
		t.Fatalf("切换后列表应为空（含置顶）: %d 条", len(flows))
	}
	if a.eng.Get().ShouldDisplay("b.example", "https://b.example/", "") {
		t.Fatal("B 项目黑名单应已热生效")
	}
	if a.proj.ID != "pb" || a.gcfg.CurrentProject != "pb" {
		t.Fatalf("当前项目指针错误: proj=%s gcfg=%s", a.proj.ID, a.gcfg.CurrentProject)
	}

	// 旧代际在途流终态：丢弃（不入列表）
	old := capture.NewFlow("inflight")
	old.State = capture.StateDone
	old.ServerAddr = "old.example"
	old.Request = &capture.Message{Method: "GET", URL: "https://old.example/"}
	a.rec.Finish(old) // Gen=0（旧代际）vs 当前 projGen=1 → 丢弃
	if _, ok := a.st.Get("inflight"); ok {
		t.Fatal("旧代际在途流终态应被丢弃")
	}

	// 新代际流正常入列表
	fresh := capture.NewFlow("fresh")
	fresh.State = capture.StateDone
	fresh.ServerAddr = "b.example"
	fresh.Request = &capture.Message{Method: "GET", URL: "https://b.example/"}
	a.rec.Begin(fresh)
	if _, ok := a.st.Get("fresh"); !ok {
		t.Fatal("新代际流应正常入列表")
	}
}

// ---------- 项目 CRUD（设计 §5.4） ----------

func TestCreateProjectCopiesRulesNoDB(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.Projects = []settings.ProjectMeta{{ID: "default", Name: "A"}}
	a.gcfg.CurrentProject = "default"
	// 当前项目：快捷忽略规则 + 用户域名组
	if _, err := a.AddQuickIgnore("host", "blocked.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := domains.WriteUser(settings.ProjectDomainsDir(a.cfgDir, "default"), "mygrp", []byte("a.example\n")); err != nil {
		t.Fatal(err)
	}

	meta, err := a.CreateProject("副本", "default")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// 创建后自动切换
	if a.proj.ID != meta.ID || a.gcfg.CurrentProject != meta.ID {
		t.Fatalf("创建后应切换到新项目: proj=%s current=%s", a.proj.ID, a.gcfg.CurrentProject)
	}
	// 规则复制
	found := false
	for _, g := range a.proj.FilterGroups {
		if g.ID == QuickIgnoreHostGroupID && len(g.Hosts) == 1 && g.Hosts[0] == "blocked.example" {
			found = true
		}
	}
	if !found {
		t.Fatalf("新项目应复制快捷忽略规则: %#v", a.proj.FilterGroups)
	}
	// 域名组复制
	data, err := os.ReadFile(filepath.Join(settings.ProjectDomainsDir(a.cfgDir, meta.ID), "mygrp.txt"))
	if err != nil || !strings.Contains(string(data), "a.example") {
		t.Fatalf("域名组应被复制: err=%v data=%s", err, data)
	}
	// prism.db 不复制
	if _, err := os.Stat(filepath.Join(a.projDir(), "prism.db")); !os.IsNotExist(err) {
		t.Fatalf("新项目不应带历史库: err=%v", err)
	}
}

func TestDeleteProjectConstraints(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.Projects = []settings.ProjectMeta{
		{ID: "default", Name: "A"},
		{ID: "pb", Name: "B"},
	}
	a.gcfg.CurrentProject = "default"

	// 当前项目不可删
	if err := a.DeleteProject("default"); err == nil {
		t.Fatal("当前项目应不可删")
	}
	// 切到 B 后可删 A；只剩一个时不可删
	if err := a.SwitchProject("pb"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteProject("default"); err != nil {
		t.Fatalf("删除非当前项目失败: %v", err)
	}
	if _, err := os.Stat(settings.ProjectDir(a.cfgDir, "default")); !os.IsNotExist(err) {
		t.Fatalf("删除后目录应清除: err=%v", err)
	}
	if err := a.DeleteProject("pb"); err == nil {
		t.Fatal("最后一个项目应不可删")
	}
}

// ---------- SaveSettings 并发令牌与字段拆分（设计 §5.5/§6.1） ----------

func TestSaveSettingsRulesProjectToken(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.Projects = []settings.ProjectMeta{{ID: "default", Name: "A"}}
	a.gcfg.CurrentProject = "default"

	// rulesProject 与当前项目不一致 → 拒绝保存
	bad := a.GetSettings()
	bad.RulesProject = "other"
	if _, err := a.SaveSettings(bad); err == nil {
		t.Fatal("rulesProject 不一致应拒绝保存")
	}
	if len(a.proj.FilterGroups) != 0 {
		t.Fatal("拒绝保存时规则不应被改写")
	}

	// 入参 currentProject 被忽略（切换唯一入口是 SwitchProject）
	nu := a.GetSettings()
	nu.RulesProject = a.proj.ID
	nu.CurrentProject = settings.ProjectMeta{ID: "evil", Name: "evil"}
	nu.FilterGroups = []rules.FilterGroup{{
		ID: "g9", Name: "新组", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"x.example"},
	}}
	res, err := a.SaveSettings(nu)
	if err != nil || res == nil {
		t.Fatalf("合法保存失败: err=%v res=%v", err, res)
	}
	if a.gcfg.CurrentProject == "evil" {
		t.Fatal("currentProject 入参应被忽略")
	}
	if len(a.proj.FilterGroups) != 1 || a.proj.FilterGroups[0].ID != "g9" {
		t.Fatalf("规则应写入当前项目: %#v", a.proj.FilterGroups)
	}
	if !a.eng.Get().ShouldDisplay("x.example", "https://x.example/", "") == false {
		t.Fatal("新规则应热生效")
	}
	// 落盘到项目 project.json
	data, err := os.ReadFile(filepath.Join(a.cfgDir, "projects", "default", "project.json"))
	if err != nil || !strings.Contains(string(data), "g9") {
		t.Fatalf("project.json 应含新规则: err=%v", err)
	}
}

// ---------- CLI 目标项目写（设计 §6.3：非当前项目只改文件不热切换；非法规则拒绝不落盘） ----------

func TestCtlWriteNonCurrentProject(t *testing.T) {
	a := newTestApp(t)
	a.gcfg.Projects = []settings.ProjectMeta{
		{ID: "default", Name: "A"},
		{ID: "pb", Name: "B"},
	}
	a.gcfg.CurrentProject = "default"
	pb := settings.DefaultProjectConfig("pb", "B")
	pb.FilterGroups = []rules.FilterGroup{{
		ID: "g1", Name: "B 黑名单", Enabled: true, Mode: rules.ModeBlacklist, Hosts: []string{"b.example"},
	}}
	if err := settings.SaveProjectConfig(a.cfgDir, pb); err != nil {
		t.Fatal(err)
	}

	s := newCtlService(a)
	// ListRules 指定项目：返回该 id 与其规则
	v, err := s.ListRules("pb")
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := v.(map[string]any); !ok || m["project"] != "pb" {
		t.Fatalf("ListRules 应返回目标项目: %#v", v)
	}

	// 写非当前项目：落盘 pb，但当前项目规则与引擎不变（不触发热切换）
	if err := s.RuleGroupSetEnabled("pb", "g1", false); err != nil {
		t.Fatalf("写非当前项目失败: %v", err)
	}
	disk, err := settings.LoadProjectConfig(a.cfgDir, "pb", "")
	if err != nil {
		t.Fatal(err)
	}
	if disk.FilterGroups[0].Enabled {
		t.Fatal("pb 规则应已落盘停用")
	}
	if a.eng.Get().ShouldDisplay("b.example", "https://b.example/", "") != true {
		t.Fatal("当前项目引擎不应受非当前项目写入影响")
	}
	if a.gcfg.CurrentProject != "default" {
		t.Fatal("currentProject 不应被改变")
	}

	// ctl 快捷忽略写目标项目（addQuickIgnoreTo 复用路径）
	added, err := s.RuleIgnore("pb", "host", "ignored.example")
	if err != nil || !added {
		t.Fatalf("ctl RuleIgnore 失败: added=%v err=%v", added, err)
	}
	disk2, err := settings.LoadProjectConfig(a.cfgDir, "pb", "")
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, g := range disk2.FilterGroups {
		if g.ID == QuickIgnoreHostGroupID && len(g.Hosts) == 1 && g.Hosts[0] == "ignored.example" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("ctl 忽略应写入目标项目: %#v", disk2.FilterGroups)
	}

	// 非当前项目写入非法规则（非法模式）→ 编译校验拒绝、不落盘
	//（路径条目不含 *? 时走 exact 不编译正则，故用引擎必拒的非法模式构造非法规则）
	badPath := filepath.Join(settings.ProjectDir(a.cfgDir, "pb"), "project.json")
	badPc := settings.DefaultProjectConfig("pb", "B")
	badPc.FilterGroups = []rules.FilterGroup{{
		ID: "bad", Name: "非法模式组", Enabled: true, Mode: "bogus",
	}}
	if err := settings.SaveProjectConfig(a.cfgDir, badPc); err != nil {
		t.Fatal(err)
	}
	badBytes, err := os.ReadFile(badPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RuleGroupSetEnabled("pb", "bad", false); err == nil || !strings.Contains(err.Error(), "编译失败") {
		t.Fatalf("非法规则应被编译校验拒绝: err=%v", err)
	}
	after, err := os.ReadFile(badPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(badBytes) {
		t.Fatal("被拒绝的写入不应落盘")
	}
}

var _ ctlapi.Service = (*ctlService)(nil) // 编译期断言（ctl_bridge.go 已有，此处防误删）
