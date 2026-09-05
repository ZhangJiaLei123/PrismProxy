package capture

import (
	"fmt"
	"sort"
	"strings"
)

// Shell 类型：决定转义与换行风格
const (
	ShellCmd        = "cmd"        // Windows cmd：双引号包裹，"" 转义（MSVC CRT 解析为字面 "）
	ShellPowerShell = "powershell" // PowerShell：双引号包裹，"" 转义 + `/$ 专属转义
	ShellBash       = "bash"       // bash / zsh / Git Bash：单引号包裹，'\'' 转义
)

// MaxInlineBody cURL --data 内联 body 上限（64KB，方案 §4.9）；超限或二进制不内联
const MaxInlineBody = 64 << 10

// CurlResult BuildCurl 产物
type CurlResult struct {
	Command     string `json:"command"`
	BodyOmitted bool   `json:"bodyOmitted"` // body 二进制或超 64KB 未内联（提示从详情复制 Body）
}

// BuildCurl 按 Flow 请求生成可直接执行的 cURL 命令（方案 §4.9）。
// body 为原始捕获字节（不解压、不截断校验由调用方保证 Message.Body 即所发内容）；
// 文本且 ≤64KB 内联 --data，二进制/超限跳过并置 BodyOmitted。
func BuildCurl(f *Flow, shell string) (*CurlResult, error) {
	if f == nil || f.Request == nil {
		return nil, fmt.Errorf("该流无请求（盲透传隧道无明文请求），无法生成 cURL")
	}
	req := f.Request
	if req.URL == "" {
		return nil, fmt.Errorf("请求 URL 为空，无法生成 cURL")
	}
	if shell != ShellCmd && shell != ShellPowerShell && shell != ShellBash {
		return nil, fmt.Errorf("shell 须为 %q | %q | %q", ShellCmd, ShellPowerShell, ShellBash)
	}

	var quote func(string) string
	switch shell {
	case ShellCmd:
		// Windows 10+ curl 为 curl.exe 别名占位，显式 curl.exe 最稳
		quote = quoteCmd
	case ShellPowerShell:
		quote = quotePowerShell
	case ShellBash:
		quote = quoteBash
	}

	var parts []string
	parts = append(parts, quote(shellExe(shell)))
	parts = append(parts, quote(req.URL))

	if req.Method != "" && req.Method != "GET" {
		parts = append(parts, "-X", quote(req.Method))
	}

	// Headers：跳过逐跳头与由 curl/--data 自动维护的字段
	skipHeaders := map[string]bool{
		"Host":              true, // curl 从 URL 推导
		"Content-Length":    true, // --data 时 curl 自动重算
		"Connection":        true,
		"Proxy-Connection":  true,
		"Keep-Alive":        true,
		"Transfer-Encoding": true,
		"Upgrade":           true,
	}
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		if !skipHeaders[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header[k] {
			parts = append(parts, "-H", quote(k+": "+v))
		}
	}

	result := &CurlResult{}
	if len(req.Body) > 0 {
		if isText(req.Body) && len(req.Body) <= MaxInlineBody && !req.BodyTruncated {
			parts = append(parts, "--data-raw", quote(string(req.Body)))
		} else {
			result.BodyOmitted = true
		}
	}

	// 单行输出（Chrome DevTools 等工具导出的 cURL 均为单行）：续行符（cmd ^ /
	// PowerShell ` / bash \）在不同终端的粘贴兼容性差异大，单行彻底规避，
	// 且任何环境直接粘贴即可执行
	result.Command = strings.Join(parts, " ")
	return result, nil
}

func shellExe(shell string) string {
	if shell == ShellBash {
		return "curl"
	}
	// cmd 与 PowerShell 均显式 curl.exe（规避 PowerShell 5.1 中 curl 是 Invoke-WebRequest 别名）
	return "curl.exe"
}

// quoteCmd cmd 双引号包裹：内部 " → ""。
// cmd 侧相邻双引号只翻转引号状态两次（无害），curl.exe 的 MSVC CRT 解析把
// 引号内 "" 还原为单个字面 "；"" 同时也是 PowerShell 的字面引号转义，
// 故简单命令在 cmd 与 PowerShell 均可直接粘贴执行。
// 注意：cmd 交互模式对引号内的 %var% 仍会做变量展开，无法完美转义，
// URL/文本中孤立的 %xx（如 %20）不构成变量引用、不受影响。
func quoteCmd(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quotePowerShell PowerShell 双引号包裹：` → “、" → ""、$ → `$。
// PowerShell 双引号串内 ` 是转义符、$var/$() 会被展开，必须先行转义；
// 顺序保证：$ 替换引入的新 ` 不再参与后续替换。
func quotePowerShell(s string) string {
	s = strings.ReplaceAll(s, "`", "``")
	s = strings.ReplaceAll(s, `"`, `""`)
	s = strings.ReplaceAll(s, "$", "`$")
	return `"` + s + `"`
}

// quoteBash 单引号包裹：内部 ' → '\”（结束单引号、转义单引号、重开单引号）
func quoteBash(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isText 启发式判断字节是否为文本：前 1024 字节内 NUL 或高比例控制字符即视为二进制
func isText(b []byte) bool {
	n := len(b)
	if n > 1024 {
		n = 1024
	}
	binary := 0
	for i := 0; i < n; i++ {
		c := b[i]
		if c == 0 {
			return false
		}
		if c < 0x09 || (c > 0x0d && c < 0x20) || c == 0x7f {
			binary++
		}
	}
	return n == 0 || float64(binary)/float64(n) < 0.10
}
