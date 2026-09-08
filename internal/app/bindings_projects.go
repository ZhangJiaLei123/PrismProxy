package app

import (
	"fmt"
	"log"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/domains"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
)

// ---------- M9 项目管理 Bindings（项目配置设计 §6.2） ----------
//
// 全部方法持有 projMu 串行化；锁序固定 projMu → a.mu/a.pmu，禁止反向。
// 切换项目的唯一入口是 SwitchProject（SaveSettings 一律忽略入参 currentProject）。

// ListProjects 项目清单（拷贝返回，前端顶栏下拉数据源）
func (a *App) ListProjects() ([]settings.ProjectMeta, error) {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	out := make([]settings.ProjectMeta, len(a.gcfg.Projects))
	copy(out, a.gcfg.Projects)
	return out, nil
}

// GetCurrentProject 当前项目清单项
func (a *App) GetCurrentProject() (settings.ProjectMeta, error) {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	return a.currentMeta(), nil
}

// SwitchProject 运行中热切换项目（设计 §5.2）：代理不重启、规则引擎热重建、
// 清空内存流量（含置顶）后载入新项目历史。失败时不产生半切换态。
func (a *App) SwitchProject(id string) error {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	return a.switchProjectLocked(id)
}

// switchProjectLocked 切换实现（调用方持 projMu）。
// 步骤序相对设计 §5.2 的调整：配置加载/域名组/规则预编译全部作为 preflight 放在
// 代际推进（projGen++）之前，任一步失败整体拒绝切换。
func (a *App) switchProjectLocked(id string) error {
	if id == a.proj.ID {
		return nil // 幂等：已在当前项目
	}
	var meta settings.ProjectMeta
	found := false
	for _, m := range a.gcfg.Projects {
		if m.ID == id {
			meta = m
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("项目 %q 不存在", id)
	}

	// preflight 1：加载新项目规则配置（损坏则拒绝切换，不留半切换态）
	proj, err := settings.LoadProjectConfig(a.cfgDir, meta.ID, meta.Name)
	if err != nil {
		return fmt.Errorf("项目配置损坏，未切换: %w", err)
	}
	proj.Name = meta.Name // 显示名以清单为准
	if proj.Migrate() {   // 旧 captureRules/processRules 迁移：落盘一次
		if serr := settings.SaveProjectConfig(a.cfgDir, proj); serr != nil {
			log.Printf("迁移项目配置落盘失败（内存态已迁移）: %v", serr)
		}
	}

	// preflight 2：域名组加载（缺失不致命）+ 规则引擎预编译（失败拒绝切换）
	groups, gerr := domains.LoadUser(settings.ProjectDomainsDir(a.cfgDir, proj.ID))
	if gerr != nil {
		groups = nil
		log.Printf("项目域名组加载失败（@组名 引用将不匹配）: %v", gerr)
	}
	var gmap map[string][]string
	if groups != nil {
		gmap = groups.Domains
	}
	e, err := rules.NewEngine(proj.FilterGroups, proj.DecryptRules, gmap)
	if err != nil {
		return fmt.Errorf("项目规则编译失败，未切换: %w", err)
	}

	// ---- 以下进入实际切换，不再失败 ----
	a.projGen.Add(1) // 代际推进：此后新流打新代际标，旧代际在途流终态丢弃
	a.st.ClearAll()  // 连置顶一起清（必须先于历史载入）
	a.proj = proj
	a.groups = groups
	a.switchPersist()                     // 换库：停旧 writer → 开新项目库 → 补载新项目历史
	a.eng.Set(e)                          // 规则引擎热重建（代理不重启）
	a.gcfg.CurrentProject = meta.ID       // 落盘当前项目指针
	if serr := a.gcfg.SaveGlobal(a.cfgDir); serr != nil {
		log.Printf("切换项目落盘失败（内存态已切换，重启后回到原项目）: %v", serr)
	}
	a.emitProjectChanged()
	log.Printf("已切换到项目 %q（%s）", meta.Name, meta.ID)
	return nil
}

// CreateProject 新建项目并自动切换（设计 §5.4）。
// fromID 非空=从该项目复制规则与整个 domains/（不复制历史库）；重名自动加序号。
func (a *App) CreateProject(name, fromID string) (settings.ProjectMeta, error) {
	a.projMu.Lock()
	defer a.projMu.Unlock()

	meta, err := settings.CreateProjectDir(a.cfgDir, a.gcfg, name, fromID)
	if err != nil {
		return settings.ProjectMeta{}, err
	}
	if serr := a.gcfg.SaveGlobal(a.cfgDir); serr != nil {
		// 回滚：移出清单 + 删除已建目录
		for i, m := range a.gcfg.Projects {
			if m.ID == meta.ID {
				a.gcfg.Projects = append(a.gcfg.Projects[:i], a.gcfg.Projects[i+1:]...)
				break
			}
		}
		_ = settings.DeleteProjectDir(a.cfgDir, meta.ID)
		return settings.ProjectMeta{}, fmt.Errorf("全局配置落盘: %w", serr)
	}
	// 创建后自动切换（项目已在清单，switchProjectLocked 不会再失败于"不存在"，
	// 但配置损坏/规则编译失败仍可能拒绝：此时项目保留在清单中，仅未切换）
	if serr := a.switchProjectLocked(meta.ID); serr != nil {
		return meta, fmt.Errorf("项目已创建，但切换失败: %w", serr)
	}
	return meta, nil
}

// RenameProject 重命名项目（重名自动加序号；当前项目改名后通知前端刷新顶栏）
func (a *App) RenameProject(id, name string) error {
	a.projMu.Lock()
	defer a.projMu.Unlock()

	uniq := settings.UniqueProjectName(name, a.gcfg.Projects, id)
	if uniq == "" {
		return fmt.Errorf("项目名称不能为空")
	}
	idx := -1
	for i, m := range a.gcfg.Projects {
		if m.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("项目 %q 不存在", id)
	}
	oldName := a.gcfg.Projects[idx].Name
	if uniq == oldName {
		return nil // 无变化
	}
	a.gcfg.Projects[idx].Name = uniq

	isCurrent := id == a.proj.ID
	if isCurrent {
		a.proj.Name = uniq
		if err := settings.SaveProjectConfig(a.cfgDir, a.proj); err != nil {
			a.gcfg.Projects[idx].Name = oldName
			a.proj.Name = oldName
			return fmt.Errorf("项目配置落盘: %w", err)
		}
	}
	if err := a.gcfg.SaveGlobal(a.cfgDir); err != nil {
		a.gcfg.Projects[idx].Name = oldName
		if isCurrent {
			a.proj.Name = oldName
			if rerr := settings.SaveProjectConfig(a.cfgDir, a.proj); rerr != nil {
				log.Printf("回滚项目配置失败（project.json 名称与清单不一致，以清单为准）: %v", rerr)
			}
		}
		return fmt.Errorf("全局配置落盘: %w", err)
	}
	if !isCurrent {
		// 非当前项目：同步 project.json 内的名称（失败仅 log，清单为准）
		if pc, err := settings.LoadProjectConfig(a.cfgDir, id, uniq); err == nil {
			pc.Name = uniq
			if serr := settings.SaveProjectConfig(a.cfgDir, pc); serr != nil {
				log.Printf("同步项目 %q 配置名称失败（以清单为准）: %v", id, serr)
			}
		}
	} else {
		a.emitProjectChanged() // 当前项目改名：前端刷新顶栏显示
	}
	return nil
}

// DeleteProject 删除项目（不能删当前项目；至少保留一个）
func (a *App) DeleteProject(id string) error {
	a.projMu.Lock()
	defer a.projMu.Unlock()

	if id == a.proj.ID {
		return fmt.Errorf("不能删除当前项目，请先切换到其他项目")
	}
	if len(a.gcfg.Projects) <= 1 {
		return fmt.Errorf("至少保留一个项目")
	}
	idx := -1
	for i, m := range a.gcfg.Projects {
		if m.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("项目 %q 不存在", id)
	}
	meta := a.gcfg.Projects[idx]
	a.gcfg.Projects = append(a.gcfg.Projects[:idx], a.gcfg.Projects[idx+1:]...)
	if err := a.gcfg.SaveGlobal(a.cfgDir); err != nil {
		// 回滚：按原位插回
		a.gcfg.Projects = append(a.gcfg.Projects[:idx], append([]settings.ProjectMeta{meta}, a.gcfg.Projects[idx:]...)...)
		return fmt.Errorf("全局配置落盘: %w", err)
	}
	if err := settings.DeleteProjectDir(a.cfgDir, id); err != nil {
		log.Printf("删除项目目录失败（清单已移除，残留目录 config/projects/%s）: %v", id, err)
	}
	return nil
}

// emitProjectChanged 通知前端项目已切换/改名（载荷 {id, name}；设计 §6.2）
func (a *App) emitProjectChanged() {
	if a.ctx == nil {
		return
	}
	meta := a.currentMeta()
	runtime.EventsEmit(a.ctx, "project:changed", map[string]string{"id": meta.ID, "name": meta.Name})
}
