// Package rules 规则引擎：过滤规则组（黑白名单 + deny-override）+ 解密层平铺规则。
// 设计文档：doc/规则设计.md（唯一事实源）。
// 过滤层：组内维度 AND、同维度多条目 OR、组间 OR、黑名单恒优先；
// 信息缺失维度（进程未知 / 隧道无 path）按"维度移除"处理。
// 解密层：有序列表首条命中生效，默认 MITM。
package rules

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
)

// Action 规则动作。捕获/进程层（仅迁移代码引用）用 include|exclude；解密层用 mitm|bypass
type Action string

const (
	ActionInclude Action = "include"
	ActionExclude Action = "exclude"
	ActionMITM    Action = "mitm"
	ActionBypass  Action = "bypass"
)

// 过滤规则组模式（规则设计 §2.2）
const (
	ModeBlacklist = "blacklist" // 命中 → 不显示（默认）
	ModeWhitelist = "whitelist" // 命中 → 只显示这些
)

// FilterGroup 过滤规则组：一组域名/路径/进程集合，可整组启停（规则设计 §2.1）
type FilterGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Mode      string   `json:"mode"` // blacklist | whitelist
	Hosts     []string `json:"hosts"`
	Paths     []string `json:"paths"`
	Processes []string `json:"processes"`
}

// CaptureRule 旧捕获规则（引擎不再使用，仅供 settings 迁移代码引用）
type CaptureRule struct {
	Action Action `json:"action"`
	Host   string `json:"host"`
	URLRe  string `json:"urlRe"`
	Method string `json:"method"`
}

// DecryptRule 解密规则：命中 bypass 则 CONNECT 盲透传（应对 SSL Pinning）
type DecryptRule struct {
	Action Action `json:"action"` // mitm | bypass
	Host   string `json:"host"`
}

// ProcessRule 旧进程规则（引擎不再使用，仅供 settings 迁移代码引用）
type ProcessRule struct {
	Action Action `json:"action"`
	Name   string `json:"name"`
}

// Engine 编译后的规则集合（并发只读；整体替换式热更新）
type Engine struct {
	fgroups []filterCompiled
	decrypt []DecryptRule
	groups  map[string][]string // 内置域名组：仅供解密层 @组名 引用（CONNECT 一次判定，非热路径）
}

type filterCompiled struct {
	FilterGroup
	hosts []string      // hosts 编译期展开产物：@组名 预展开为裸域名清单（引用缺失展开为空）
	paths []pathMatcher // paths 编译产物（双形态）
}

// pathMatcher 双形态（规则设计 §三）：无通配符条目免正则
type pathMatcher struct {
	exact string         // 无通配符条目非空：==/HasPrefix 字符串比较（段边界）
	re    *regexp.Regexp // 含通配符条目非空
}

// NewEngine 编译过滤规则组与解密规则；groups 为域名组（@组名 引用），可为 nil
func NewEngine(filterGroups []FilterGroup, decRules []DecryptRule, groups map[string][]string) (*Engine, error) {
	e := &Engine{decrypt: decRules, groups: groups}
	for i, g := range filterGroups {
		fc := filterCompiled{FilterGroup: g}
		if g.Mode != ModeBlacklist && g.Mode != ModeWhitelist {
			return nil, fmt.Errorf("过滤规则组 #%d %q: 非法模式 %q", i+1, g.Name, g.Mode)
		}
		if strings.TrimSpace(g.Name) == "" {
			return nil, fmt.Errorf("过滤规则组 #%d: 组名不能为空", i+1)
		}
		for _, h := range g.Hosts {
			if err := validateHostEntry(h); err != nil {
				return nil, fmt.Errorf("过滤规则组 %q: %w", g.Name, err)
			}
			if strings.HasPrefix(h, "@") {
				fc.hosts = append(fc.hosts, expandGroup(groups, strings.TrimPrefix(h, "@"))...)
			} else {
				fc.hosts = append(fc.hosts, normalizeHost(h))
			}
		}
		for _, p := range g.Paths {
			pm, err := compilePathEntry(p)
			if err != nil {
				return nil, fmt.Errorf("过滤规则组 %q 路径 %q: %w", g.Name, p, err)
			}
			fc.paths = append(fc.paths, pm)
		}
		for _, pr := range g.Processes {
			if strings.TrimSpace(pr) == "" {
				return nil, fmt.Errorf("过滤规则组 %q: 进程名条目不能为空", g.Name)
			}
		}
		e.fgroups = append(e.fgroups, fc)
	}
	for i, r := range decRules {
		if err := validateAction(r.Action, ActionMITM, ActionBypass); err != nil {
			return nil, fmt.Errorf("解密规则 #%d: %w", i+1, err)
		}
		if r.Host == "" {
			return nil, fmt.Errorf("解密规则 #%d: host 不能为空", i+1)
		}
	}
	return e, nil
}

func validateAction(a Action, allowed ...Action) error {
	for _, x := range allowed {
		if a == x {
			return nil
		}
	}
	return fmt.Errorf("非法动作 %q", a)
}

// validateHostEntry hosts 条目校验：非空、不允许裸 *（语义过宽必是手误）
func validateHostEntry(h string) error {
	h = strings.TrimSpace(h)
	if h == "" {
		return fmt.Errorf("域名条目不能为空")
	}
	if h == "*" || h == "*." {
		return fmt.Errorf("域名条目不允许裸 *")
	}
	return nil
}

// normalizeHost 编译期规范化：小写 + 去尾部点 + *. 前缀等价裸域名
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	return strings.TrimPrefix(h, "*.")
}

// expandGroup 展开 @组名 引用为规范化裸域名清单（引用缺失展开为空）
func expandGroup(groups map[string][]string, name string) []string {
	var out []string
	for _, d := range groups[name] {
		out = append(out, normalizeHost(d))
	}
	return out
}

// compilePathEntry 路径条目编译：含 * / ? → glob 正则；否则字符串比较形态
func compilePathEntry(p string) (pathMatcher, error) {
	if strings.ContainsAny(p, "*?") {
		re, err := regexp.Compile(globToRe(p))
		if err != nil {
			return pathMatcher{}, fmt.Errorf("glob 编译失败: %v", err)
		}
		return pathMatcher{re: re}, nil
	}
	return pathMatcher{exact: p}, nil
}

// globToRe * → .*（跨 /）、? → .、其余 QuoteMeta，整体锚定 ^...$
func globToRe(g string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range g {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return b.String()
}

func (m pathMatcher) match(path string) bool {
	if m.re != nil {
		return m.re.MatchString(path)
	}
	if m.exact == "/" {
		return true // "/" 单独一条 = 匹配所有路径
	}
	return path == m.exact || strings.HasPrefix(path, m.exact+"/")
}

// match 组对流判定（维度移除，规则设计 §2.3）：
// 缺失维度（path 为空 / procName 为空）从约束集移除；剩余约束全命中才算命中；
// 剩余约束为空 → 不匹配。
func (g *filterCompiled) match(host, path, procName string) bool {
	checked := false
	if len(g.hosts) > 0 {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		ok := false
		for _, d := range g.hosts {
			if bareMatch(d, host) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
		checked = true
	}
	if len(g.paths) > 0 && path != "" {
		ok := false
		for _, pm := range g.paths {
			if pm.match(path) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
		checked = true
	}
	if len(g.Processes) > 0 && procName != "" {
		ok := false
		for _, pr := range g.Processes {
			if strings.EqualFold(pr, procName) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
		checked = true
	}
	return checked
}

// isActiveWhitelist 静态谓词（规则设计 §2.2，UI 状态条须同口径实现）
func (g *FilterGroup) isActiveWhitelist() bool {
	return g.Enabled && g.Mode == ModeWhitelist &&
		len(g.Hosts)+len(g.Paths)+len(g.Processes) > 0
}

// ShouldDisplay 返回该流是否显示（黑名单/白名单 + deny-override，规则设计 §2.2）
func (e *Engine) ShouldDisplay(host, rawURL, procName string) bool {
	if e == nil {
		return true
	}
	path := extractPath(rawURL)
	hasWhitelist := false
	whitelistHit := false
	for i := range e.fgroups {
		g := &e.fgroups[i]
		if !g.Enabled {
			continue
		}
		if g.Mode == ModeBlacklist {
			if g.match(host, path, procName) {
				return false // 黑名单最高优先，拒绝覆盖允许
			}
			continue
		}
		if g.isActiveWhitelist() {
			hasWhitelist = true
			if g.match(host, path, procName) {
				whitelistHit = true
			}
		}
	}
	if hasWhitelist {
		return whitelistHit
	}
	return true // 无启用中的白名单组 → 默认全显示
}

// extractPath 从 rawURL 提取 path；解析失败或 path 为空（隧道流）→ 空串走维度移除
func extractPath(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Path
}

// ShouldDecrypt 解密判定（CONNECT host 维度）：默认 MITM
func (e *Engine) ShouldDecrypt(host string) bool {
	if e == nil {
		return true
	}
	for _, r := range e.decrypt {
		if e.hostMatch(r.Host, host) {
			return r.Action == ActionMITM
		}
	}
	return true
}

// hostMatch 域名匹配：裸域名匹配自身+全部子域；*.x.com 等价裸域名；@组名 引用域名组
func (e *Engine) hostMatch(pattern, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || host == "" {
		return false
	}
	if strings.HasPrefix(pattern, "@") {
		for _, d := range e.groups[strings.TrimPrefix(pattern, "@")] {
			if bareMatch(d, host) {
				return true
			}
		}
		return false
	}
	return bareMatch(strings.TrimPrefix(pattern, "*."), host)
}

func bareMatch(domain, host string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// Holder 引擎热更新容器（保存设置时整体替换，读侧零锁）
type Holder struct{ p atomic.Pointer[Engine] }

func (h *Holder) Get() *Engine  { return h.p.Load() }
func (h *Holder) Set(e *Engine) { h.p.Store(e) }
