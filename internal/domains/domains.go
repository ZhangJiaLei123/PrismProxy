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
	"path"
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
}

// Load 从 fsys 的 dir 目录加载 index.json 与全部 txt。
// index.json 缺失/损坏不致命（降级为仅按文件名建组）。
func Load(fsys fs.FS, dir string) (*Groups, error) {
	g := &Groups{Domains: make(map[string][]string)}

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

// Parse 解析 txt：每行一个域名，# 注释/空行忽略，统一小写，去重保序
func Parse(data []byte) []string {
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
