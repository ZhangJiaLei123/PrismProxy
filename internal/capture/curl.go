package capture

import (
	"fmt"
	"sort"
	"strings"
)

// Shell 类型：决定转义与换行风格（2026-09-10 起只支持 cmd/bash，PowerShell 已删）
const (
	ShellCmd  = "cmd"  // Windows cmd：^" 包裹，对齐 Chrome DevTools escapeStringWin
	ShellBash = "bash" // bash / zsh / Git Bash：单引号/ANSI-C，对齐 DevTools escapeStringPosix
)

// MaxInlineBody cURL --data-raw 内联 body 上限（64KB，方案 §4.9）；超限或二进制不内联。
// 前端 frontend/src/lib/curl.ts 共用同一阈值与判定，改规则时两侧必须同步。
const MaxInlineBody = 64 << 10

// CurlResult BuildCurl 产物
type CurlResult struct {
	Command     string `json:"command"`
	BodyOmitted bool   `json:"bodyOmitted"` // body 二进制或超 64KB 未内联（提示从详情复制 Body）
}

// skipCurlHeaders 不输出到 cURL 的首部（小写比较）：代理逐跳/由 curl 自动维护头 +
// accept-encoding（对齐 Chrome DevTools ignoredHeaders）。
// Connection/Proxy-Connection/Keep-Alive/Transfer-Encoding/Upgrade 代理层记录前已剥离
// （见 proxy/server.go removeHopHeaders），此处保留仅为防御；host 由 curl 按 URL 补，
// content-length 让 curl 按 --data-raw 自动算，accept-encoding 让 curl 自己协商。
var skipCurlHeaders = map[string]bool{
	"connection":        true,
	"proxy-connection":  true,
	"keep-alive":        true,
	"transfer-encoding": true,
	"host":              true,
	"content-length":    true,
	"accept-encoding":   true,
}

// BuildCurl 按 Flow 请求生成可直接粘贴执行的 cURL 命令，形态对齐 Chrome 152 DevTools
// 「Copy as cURL」（前端 lib/curl.ts 同规则，改规则时两侧同步）：
//   - 首段裸 `curl --url <URL>`（可执行名不加引号；URL 内 {} [] 加反斜杠防 globbing）；
//   - 方法推断基于「是否真输出 data 段」：有内联 body→POST，否则 GET，不一致才输出 -X
//     （二进制/超限 body 被省略时，POST 会显式补 -X POST，否则 curl 误发 GET）；
//   - 头按 key ASCII 字典序；cookie 且值含 '=' 改用 -b（无 '=' 回退 -H 防 curl 读文件）；
//     值纯空白输出 -H 'k;'；
//   --data-raw 压尾；参数段 ≥3 才多行（cmd ' ^\n  ' / bash ' \\\n  '，两空格缩进），否则单行。
//
// body 为原始捕获字节（不解压）；无 NUL 且 ≤64KB 且未截断才内联，否则跳过并置 BodyOmitted。
func BuildCurl(f *Flow, shell string) (*CurlResult, error) {
	if f == nil || f.Request == nil {
		return nil, fmt.Errorf("该流无请求（盲透传隧道无明文请求），无法生成 cURL")
	}
	req := f.Request
	if req.URL == "" {
		return nil, fmt.Errorf("请求 URL 为空，无法生成 cURL")
	}
	if shell != ShellCmd && shell != ShellBash {
		return nil, fmt.Errorf("shell 须为 %q | %q", ShellCmd, ShellBash)
	}

	var quote func(string) string
	if shell == ShellCmd {
		quote = quoteCmd
	} else {
		quote = quoteBash
	}

	// 先判定 body 能否内联：方法推断依据「是否有 data 段」，必须在推断前算好。
	result := &CurlResult{}
	inlineBody := ""
	hasBody := false
	if len(req.Body) > 0 {
		hasBody = true
		if isText(req.Body) && len(req.Body) <= MaxInlineBody && !req.BodyTruncated {
			inlineBody = string(req.Body) // 原始 UTF-8 字节，非法序列按 RuneError 保留
		} else {
			result.BodyOmitted = true
		}
	}

	lines := []string{"curl --url " + escapeCurlURL(quote(req.URL))}

	method := strings.ToUpper(req.Method)
	inferred := "GET"
	if hasBody && !result.BodyOmitted {
		inferred = "POST"
	}
	if method != "" && method != inferred {
		lines = append(lines, "-X "+quote(method))
	}

	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		if !skipCurlHeaders[strings.ToLower(k)] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header[k] {
			if strings.TrimSpace(v) == "" {
				// 空白值：curl 语法中分号表示空头
				lines = append(lines, "-H "+quote(k+";"))
				continue
			}
			if strings.EqualFold(k, "Cookie") && strings.Contains(v, "=") {
				lines = append(lines, "-b "+quote(v))
				continue
			}
			lines = append(lines, "-H "+quote(k+": "+v))
		}
	}

	if hasBody && !result.BodyOmitted {
		// 两平台均用 --data-raw：原样保留反斜杠等，不做 @ 文件展开
		lines = append(lines, "--data-raw "+quote(inlineBody))
	}

	sep := " "
	if len(lines) >= 3 {
		if shell == ShellCmd {
			sep = " ^\n  "
		} else {
			sep = " \\\n  "
		}
	}
	result.Command = strings.Join(lines, sep)
	return result, nil
}

// cmdWhitelist 为 cmd 转义中无需加 ^ 的可打印 ASCII（对齐 DevTools 白名单并含 ()：
// main 源码白名单不含括号，但 Chrome 152 实测 UA 中括号无 ^，以真实样例为准）。
const cmdWhitelist = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _-:=+~/'.,?;*()"

// quoteCmd 逐字节移植 Chrome DevTools escapeStringWin（顺序不可调换）：
//  1. \ → \\、" → \"（先过 MS Crt 解析器）；
//  2. 白名单之外的每个 ASCII 可打印字符（\x21-\x7e）前加 ^（过 cmd.exe 解析器），
//     故 "→^\^"、{→^{、|→^|、`→^`；
//  3. 后跟字母/数字/下划线的 % 改 %^，避免 MS Crt 当环境变量展开；
//  4. 其余控制字符（含 TAB/DEL）→ 空格防命令注入；换行→ ^ 加两个换行（cmd 续行转义）；
//  5. 整体 ^"..."^ 包裹。
//
// 与 DevTools 唯一刻意差异：非 ASCII（>=0x80，如中文）原样保留，不加 ^ 也不替换空格
// ——替换会毁掉中文重放；现代 Windows Terminal/cmd 按活动代码页仍可粘贴执行。
func quoteCmd(s string) string {
	// 1) 反斜杠与双引号
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)

	// 2) 白名单外的可打印 ASCII 前加 ^（逐字节，>=0x80 不动）
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x21 && c <= 0x7e && !strings.ContainsRune(cmdWhitelist, rune(c)) {
			b.WriteByte('^')
		}
		b.WriteByte(c)
	}
	s = b.String()

	// 3) 后跟标识符字符的 % 防环境变量展开（用加 ^ 前的下一字节判定，^ 不是标识符）
	b.Reset()
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		b.WriteByte(s[i])
		if s[i] == '%' && i+1 < len(s) && isIdentByte(s[i+1]) {
			b.WriteByte('^')
		}
	}
	s = b.String()

	// 4) 控制字符（含 TAB/DEL，不含换行）→ 空格
	b.Reset()
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 && c != '\n' && c != '\r' || c == 0x7f {
			b.WriteByte(' ')
		} else {
			b.WriteByte(c)
		}
	}
	s = b.String()

	// 换行→ ^ 加两个换行（\r\n 中的 \r 随后变 ^，整体与 DevTools 形态一致）
	s = strings.ReplaceAll(s, "\r\n", "^\n\n")
	s = strings.ReplaceAll(s, "\r", "^\n\n")
	s = strings.ReplaceAll(s, "\n", "^\n\n")
	return `^"` + s + `^"`
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// quoteBash 移植 DevTools escapeStringPosix（按 rune 处理，避免 UTF-8 续接字节
// 0x80-0x9f 被误判为控制字符）：含控制字符/DEL-0x9f/!/单引号时用 ANSI-C 引用
// $'...'（!→\u0021 防历史展开、控制字符→\uXXXX，\n/\r 用 \n/\r），否则普通单引号。
func quoteBash(s string) string {
	needsAnsiC := strings.ContainsRune(s, '!') || strings.ContainsRune(s, '\'')
	if !needsAnsiC {
		for _, r := range s {
			if r < 0x20 || r >= 0x7f && r <= 0x9f {
				needsAnsiC = true
				break
			}
		}
	}
	if !needsAnsiC {
		return "'" + s + "'"
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	b.WriteString("$'")
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\'':
			b.WriteString(`\'`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '!':
			b.WriteString(`\u0021`)
		case r < 0x20 || r >= 0x7f && r <= 0x9f:
			b.WriteString(fmt.Sprintf(`\u%04x`, r))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("'")
	return b.String()
}

// escapeCurlURL DevTools 在 quote 之后对 URL 统一处理：{ } [ ] 前加反斜杠防 curl globbing
// （两平台同一规则；cmd 里 [ 不在白名单会先被加 ^，最终恰为 ^\[，与 DevTools 样例一致）。
func escapeCurlURL(quoted string) string {
	var b strings.Builder
	for i := 0; i < len(quoted); i++ {
		if quoted[i] == '{' || quoted[i] == '}' || quoted[i] == '[' || quoted[i] == ']' {
			b.WriteByte('\\')
		}
		b.WriteByte(quoted[i])
	}
	return b.String()
}

// isText 判断 body 是否可内联为文本：含 NUL 即二进制（对齐前端 lib/curl.ts 判定）。
func isText(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			return false
		}
	}
	return true
}
