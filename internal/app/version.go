package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// baseVersion 基础版本号（不含 v 前缀与编译时间戳），由开发人员发版时手动 bump。
// 窗口标题只显示该部分（v2.0.0）；版本比较也以它为基准。
const baseVersion = "2.0.0"

// Version 基础版本号（无 v 前缀），窗口标题拼接为 "PrismProxy v" + Version。
const Version = baseVersion

// buildTime 编译时间戳（格式 yyyyMMddHHmmss），由 build.bat 经
// -ldflags "-X prismproxy/internal/app.buildTime=<时间戳>" 注入；
// 未注入（go run / wails dev / 直接 go build）时为 dev。
var buildTime = "dev"

// FullVersion 完整版本号：v2.0.0-<编译时间戳>（设置面板「关于」显示）；
// 未注入时间戳时为 v2.0.0-dev。
func FullVersion() string {
	return "v" + baseVersion + "-" + buildTime
}

// githubRepo 检查更新用的仓库（releases/latest API）
const githubRepo = "ZhangJiaLei123/PrismProxy"

// GetVersion 返回完整版本号（前端设置面板显示）：v2.0.0-<编译时间戳>
func (a *App) GetVersion() string {
	return FullVersion()
}

// UpdateInfo 检查更新结果
type UpdateInfo struct {
	HasUpdate bool   `json:"hasUpdate"` // 远端版本号更大
	Current   string `json:"current"`   // 当前完整版本（含 v 前缀与编译时间戳）
	Latest    string `json:"latest"`    // 最新 Release tag（如 v2.0.1）
	URL       string `json:"url"`       // Release 页面地址
	Notes     string `json:"notes"`     // Release 说明（截断）
}

// CheckUpdate 查询 GitHub 最新 Release 并与当前基础版本比较（无鉴权公开 API，10s 超时）。
// 网络失败返回 error 由前端展示；版本比较只看 baseVersion，编译时间戳不参与。
func (a *App) CheckUpdate() (*UpdateInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+githubRepo+"/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回 %s（可能触发限流）", resp.Status)
	}

	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("仓库尚无 Release")
	}

	notes := strings.TrimSpace(rel.Body)
	if len(notes) > 500 {
		notes = notes[:500] + "…"
	}
	info := &UpdateInfo{
		Current: FullVersion(),
		Latest:  rel.TagName,
		URL:     rel.HTMLURL,
		Notes:   notes,
	}
	info.HasUpdate = compareSemver(rel.TagName, baseVersion) > 0
	return info, nil
}

// compareSemver 比较三段数字版本号（容忍 v 前缀与 -预发布后缀）：
// a>b 返回 1，a<b 返回 -1，相等返回 0。非数字部分按 0 处理。
func compareSemver(a, b string) int {
	pa, pb := parseSemver(a), parseSemver(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseSemver(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "v"), "V")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i] // 丢弃预发布后缀（v1.2.2-beta 按 1.2.2 比较）
	}
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out
}
