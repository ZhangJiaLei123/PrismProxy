package sysproxy

import "strings"

// MergeOverride 合并 ProxyOverride：用户已有值在前 + 绕过列表去重追加；
// <-loopback> 固定在首位（硬要求：绝不覆盖用户已有值，方案 §4.6）。
// 纯函数，跨平台共享（单测用）。
func MergeOverride(existing string, bypass []string) string {
	seen := make(map[string]struct{})
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		k := strings.ToLower(v)
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	for _, v := range strings.Split(existing, ";") {
		add(v)
	}
	for _, v := range bypass {
		add(v)
	}
	// <-loopback> 提首
	for i, v := range out {
		if strings.EqualFold(v, "<-loopback>") {
			copy(out[1:i+1], out[0:i])
			out[0] = v
			break
		}
	}
	return strings.Join(out, ";")
}
