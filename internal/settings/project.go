// 项目配置（项目配置设计 §4.2/§5.4/§8）：ProjectConfig 类型、项目目录解析、
// 项目 CRUD 的文件层助手，以及旧版单配置 → 多项目的一次性迁移 MigrateToProjects。
package settings

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prismproxy/internal/rules"
)

const (
	projectsDirName = "projects"
	projectFileName = "project.json"
	backupFileName  = "settings.json.bak-v1"
)

// ProjectMeta 项目清单项（项目目录内 project.json 亦冗余 name，见 §4.2）
type ProjectMeta struct {
	ID   string `json:"id"`   // 项目 id（时间戳毫秒+序号，沿用规则组 ID 惯例）
	Name string `json:"name"` // 显示名（用户可改，必填、去空白、同清单内重名加序号区分）
}

// ProjectConfig 单个项目的规则类配置（项目目录内 project.json）。
type ProjectConfig struct {
	ID           string              `json:"id"`   // 与目录名一致，加载时校验
	Name         string              `json:"name"` // 冗余自清单，便于单目录自识别/导出
	FilterGroups []rules.FilterGroup `json:"filterGroups"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`

	// 旧字段仅作迁移用途（与旧 Settings.Migrate 同一套逻辑）
	CaptureRules []rules.CaptureRule `json:"captureRules,omitempty"`
	ProcessRules []rules.ProcessRule `json:"processRules,omitempty"`
}

// ProjectsRoot 项目根目录 config/projects/
func ProjectsRoot(cfgDir string) string { return filepath.Join(cfgDir, projectsDirName) }

// ProjectDir 项目目录 config/projects/<id>/
func ProjectDir(cfgDir, id string) string { return filepath.Join(ProjectsRoot(cfgDir), id) }

// ProjectDomainsDir 项目用户域名组目录 config/projects/<id>/domains/
func ProjectDomainsDir(cfgDir, id string) string { return filepath.Join(ProjectDir(cfgDir, id), "domains") }

// DefaultProjectConfig 新建项目的默认配置（规则空白起点；如需沿用已有规则用 CreateProjectDir 的 fromID）
func DefaultProjectConfig(id, name string) *ProjectConfig {
	return &ProjectConfig{
		ID:           id,
		Name:         name,
		FilterGroups: []rules.FilterGroup{},
		DecryptRules: []rules.DecryptRule{},
	}
}

// LoadProjectConfig 加载项目配置；文件缺失返回默认（name 为清单中的显示名兜底）；
// 损坏返回错误（调用方降级默认并提示）。id 与目录名不一致时以目录名为准并告警（防手工改名漂移）。
func LoadProjectConfig(cfgDir, id, name string) (*ProjectConfig, error) {
	data, err := os.ReadFile(filepath.Join(ProjectDir(cfgDir, id), projectFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultProjectConfig(id, name), nil
		}
		return nil, err
	}
	pc := DefaultProjectConfig(id, name) // 默认值兜底，兼容缺字段
	if err := json.Unmarshal(data, pc); err != nil {
		return nil, fmt.Errorf("解析 projects/%s/%s: %w", id, projectFileName, err)
	}
	if pc.ID != id {
		log.Printf("settings: projects/%s/project.json 内 id=%q 与目录名不一致，以目录名为准", id, pc.ID)
		pc.ID = id
	}
	if pc.Name == "" {
		pc.Name = name
	}
	if pc.FilterGroups == nil {
		pc.FilterGroups = []rules.FilterGroup{}
	}
	if pc.DecryptRules == nil {
		pc.DecryptRules = []rules.DecryptRule{}
	}
	return pc, nil
}

// SaveProjectConfig 原子写项目配置（tmp + rename，文件 0600、目录 0700）
func SaveProjectConfig(cfgDir string, pc *ProjectConfig) error {
	dir := ProjectDir(cfgDir, pc.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pc, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, projectFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, projectFileName))
}

// NewProjectID 生成项目 id：<时间戳毫秒>-<序号>，与现有目录/清单去重
func NewProjectID(cfgDir string, g *GlobalSettings) string {
	ts := time.Now().UnixMilli()
	for seq := 1; ; seq++ {
		id := fmt.Sprintf("%d-%d", ts, seq)
		if _, err := os.Stat(ProjectDir(cfgDir, id)); err == nil {
			continue
		}
		dup := false
		for _, m := range g.Projects {
			if m.ID == id {
				dup = true
				break
			}
		}
		if !dup {
			return id
		}
	}
}

// UniqueProjectName 名称去空白后在清单内去重（excludeID 自身除外），重名自动加「 (2)」「 (3)」序号。
// 空白名称返回空串（调用方校验报错）。
func UniqueProjectName(name string, metas []ProjectMeta, excludeID string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		return ""
	}
	candidate := base
	for i := 2; ; i++ {
		dup := false
		for _, m := range metas {
			if m.ID != excludeID && m.Name == candidate {
				dup = true
				break
			}
		}
		if !dup {
			return candidate
		}
		candidate = fmt.Sprintf("%s (%d)", base, i)
	}
}

// EnsureDefaultProject 清单为空时创建「默认项目」（目录 + 默认 project.json），并指向 currentProject。
// 调用方负责随后 SaveGlobal 落盘。
func EnsureDefaultProject(cfgDir string, g *GlobalSettings) error {
	if len(g.Projects) > 0 {
		return nil
	}
	meta := ProjectMeta{ID: NewProjectID(cfgDir, g), Name: "默认项目"}
	if err := os.MkdirAll(ProjectDomainsDir(cfgDir, meta.ID), 0o700); err != nil {
		return err
	}
	if err := SaveProjectConfig(cfgDir, DefaultProjectConfig(meta.ID, meta.Name)); err != nil {
		return err
	}
	g.Projects = []ProjectMeta{meta}
	g.CurrentProject = meta.ID
	return nil
}

// CreateProjectDir 创建项目目录与 project.json（项目配置设计 §5.4）。
// fromID 为空=空白起点；非空=从该项目复制 filterGroups/decryptRules 与整个 domains/
// （不复制 prism.db，历史流量不跟走）。仅改文件与内存清单 g.Projects，
// 调用方负责 SaveGlobal 落盘与后续切换。失败时清理已建目录。
func CreateProjectDir(cfgDir string, g *GlobalSettings, name, fromID string) (ProjectMeta, error) {
	uniq := UniqueProjectName(name, g.Projects, "")
	if uniq == "" {
		return ProjectMeta{}, fmt.Errorf("项目名称不能为空")
	}
	meta := ProjectMeta{ID: NewProjectID(cfgDir, g), Name: uniq}

	pc := DefaultProjectConfig(meta.ID, meta.Name)
	if fromID != "" {
		found := false
		for _, m := range g.Projects {
			if m.ID == fromID {
				found = true
				break
			}
		}
		if !found {
			return ProjectMeta{}, fmt.Errorf("来源项目 %q 不存在", fromID)
		}
		src, err := LoadProjectConfig(cfgDir, fromID, "")
		if err != nil {
			return ProjectMeta{}, fmt.Errorf("读取来源项目配置: %w", err)
		}
		pc.FilterGroups = append([]rules.FilterGroup{}, src.FilterGroups...)
		pc.DecryptRules = append([]rules.DecryptRule{}, src.DecryptRules...)
	}

	if err := os.MkdirAll(ProjectDomainsDir(cfgDir, meta.ID), 0o700); err != nil {
		return ProjectMeta{}, err
	}
	if fromID != "" {
		srcDomains := ProjectDomainsDir(cfgDir, fromID)
		if info, err := os.Stat(srcDomains); err == nil && info.IsDir() {
			if err := copyDir(srcDomains, ProjectDomainsDir(cfgDir, meta.ID)); err != nil {
				os.RemoveAll(ProjectDir(cfgDir, meta.ID))
				return ProjectMeta{}, fmt.Errorf("复制域名组: %w", err)
			}
		}
	}
	if err := SaveProjectConfig(cfgDir, pc); err != nil {
		os.RemoveAll(ProjectDir(cfgDir, meta.ID))
		return ProjectMeta{}, err
	}
	g.Projects = append(g.Projects, meta)
	return meta, nil
}

// DeleteProjectDir 删除整个项目目录（含 project.json/domains/prism.db）
func DeleteProjectDir(cfgDir, id string) error {
	return os.RemoveAll(ProjectDir(cfgDir, id))
}

// legacySettings 旧版单文件 settings.json 的全部字段并集（仅迁移读取用）
type legacySettings struct {
	FilterGroups []rules.FilterGroup `json:"filterGroups"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`
	CaptureRules []rules.CaptureRule `json:"captureRules,omitempty"`
	ProcessRules []rules.ProcessRule `json:"processRules,omitempty"`
}

// MigrateToProjects 旧版单配置 → 多项目（一次性，幂等；项目配置设计 §8.1）。
// 检测 settings.json 存在且无 projects 字段时执行：备份 bak-v1 → 建默认项目目录 →
// 移动 domains/ 与 prism.db(±wal/shm) 进项目目录（自定义 dbPath 不迁移仅告警）→
// 生成 project.json（执行一次 Migrate）→ 生成新全局 settings.json。
// 已是新结构或全新安装返回 nil；中途失败返回错误，调用方 log 后继续以默认兜底启动，不阻塞程序。
func MigrateToProjects(cfgDir string) error {
	path := filepath.Join(cfgDir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 全新安装
		}
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // 损坏文件交给 LoadGlobal 报错降级，不做迁移
	}
	if _, ok := raw["projects"]; ok {
		return nil // 已是新结构，幂等跳过
	}
	log.Printf("settings migrate: 检测到旧版单配置，开始迁移为多项目结构")

	// 1. 备份旧文件（已存在备份不覆盖，保证重入不破坏原始备份）
	bak := filepath.Join(cfgDir, backupFileName)
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		if err := os.WriteFile(bak, data, 0o600); err != nil {
			log.Printf("settings migrate: 备份旧配置失败: %v（继续迁移）", err)
		}
	}

	// 2. 解析旧配置：环境字段经 GlobalSettings 默认值兜底，规则字段经 legacySettings 读取
	g := DefaultGlobal()
	_ = json.Unmarshal(data, g)
	var ls legacySettings
	_ = json.Unmarshal(data, &ls)

	// 3. 创建默认项目目录
	meta := ProjectMeta{ID: NewProjectID(cfgDir, g), Name: "默认项目"}
	pdir := ProjectDir(cfgDir, meta.ID)
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		return fmt.Errorf("创建默认项目目录: %w", err)
	}

	// 4. 移动旧资产进项目目录（rename 失败回退拷贝；再失败仅告警不阻塞）
	oldDomains := filepath.Join(cfgDir, "domains")
	if info, err := os.Stat(oldDomains); err == nil && info.IsDir() {
		if err := moveAsset(oldDomains, ProjectDomainsDir(cfgDir, meta.ID)); err != nil {
			log.Printf("settings migrate: 移动 domains 目录失败: %v（原目录保留）", err)
		}
	}
	if err := os.MkdirAll(ProjectDomainsDir(cfgDir, meta.ID), 0o700); err != nil {
		return fmt.Errorf("创建项目 domains 目录: %w", err)
	}
	customDB := false
	if g.Persist.DBPath != "" &&
		filepath.Clean(g.Persist.DBPath) != filepath.Clean(filepath.Join(cfgDir, DefaultDBPath)) {
		customDB = true
		log.Printf("settings migrate: 原数据库使用自定义路径 %s，不自动迁移、不再被读取；可手动移入 %s 并改名为 %s",
			g.Persist.DBPath, pdir, DefaultDBPath)
	}
	if !customDB {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			src := filepath.Join(cfgDir, DefaultDBPath+suffix)
			if _, err := os.Stat(src); err == nil {
				if err := moveAsset(src, filepath.Join(pdir, DefaultDBPath+suffix)); err != nil {
					log.Printf("settings migrate: 移动 %s 失败: %v（原文件保留）", src, err)
				}
			}
		}
	}

	// 5. 生成 project.json（旧 captureRules/processRules 迁移、_quick_ignore 拆组同源逻辑）
	pc := &ProjectConfig{
		ID:           meta.ID,
		Name:         meta.Name,
		FilterGroups: ls.FilterGroups,
		DecryptRules: ls.DecryptRules,
		CaptureRules: ls.CaptureRules,
		ProcessRules: ls.ProcessRules,
	}
	if pc.FilterGroups == nil {
		pc.FilterGroups = []rules.FilterGroup{}
	}
	if pc.DecryptRules == nil {
		pc.DecryptRules = []rules.DecryptRule{}
	}
	pc.Migrate()
	if err := SaveProjectConfig(cfgDir, pc); err != nil {
		return fmt.Errorf("写入默认项目配置: %w", err)
	}

	// 6. 生成新全局 settings.json（环境字段照抄含 bypassList；不写 dbPath）
	g.Persist.DBPath = ""
	g.Projects = []ProjectMeta{meta}
	g.CurrentProject = meta.ID
	if err := g.SaveGlobal(cfgDir); err != nil {
		return fmt.Errorf("写入新全局配置: %w", err)
	}
	log.Printf("settings migrate: 迁移完成，默认项目 %s（旧配置备份于 %s）", meta.ID, backupFileName)
	return nil
}

// moveAsset 移动文件/目录：rename 失败（跨卷/占用）回退拷贝后删源
func moveAsset(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := copyDir(src, dst); err != nil {
			return err
		}
	} else {
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return os.RemoveAll(src)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// Migrate 旧 captureRules/processRules → filterGroups（规则设计 §六，逻辑与旧 Settings.Migrate 同源）。
// 返回是否发生迁移（调用方据此落盘一次；迁移幂等：旧字段清空后二次调用返回 false）。
// urlRe/method 维度丢弃（path glob + 展示层方法过滤替代）并 log 提示；
// decryptRules 不迁移、不改动。
func (pc *ProjectConfig) Migrate() bool {
	changed := false
	if len(pc.CaptureRules) > 0 || len(pc.ProcessRules) > 0 {
		changed = pc.migrateLegacyRules()
	}
	if pc.splitQuickIgnoreGroup() {
		changed = true
	}
	return changed
}

// splitQuickIgnoreGroup 拆分 M5 早期的混合内置黑名单组 _quick_ignore（hosts+processes 同组，
// 组内 AND 语义会令域名/进程忽略互相收窄）为两个独立组 _quick_ignore_hosts / _quick_ignore_procs
// （组间 OR）。幂等：拆分后旧组不存在，二次调用无操作。
func (pc *ProjectConfig) splitQuickIgnoreGroup() bool {
	qi := -1
	for i := range pc.FilterGroups {
		if pc.FilterGroups[i].ID == "_quick_ignore" {
			qi = i
			break
		}
	}
	if qi < 0 {
		return false
	}
	old := pc.FilterGroups[qi]
	// 移除旧组
	pc.FilterGroups = append(pc.FilterGroups[:qi], pc.FilterGroups[qi+1:]...)

	ensureGroup := func(id, name string) *rules.FilterGroup {
		for i := range pc.FilterGroups {
			if pc.FilterGroups[i].ID == id {
				return &pc.FilterGroups[i]
			}
		}
		pc.FilterGroups = append(pc.FilterGroups, rules.FilterGroup{
			ID: id, Name: name, Enabled: true, Mode: rules.ModeBlacklist,
		})
		return &pc.FilterGroups[len(pc.FilterGroups)-1]
	}
	mergeUniq := func(dst *[]string, src []string, caseInsensitive bool) {
		for _, v := range src {
			exist := false
			for _, x := range *dst {
				if (caseInsensitive && strings.EqualFold(x, v)) || (!caseInsensitive && x == v) {
					exist = true
					break
				}
			}
			if !exist {
				*dst = append(*dst, v)
			}
		}
	}
	if len(old.Hosts) > 0 || len(old.Paths) > 0 {
		g := ensureGroup("_quick_ignore_hosts", "快捷忽略-域名")
		mergeUniq(&g.Hosts, old.Hosts, false)
		mergeUniq(&g.Paths, old.Paths, false)
	}
	if len(old.Processes) > 0 {
		g := ensureGroup("_quick_ignore_procs", "快捷忽略-进程")
		mergeUniq(&g.Processes, old.Processes, true)
	}
	log.Printf("settings migrate: 已将混合组 _quick_ignore 拆分为 _quick_ignore_hosts / _quick_ignore_procs")
	return true
}

// migrateLegacyRules 旧 captureRules/processRules → filterGroups（规则设计 §六）。
// urlRe/method 维度丢弃（path glob + 展示层方法过滤替代）并 log 提示；decryptRules 不迁移、不改动。
func (pc *ProjectConfig) migrateLegacyRules() bool {
	// 来源 × 模式 四个迁移桶：同 action 旧条目合并进同一组（语义聚合）
	type bucket struct {
		name      string
		id        string
		mode      string
		hosts     []string
		processes []string
	}
	buckets := map[string]*bucket{
		"capture_black": {name: "旧捕获规则-黑名单(迁移)", id: "_migrated_capture_black", mode: rules.ModeBlacklist},
		"capture_white": {name: "旧捕获规则-白名单(迁移)", id: "_migrated_capture_white", mode: rules.ModeWhitelist},
		"process_black": {name: "旧进程规则-黑名单(迁移)", id: "_migrated_process_black", mode: rules.ModeBlacklist},
		"process_white": {name: "旧进程规则-白名单(迁移)", id: "_migrated_process_white", mode: rules.ModeWhitelist},
	}
	appendUniq := func(dst *[]string, v string) {
		for _, x := range *dst {
			if x == v {
				return
			}
		}
		*dst = append(*dst, v)
	}
	for _, r := range pc.CaptureRules {
		key := "capture_black"
		if r.Action == rules.ActionInclude {
			key = "capture_white"
		}
		if r.Host != "" {
			appendUniq(&buckets[key].hosts, r.Host)
		}
		if r.URLRe != "" || r.Method != "" {
			log.Printf("settings migrate: 旧捕获规则 %q 的 urlRe/method 维度已丢弃（path glob/展示层过滤替代）", r.Host)
		}
	}
	for _, r := range pc.ProcessRules {
		key := "process_black"
		if r.Action == rules.ActionInclude {
			key = "process_white"
		}
		if r.Name != "" {
			appendUniq(&buckets[key].processes, r.Name)
		}
	}
	// 固定顺序追加非空迁移组，保证输出确定性
	for _, key := range []string{"capture_black", "capture_white", "process_black", "process_white"} {
		b := buckets[key]
		if len(b.hosts)+len(b.processes) == 0 {
			continue
		}
		pc.FilterGroups = append(pc.FilterGroups, rules.FilterGroup{
			ID: b.id, Name: b.name, Enabled: true, Mode: b.mode,
			Hosts: b.hosts, Processes: b.processes,
		})
	}
	pc.CaptureRules = nil
	pc.ProcessRules = nil
	return true
}
