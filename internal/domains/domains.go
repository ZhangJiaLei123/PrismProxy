// Package domains 域名组加载：一个 txt = 一个组（文件名即组名），
// 每行一个域名，# 注释与空行忽略（含行尾 # 行内注释）。
// 数据源见 PrismProxy/domains/，构建时经 go:embed 打入单 exe。
package domains

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GroupMeta 域名组元数据（index.json 登记项；域名清单以 txt 为唯一事实源）
type GroupMeta struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Note     string `json:"note"`
}

// Groups 域名组集合：组名 → 域名列表（均小写）
type Groups struct {
	Domains map[string][]string
	Meta    []GroupMeta
	Custom  map[string]bool   // 用户导入的组（userDir 来源；同 id 时覆盖内置组）
	Titles  map[string]string // 各 txt 标准头部「# 域名组：…」标题行（缺失时 UI 回退 id）
}

// Load 从 fsys 的 dir 目录加载 index.json 与全部 txt。
// index.json 缺失/损坏不致命（降级为仅按文件名建组）。
func Load(fsys fs.FS, dir string) (*Groups, error) {
	g := &Groups{Domains: make(map[string][]string), Titles: make(map[string]string)}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read domains dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".txt") {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		id := strings.TrimSuffix(name, ".txt")
		g.Domains[id] = Parse(data)
		if t := ParseTitle(data); t != "" {
			g.Titles[id] = t
		}
	}

	if data, err := fs.ReadFile(fsys, path.Join(dir, "index.json")); err == nil {
		var idx struct {
			Groups []GroupMeta `json:"groups"`
		}
		if json.Unmarshal(data, &idx) == nil {
			g.Meta = idx.Groups
		}
	}
	return g, nil
}

// LoadMerged 先加载内嵌域名组，再叠加用户目录（如 %APPDATA%/PrismProxy/domains）的
// 自定义组（导入落盘的 txt）；同 id 时自定义组覆盖内置组。userDir 为空/不存在等同 Load。
func LoadMerged(fsys fs.FS, dir, userDir string) (*Groups, error) {
	g, err := Load(fsys, dir)
	if err != nil {
		return nil, err
	}
	g.Custom = make(map[string]bool)
	if g.Titles == nil {
		g.Titles = make(map[string]string)
	}
	if userDir == "" {
		return g, nil
	}
	entries, err := os.ReadDir(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			return g, nil
		}
		return nil, fmt.Errorf("read user domains dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(userDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		id := strings.TrimSuffix(name, ".txt")
		g.Domains[id] = Parse(data)
		g.Custom[id] = true
		if t := ParseTitle(data); t != "" {
			g.Titles[id] = t
		} else {
			delete(g.Titles, id) // 覆盖内置组时旧标题不残留
		}
	}
	return g, nil
}

// LoadUser 仅加载用户目录下的自定义域名组（程序不再内嵌任何域名组，
// domains/ 源码目录仅作 git 分发的导入源，不 go:embed 进 exe）。所有组均标记 Custom=true。
func LoadUser(userDir string) (*Groups, error) {
	g := &Groups{Domains: make(map[string][]string), Custom: make(map[string]bool), Titles: make(map[string]string)}
	if userDir == "" {
		return g, nil
	}
	entries, err := os.ReadDir(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			return g, nil
		}
		return nil, fmt.Errorf("read user domains dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(userDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		id := strings.TrimSuffix(name, ".txt")
		g.Domains[id] = Parse(data)
		g.Custom[id] = true
		if t := ParseTitle(data); t != "" {
			g.Titles[id] = t
		}
	}
	return g, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateID 组 id 即 txt 文件名与 @引用名：小写字母/数字开头，仅含小写字母/数字/连字符
func ValidateID(id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("组 ID %q 非法：需以小写字母或数字开头，仅含小写字母/数字/连字符", id)
	}
	return nil
}

// WriteUser 把导入内容落盘为 userDir/<id>.txt（保留原文注释/格式），返回解析出的域名条数
func WriteUser(userDir, id string, data []byte) (int, error) {
	if err := ValidateID(id); err != nil {
		return 0, err
	}
	data = stripBOM(data) // 剥 BOM：容忍带 BOM 导入且落盘文件不残留 BOM（Parse 内部也会剥，这里为落盘干净）
	list := Parse(data)
	if len(list) == 0 {
		return 0, fmt.Errorf("未解析到任何域名（每行一个域名，# 为注释）")
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(userDir, id+".txt"), data, 0o644); err != nil {
		return 0, err
	}
	return len(list), nil
}

// DeleteUser 删除 userDir/<id>.txt（仅自定义组；内置组在内嵌资源中不受影响）
func DeleteUser(userDir, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(userDir, id+".txt"))
	if os.IsNotExist(err) {
		return fmt.Errorf("自定义域名组 %q 不存在", id)
	}
	return err
}

// stripBOM 去掉开头的 UTF-8 BOM（Windows PowerShell 5.1 落盘 txt 常带 BOM，
// 会污染首行域名/标题）。
func stripBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
}

// ParseTitle 提取 txt 标准头部标题行「# 域名组：<名称>」（首个命中即返回）。
// 标准格式见 domains/alipay.txt 头部注释；无该头的第三方文件返回空串（UI 回退组 id）。
func ParseTitle(data []byte) string {
	data = stripBOM(data)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return "" // 首个非注释行之前未命中即无标准头
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if t, ok := strings.CutPrefix(body, "域名组："); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// Parse 解析 txt：每行一个域名，# 注释/空行忽略，统一小写，去重保序
func Parse(data []byte) []string {
	data = stripBOM(data)
	seen := make(map[string]struct{})
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		// *.x.com 通配等价裸域名（语义同方案 §4.7）
		line = strings.TrimPrefix(line, "*.")
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

// Names 返回排序后的组名列表（UI 展示用）
func (g *Groups) Names() []string {
	out := make([]string, 0, len(g.Domains))
	for k := range g.Domains {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
