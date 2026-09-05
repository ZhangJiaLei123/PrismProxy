package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/domains"
)

// ---------- 域名组管理（设置面板：导入/导出/删除，即时生效） ----------

// DomainGroupInfo 域名组管理列表项
type DomainGroupInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"` // 显示名：优先 txt 标准头部「# 域名组：…」，回退 index.json，再回退 id
	Category string `json:"category"`
	Count    int    `json:"count"`
	Custom   bool   `json:"custom"` // 用户导入（同 id 覆盖内置组）
}

// displayName 解析域名组展示名：txt 头部标题 > index.json 中文名 > 组 id
func groupDisplayName(id string, titles map[string]string, meta map[string]domains.GroupMeta) string {
	if t := titles[id]; t != "" {
		return t
	}
	if m, ok := meta[id]; ok && m.Name != "" {
		return m.Name
	}
	return id
}

// DomainGroupImportResult 导入结果
type DomainGroupImportResult struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func (a *App) userDomainsDir() string { return filepath.Join(a.cfgDir, "domains") }

// ListDomainGroupDetails 域名组管理列表（含条数与自定义标记，按 id 排序）
func (a *App) ListDomainGroupDetails() []DomainGroupInfo {
	a.mu.Lock()
	g := a.groups
	a.mu.Unlock()
	if g == nil {
		return []DomainGroupInfo{}
	}
	meta := make(map[string]domains.GroupMeta, len(g.Meta))
	for _, m := range g.Meta {
		meta[m.ID] = m
	}
	titles := g.Titles
	out := make([]DomainGroupInfo, 0, len(g.Domains))
	for id, list := range g.Domains {
		info := DomainGroupInfo{ID: id, Name: groupDisplayName(id, titles, meta), Count: len(list), Custom: g.Custom[id]}
		if m, ok := meta[id]; ok {
			info.Category = m.Category
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ImportDomainGroupFile 本地导入：弹文件对话框选 txt，id 为空取文件名（去扩展名）。用户取消返回 nil
func (a *App) ImportDomainGroupFile(id string) (*DomainGroupImportResult, error) {
	file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择域名组文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "域名组文本 (*.txt)", Pattern: "*.txt"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if file == "" {
		return nil, nil // 用户取消
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("读取文件: %w", err)
	}
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	return a.importDomains(id, data)
}

// ImportDomainGroupURL URL 导入：拉取远程 txt（限 4MB），id 为空取 URL 路径文件名
func (a *App) ImportDomainGroupURL(rawurl, id string) (*DomainGroupImportResult, error) {
	u, err := parseHTTPURL(rawurl)
	if err != nil {
		return nil, err
	}
	data, err := httpGet(rawurl)
	if err != nil {
		return nil, err
	}
	if id == "" {
		base := path.Base(u.Path)
		id = strings.TrimSuffix(base, path.Ext(base))
	}
	return a.importDomains(id, data)
}

// ---------- URL 导入：索引（index.json）支持 ----------

// DomainIndexEntry 索引文件中的单个域名组条目
type DomainIndexEntry struct {
	ID       string `json:"id"`
	File     string `json:"file"` // 相对索引 URL 的组文件路径
	Name     string `json:"name"`
	Category string `json:"category"`
}

// URLImportProbe URL 探测结果：kind = txt（直接域名组文件）| index（索引文件）
type URLImportProbe struct {
	Kind    string             `json:"kind"`
	Entries []DomainIndexEntry `json:"entries,omitempty"`
}

// IndexImportResult 索引批量导入单项结果
type IndexImportResult struct {
	ID      string `json:"id"`
	Count   int    `json:"count"`
	Skipped bool   `json:"skipped,omitempty"` // 本地已存在同名组且选择跳过
	Err     string `json:"err,omitempty"`
}

// domainIndex index.json 结构（仅取导入所需字段）
type domainIndex struct {
	Groups []DomainIndexEntry `json:"groups"`
}

// parseHTTPURL 校验 URL 仅支持 http/https
func parseHTTPURL(rawurl string) (*url.URL, error) {
	u, err := url.Parse(rawurl)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("URL 非法（仅支持 http/https）")
	}
	return u, nil
}

// httpGet 拉取远程内容（20s 超时，限 4MB）
func httpGet(rawurl string) ([]byte, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(rawurl)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}
	return data, nil
}

// ProbeURLImport 探测 URL 内容：能解析为含 groups 数组的 JSON 视为索引，否则视为直接域名组 txt
func (a *App) ProbeURLImport(rawurl string) (*URLImportProbe, error) {
	if _, err := parseHTTPURL(rawurl); err != nil {
		return nil, err
	}
	data, err := httpGet(rawurl)
	if err != nil {
		return nil, err
	}
	var idx domainIndex
	if err := json.Unmarshal(data, &idx); err == nil && idx.Groups != nil {
		entries := make([]DomainIndexEntry, 0, len(idx.Groups))
		for _, e := range idx.Groups {
			if e.ID == "" || e.File == "" {
				continue
			}
			entries = append(entries, e)
		}
		return &URLImportProbe{Kind: "index", Entries: entries}, nil
	}
	return &URLImportProbe{Kind: "txt"}, nil
}

// ImportDomainGroupsFromIndex 按勾选的 id 从索引 URL 批量下载域名组并导入（各组文件相对索引 URL 解析）。
// overwrite=false 时本地已存在的同名组（config/domains/<id>.txt）跳过不下载、不覆盖。
func (a *App) ImportDomainGroupsFromIndex(rawurl string, ids []string, overwrite bool) ([]IndexImportResult, error) {
	base, err := parseHTTPURL(rawurl)
	if err != nil {
		return nil, err
	}
	data, err := httpGet(rawurl)
	if err != nil {
		return nil, err
	}
	var idx domainIndex
	if err := json.Unmarshal(data, &idx); err != nil || idx.Groups == nil {
		return nil, fmt.Errorf("不是有效的索引文件（index.json）")
	}
	byID := make(map[string]DomainIndexEntry, len(idx.Groups))
	for _, e := range idx.Groups {
		byID[e.ID] = e
	}
	// 去重并得到总数（进度条用）
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	total := len(unique)
	results := make([]IndexImportResult, 0, total)
	for i, id := range unique {
		res := IndexImportResult{ID: id}
		emitImportProgress(a.ctx, i, total, id, false)
		entry, ok := byID[id]
		if !ok {
			res.Err = "索引中不存在该组"
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		ref, err := url.Parse(entry.File)
		if err != nil {
			res.Err = "索引中文件路径非法"
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		if !overwrite {
			if _, err := os.Stat(filepath.Join(a.userDomainsDir(), id+".txt")); err == nil {
				res.Skipped = true
				results = append(results, res)
				emitImportProgress(a.ctx, i+1, total, id, false)
				continue
			}
		}
		txt, err := httpGet(base.ResolveReference(ref).String())
		if err != nil {
			res.Err = err.Error()
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		n, err := domains.WriteUser(a.userDomainsDir(), id, txt)
		if err != nil {
			res.Err = err.Error()
			results = append(results, res)
			emitImportProgress(a.ctx, i+1, total, id, false)
			continue
		}
		res.Count = n
		results = append(results, res)
		emitImportProgress(a.ctx, i+1, total, id, false)
	}
	emitImportProgress(a.ctx, total, total, "", true)
	if err := a.reloadGroups(); err != nil {
		return results, fmt.Errorf("热更新失败: %w", err)
	}
	return results, nil
}

// emitImportProgress 发射索引批量导入进度事件（前端 n-progress 监听 index-import-progress）
func emitImportProgress(ctx context.Context, current, total int, id string, done bool) {
	runtime.EventsEmit(ctx, "index-import-progress", map[string]interface{}{
		"current": current,
		"total":   total,
		"id":      id,
		"done":    done,
	})
}

// importDomains 校验并落盘导入内容，随后重载域名组 + 热更新规则引擎
func (a *App) importDomains(id string, data []byte) (*DomainGroupImportResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	n, err := domains.WriteUser(a.userDomainsDir(), id, data)
	if err != nil {
		return nil, err
	}
	if err := a.reloadGroups(); err != nil {
		return nil, err
	}
	return &DomainGroupImportResult{ID: id, Count: n}, nil
}

// ExportDomainGroup 导出域名组到文件（弹保存对话框）；返回保存路径（用户取消返回空串）
func (a *App) ExportDomainGroup(id string) (string, error) {
	data, err := a.groupRaw(id)
	if err != nil {
		return "", err
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出域名组",
		DefaultFilename: id + ".txt",
		Filters:         []runtime.FileFilter{{DisplayName: "域名组文本 (*.txt)", Pattern: "*.txt"}},
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

// groupRaw 取组原始文本（保留注释原貌）：所有组均为用户导入，读用户目录文件
func (a *App) groupRaw(id string) ([]byte, error) {
	a.mu.Lock()
	g := a.groups
	a.mu.Unlock()
	if g == nil {
		return nil, fmt.Errorf("域名组不可用")
	}
	if _, ok := g.Domains[id]; !ok {
		return nil, fmt.Errorf("域名组 %q 不存在", id)
	}
	return os.ReadFile(filepath.Join(a.userDomainsDir(), id+".txt"))
}

// DeleteDomainGroup 删除自定义域名组（内置组不可删除；覆盖同名内置组的删除后内置组恢复生效）
func (a *App) DeleteDomainGroup(id string) error {
	a.mu.Lock()
	custom := a.groups != nil && a.groups.Custom[id]
	a.mu.Unlock()
	if !custom {
		return fmt.Errorf("内置域名组不可删除")
	}
	if err := domains.DeleteUser(a.userDomainsDir(), id); err != nil {
		return err
	}
	return a.reloadGroups()
}

// GetDomainGroupText 取域名组原始文本（含注释/格式）：自定义组读用户目录，内置组读内嵌资源
func (a *App) GetDomainGroupText(id string) (string, error) {
	data, err := a.groupRaw(id)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveDomainGroupText 保存编辑后的域名组文本（落盘为自定义组，同 id 覆盖内置组），随后热更新
func (a *App) SaveDomainGroupText(id, content string) (*DomainGroupImportResult, error) {
	return a.importDomains(id, []byte(content))
}

// reloadGroups 重载用户导入的域名组并热更新规则引擎
func (a *App) reloadGroups() error {
	g, err := domains.LoadUser(a.userDomainsDir())
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.groups = g
	a.mu.Unlock()
	return a.rebuildEngine()
}
