package app

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/domains"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
)

// ---------- 快捷忽略 / 规则导入导出 / 解密绕过 Bindings（方案 §4.7-4.8） ----------
//
// M9 起规则（filterGroups/decryptRules）随项目：读写当前项目 project.json，
// 写操作持有 projMu（项目配置设计 §5.4）。

// 快捷忽略内置黑名单组（规则设计 §八）：域名与进程拆为两个独立组——
// 引擎组内维度为 AND，若混放同一组会令"忽略域名 X"与"忽略进程 Y"互相收窄
// （仅当 host=X 且 proc=Y 才过滤）；拆成两组后走组间 OR，任一命中即过滤。
const (
	QuickIgnoreHostGroupID = "_quick_ignore_hosts" // 快捷忽略-域名
	QuickIgnoreProcGroupID = "_quick_ignore_procs" // 快捷忽略-进程
	QuickIgnorePathGroupID = "_quick_ignore_paths" // 快捷忽略-路径
)

// AddQuickIgnore 一键忽略：target = host（裸域名=自身+全部子域）| process（进程名，精确不区分大小写）| path（路径，精确+段边界前缀，支持 *? 通配）。
// 分别写入内置黑名单组 _quick_ignore_hosts / _quick_ignore_procs / _quick_ignore_paths（不存在则自动创建），幂等去重；热更新 + 落盘。
// 返回 added=false 表示已存在未重复添加。
func (a *App) AddQuickIgnore(target, value string) (bool, error) {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	if a.proj == nil {
		return false, fmt.Errorf("尚未打开任何项目，请先从欢迎页新建或打开项目")
	}
	added, err := addQuickIgnoreTo(a.proj, target, value)
	if err != nil || !added {
		return added, err
	}
	if err := settings.SaveProjectConfig(a.cfgDir, a.proj); err != nil {
		return false, err
	}
	if err := a.rebuildEngine(); err != nil {
		return false, err
	}
	return true, nil
}

// addQuickIgnoreTo 快捷忽略的规则操作（纯函数，作用于给定项目配置；调用方负责落盘/热重建）。
// 供 AddQuickIgnore（当前项目）与 ctl RuleIgnore（目标项目，设计 §6.3）共用。
func addQuickIgnoreTo(pc *settings.ProjectConfig, target, value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, fmt.Errorf("忽略内容为空")
	}

	var groupID, groupName string
	var normalize func(string) (string, bool)
	var exists func(rules.FilterGroup, string) bool
	switch target {
	case "host":
		groupID, groupName = QuickIgnoreHostGroupID, "快捷忽略-域名"
		normalize = func(v string) (string, bool) {
			h := normalizeQuickIgnoreHost(v)
			return h, h != ""
		}
		exists = func(g rules.FilterGroup, v string) bool {
			for _, x := range g.Hosts {
				if x == v {
					return true
				}
			}
			return false
		}
	case "process":
		groupID, groupName = QuickIgnoreProcGroupID, "快捷忽略-进程"
		normalize = func(v string) (string, bool) { return v, true }
		exists = func(g rules.FilterGroup, v string) bool {
			for _, x := range g.Processes {
				if strings.EqualFold(x, v) {
					return true
				}
			}
			return false
		}
	case "path":
		groupID, groupName = QuickIgnorePathGroupID, "快捷忽略-路径"
		normalize = normalizeQuickIgnorePath
		exists = func(g rules.FilterGroup, v string) bool {
			for _, x := range g.Paths {
				if x == v {
					return true
				}
			}
			return false
		}
	default:
		return false, fmt.Errorf("target 须为 host|process|path")
	}

	nv, ok := normalize(value)
	if !ok {
		return false, fmt.Errorf("忽略内容无效：%q", value)
	}

	gi := -1
	for i := range pc.FilterGroups {
		if pc.FilterGroups[i].ID == groupID {
			gi = i
			break
		}
	}
	if gi < 0 {
		pc.FilterGroups = append(pc.FilterGroups, rules.FilterGroup{
			ID: groupID, Name: groupName, Enabled: true, Mode: rules.ModeBlacklist,
		})
		gi = len(pc.FilterGroups) - 1
	}
	g := &pc.FilterGroups[gi]
	if exists(*g, nv) {
		return false, nil // 幂等：已存在
	}
	switch target {
	case "host":
		g.Hosts = append(g.Hosts, nv)
	case "process":
		g.Processes = append(g.Processes, nv)
	case "path":
		g.Paths = append(g.Paths, nv)
	}
	return true, nil
}

// normalizeQuickIgnoreHost 忽略域名归一化：去端口（ServerAddr 可能带 :port）、小写、去尾点、去 *. 前缀
func normalizeQuickIgnoreHost(h string) string {
	h = strings.TrimSpace(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.ToLower(h)
	h = strings.TrimSuffix(h, ".")
	h = strings.TrimPrefix(h, "*.")
	return h
}

// normalizeQuickIgnorePath 忽略路径归一化：去空白、去 query/fragment、确保以 / 开头。
// "/" 单独一条在引擎里等价「匹配所有路径」，快捷忽略场景下拒绝以免一键过滤全部流量。
func normalizeQuickIgnorePath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/" {
		return "", false
	}
	return p, true
}

// rulesFile 规则导入导出 JSON 结构（方案 §4.7：含 version + 导出时间 + 规则三层 + 绕过列表）
type rulesFile struct {
	Version      int                 `json:"version"` // 当前 1
	ExportedAt   string              `json:"exportedAt,omitempty"`
	FilterGroups []rules.FilterGroup `json:"filterGroups"`
	DecryptRules []rules.DecryptRule `json:"decryptRules"`
	BypassList   []string            `json:"bypassList"`              // 全局环境设置；导出附带供参考，导入忽略
	Groups       map[string][]string `json:"groups,omitempty"`        // 内嵌引用域名组清单（可选）
}

// ExportRules 导出规则为 JSON 文件（弹保存对话框）；embedGroups=true 时内嵌规则 @引用到的域名组清单，
// 便于跨机分享。返回保存路径（用户取消返回空串）。
func (a *App) ExportRules(embedGroups bool) (string, error) {
	a.projMu.Lock()
	if a.proj == nil {
		a.projMu.Unlock()
		return "", fmt.Errorf("尚未打开任何项目，无规则可导出")
	}
	fg := append([]rules.FilterGroup(nil), a.proj.FilterGroups...)
	dr := append([]rules.DecryptRule(nil), a.proj.DecryptRules...)
	bp := append([]string(nil), a.gcfg.BypassList...)
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	a.projMu.Unlock()

	doc := rulesFile{
		Version:      1,
		ExportedAt:   time.Now().Format(time.RFC3339),
		FilterGroups: fg,
		DecryptRules: dr,
		BypassList:   bp,
	}
	if embedGroups {
		doc.Groups = map[string][]string{}
		for _, g := range fg {
			for _, h := range g.Hosts {
				if !strings.HasPrefix(h, "@") {
					continue
				}
				name := strings.TrimPrefix(h, "@")
				if _, ok := doc.Groups[name]; ok {
					continue
				}
				if list, ok := gmap[name]; ok {
					doc.Groups[name] = append([]string(nil), list...)
				}
			}
		}
		if len(doc.Groups) == 0 {
			doc.Groups = nil
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出规则",
		DefaultFilename: "prismproxy-rules.json",
		Filters:         []runtime.FileFilter{{DisplayName: "规则文件 (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return "", err
	}
	if dest == "" {
		return "", nil // 用户取消
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("写入文件: %w", err)
	}
	return dest, nil
}

// ImportRulesResult 规则导入结果：Warnings 为非阻塞提示（缺组等）
type ImportRulesResult struct {
	Warnings []string `json:"warnings"`
}

// ImportRules 从本地文件或 http(s) URL 导入规则（整体替换当前项目规则 + 校验，方案 §4.7）。
// src 为本地路径或 URL；src 为空时弹文件选择对话框。
// 内嵌域名组清单（groups）自动补建为用户域名组；引用缺失组不阻塞、以 warnings 返回。
// M9：bypassList 为全局环境设置，导入时忽略（以 warning 提示）。
func (a *App) ImportRules(src string) (*ImportRulesResult, error) {
	var data []byte
	src = strings.TrimSpace(src)
	switch {
	case src == "":
		file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
			Title:   "选择规则文件",
			Filters: []runtime.FileFilter{{DisplayName: "规则文件 (*.json)", Pattern: "*.json"}, {DisplayName: "所有文件 (*.*)", Pattern: "*.*"}},
		})
		if err != nil {
			return nil, err
		}
		if file == "" {
			return nil, nil // 用户取消
		}
		data, err = os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取文件: %w", err)
		}
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		var err error
		data, err = httpGet(src)
		if err != nil {
			return nil, err
		}
	default:
		var err error
		data, err = os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("读取文件: %w", err)
		}
	}

	data = stripUTF8BOM(data) // 容忍 Windows PowerShell 5.1 落盘的 UTF-8 BOM
	var doc rulesFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("不是有效的规则 JSON: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("不支持的规则文件版本: %d（当前支持版本 1）", doc.Version)
	}
	// 缺字段兜底为空切片（防 null 覆盖后前端渲染/引擎编译异常）
	if doc.FilterGroups == nil {
		doc.FilterGroups = []rules.FilterGroup{}
	}
	if doc.DecryptRules == nil {
		doc.DecryptRules = []rules.DecryptRule{}
	}

	a.projMu.Lock()
	defer a.projMu.Unlock()

	if a.proj == nil {
		return nil, fmt.Errorf("尚未打开任何项目，请先从欢迎页新建或打开项目")
	}

	// 内嵌域名组 → 补建用户域名组（当前项目目录；含内嵌清单则引用不再缺失）
	built := 0
	for id, list := range doc.Groups {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || len(list) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString("# 规则导入内嵌域名组：" + id + "\n")
		for _, d := range list {
			d = strings.TrimSpace(d)
			if d != "" {
				b.WriteString(d + "\n")
			}
		}
		if _, err := domains.WriteUser(a.userDomainsDir(), id, []byte(b.String())); err != nil {
			return nil, fmt.Errorf("补建内嵌域名组 %q: %w", id, err)
		}
		built++
	}

	var gmap map[string][]string
	if built > 0 {
		g, err := domains.LoadUser(a.userDomainsDir())
		if err != nil {
			return nil, err
		}
		a.groups = g
		gmap = g.Domains
	} else if a.groups != nil {
		gmap = a.groups.Domains
	}

	// 仅替换当前项目规则（环境字段不动，无需重启代理/调整存储预算）
	err, warns := settings.ValidateRules(doc.FilterGroups, doc.DecryptRules, gmap)
	if err != nil {
		return nil, err
	}
	if doc.BypassList != nil {
		warns = append(warns, "系统代理绕过列表为全局环境设置，导入时已忽略（请在「设置-常规」中修改）")
	}
	a.proj.FilterGroups = doc.FilterGroups
	a.proj.DecryptRules = doc.DecryptRules
	if err := settings.SaveProjectConfig(a.cfgDir, a.proj); err != nil {
		return nil, fmt.Errorf("保存项目配置: %w", err)
	}
	if err := a.rebuildEngine(); err != nil {
		return nil, err
	}
	return &ImportRulesResult{Warnings: warns}, nil
}

// AddDecryptBypass 一键排除域名：追加解密 bypass 规则并热生效（应对 SSL Pinning，列表右键入口）
func (a *App) AddDecryptBypass(host string) error {
	if host == "" {
		return fmt.Errorf("host 为空")
	}
	a.projMu.Lock()
	defer a.projMu.Unlock()
	if a.proj == nil {
		return fmt.Errorf("尚未打开任何项目，请先从欢迎页新建或打开项目")
	}
	a.proj.DecryptRules = append(a.proj.DecryptRules, rules.DecryptRule{Action: rules.ActionBypass, Host: host})
	if err := settings.SaveProjectConfig(a.cfgDir, a.proj); err != nil {
		return err
	}
	return a.rebuildEngine()
}

// ListDomainGroups 域名组清单（规则编辑器 @组名 引用候选）
func (a *App) ListDomainGroups() map[string]interface{} {
	a.projMu.Lock()
	g := a.groups
	a.projMu.Unlock()
	if g == nil {
		return map[string]interface{}{"names": []string{}, "meta": []domains.GroupMeta{}, "titles": map[string]string{}}
	}
	titles := g.Titles
	if titles == nil {
		titles = map[string]string{}
	}
	return map[string]interface{}{"names": g.Names(), "meta": g.Meta, "titles": titles}
}
