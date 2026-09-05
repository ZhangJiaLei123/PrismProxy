package capture

import (
	"net/http"
	"strings"
	"testing"
)

func newReqFlow(method, url string) *Flow {
	return &Flow{
		ID: "t-1",
		Request: &Message{
			Method: method,
			URL:    url,
			Header: http.Header{},
		},
	}
}

func TestBuildCurl_Errors(t *testing.T) {
	if _, err := BuildCurl(nil, ShellCmd); err == nil {
		t.Fatal("nil flow 应报错")
	}
	if _, err := BuildCurl(&Flow{ID: "x"}, ShellCmd); err == nil {
		t.Fatal("nil request 应报错（盲透传隧道）")
	}
	f := newReqFlow("GET", "")
	if _, err := BuildCurl(f, ShellCmd); err == nil {
		t.Fatal("空 URL 应报错")
	}
	f = newReqFlow("GET", "http://a.com")
	if _, err := BuildCurl(f, "zsh"); err == nil {
		t.Fatal("非法 shell 应报错")
	}
}

func TestBuildCurl_BasicGet(t *testing.T) {
	f := newReqFlow("GET", "http://example.com/path?q=1")

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cmd.Command, `"curl.exe"`) {
		t.Fatalf("cmd 应以 curl.exe 开头: %q", cmd.Command)
	}
	if !strings.Contains(cmd.Command, `"http://example.com/path?q=1"`) {
		t.Fatalf("cmd 应含引号包裹的 URL: %q", cmd.Command)
	}
	if strings.Contains(cmd.Command, "-X") {
		t.Fatalf("GET 不应有 -X: %q", cmd.Command)
	}
	if strings.Contains(cmd.Command, "\n") {
		t.Fatalf("单参数命令不应有续行: %q", cmd.Command)
	}

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bash.Command, `'curl'`) {
		t.Fatalf("bash 应以 'curl' 开头: %q", bash.Command)
	}
	if !strings.Contains(bash.Command, `'http://example.com/path?q=1'`) {
		t.Fatalf("bash 应含单引号包裹的 URL: %q", bash.Command)
	}
}

func TestBuildCurl_MethodAndHeaders(t *testing.T) {
	f := newReqFlow("POST", "https://api.example.com/v1/items")
	f.Request.Header.Set("Host", "api.example.com")         // 应跳过
	f.Request.Header.Set("Content-Length", "42")            // 应跳过
	f.Request.Header.Set("Authorization", "Bearer abc 123") // 含空格
	f.Request.Header.Add("X-Multi", "v1")
	f.Request.Header.Add("X-Multi", "v2")
	f.Request.Header.Set("Accept", "application/json")

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Command, "-X") || !strings.Contains(cmd.Command, `"POST"`) {
		t.Fatalf("POST 应有 -X POST: %q", cmd.Command)
	}
	for _, skip := range []string{"Host:", "Content-Length:"} {
		if strings.Contains(cmd.Command, skip) {
			t.Fatalf("应跳过逐跳头 %s: %q", skip, cmd.Command)
		}
	}
	if !strings.Contains(cmd.Command, `"Authorization: Bearer abc 123"`) {
		t.Fatalf("含空格 header 应整体双引号包裹: %q", cmd.Command)
	}
	if !strings.Contains(cmd.Command, `"X-Multi: v1"`) || !strings.Contains(cmd.Command, `"X-Multi: v2"`) {
		t.Fatalf("多值 header 应逐值输出: %q", cmd.Command)
	}
	// header 按 key 排序：Accept 在 Authorization 前
	ai := strings.Index(cmd.Command, "Accept")
	au := strings.Index(cmd.Command, "Authorization")
	if ai < 0 || au < 0 || ai > au {
		t.Fatalf("header 应按 key 排序输出: %q", cmd.Command)
	}
	// 单行输出：任何终端直接粘贴执行
	if strings.Contains(cmd.Command, "\n") {
		t.Fatalf("命令应为单行输出: %q", cmd.Command)
	}
	if !strings.Contains(cmd.Command, `"POST" -H "Accept: application/json"`) {
		t.Fatalf("cmd 参数间应以单空格连接: %q", cmd.Command)
	}

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bash.Command, "\n") {
		t.Fatalf("命令应为单行输出: %q", bash.Command)
	}
}

func TestBuildCurl_CmdQuoteDoubleQuote(t *testing.T) {
	f := newReqFlow("GET", `http://a.com/p?x="hi"`)
	f.Request.Header.Set("X-Q", `say "yo"`)

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	// 双引号应转义为 ""（cmd/PowerShell 双兼容：MSVC CRT 与 PS 均还原为字面 "）
	if !strings.Contains(cmd.Command, `"http://a.com/p?x=""hi"""`) {
		t.Fatalf("cmd 内双引号应转义为 \"\": %q", cmd.Command)
	}
	if !strings.Contains(cmd.Command, `"X-Q: say ""yo"""`) {
		t.Fatalf("cmd header 内双引号应转义: %q", cmd.Command)
	}
}

func TestBuildCurl_PowerShell(t *testing.T) {
	f := newReqFlow("POST", "https://a.com/api")
	f.Request.Header.Set("X-Quote", `say "yo"`)
	f.Request.Header.Set("X-Dollar", "cost $5 and `tick`")
	f.Request.Body = []byte(`{"a":1}`)

	ps, err := BuildCurl(f, ShellPowerShell)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ps.Command, `"curl.exe"`) {
		t.Fatalf("PowerShell 应显式 curl.exe（规避 Invoke-WebRequest 别名）: %q", ps.Command)
	}
	// 单行输出，无续行符
	if strings.Contains(ps.Command, "\n") {
		t.Fatalf("命令应为单行输出: %q", ps.Command)
	}
	// 双引号 → ""
	if !strings.Contains(ps.Command, `"X-Quote: say ""yo"""`) {
		t.Fatalf("PowerShell 双引号应转义为 \"\": %q", ps.Command)
	}
	// 反引号 → ``、$ → `$
	if !strings.Contains(ps.Command, "cost `$5 and ``tick``") {
		t.Fatalf("PowerShell $ 与反引号应转义: %q", ps.Command)
	}
	// JSON body 双引号 → ""
	if !strings.Contains(ps.Command, `"{""a"":1}"`) {
		t.Fatalf("PowerShell body 内双引号应转义: %q", ps.Command)
	}

	// 非法 shell 校验覆盖 powershell 之外的分支已测；此处确认 powershell 合法
	f2 := newReqFlow("GET", "http://a.com")
	if _, err := BuildCurl(f2, ShellPowerShell); err != nil {
		t.Fatal(err)
	}
}

func TestBuildCurl_BashQuoteSingleQuote(t *testing.T) {
	f := newReqFlow("GET", `http://a.com/p?x=it's`)
	f.Request.Header.Set("X-Q", `don't go`)

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	// 单引号应转义为 '\''
	if !strings.Contains(bash.Command, `'http://a.com/p?x=it'\''s'`) {
		t.Fatalf("bash 内单引号应转义为 '\\'' : %q", bash.Command)
	}
	if !strings.Contains(bash.Command, `'X-Q: don'\''t go'`) {
		t.Fatalf("bash header 内单引号应转义: %q", bash.Command)
	}
}

func TestBuildCurl_BodyInline(t *testing.T) {
	f := newReqFlow("POST", "http://a.com/api")
	f.Request.Header.Set("Content-Type", "application/json")
	f.Request.Body = []byte(`{"name":"张三","ok":true}`)

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.BodyOmitted {
		t.Fatal("文本小 body 不应省略")
	}
	if !strings.Contains(cmd.Command, "--data-raw") {
		t.Fatalf("文本 body 应内联 --data-raw: %q", cmd.Command)
	}
	// cmd 双引号风格：JSON 内的双引号转义为 ""
	if !strings.Contains(cmd.Command, `"{""name"":""张三"",""ok"":true}"`) {
		t.Fatalf("cmd body 内双引号应转义: %q", cmd.Command)
	}

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bash.Command, `'{"name":"张三","ok":true}'`) {
		t.Fatalf("bash body 单引号包裹无需转义双引号: %q", bash.Command)
	}
}

func TestBuildCurl_BodyBinaryOmitted(t *testing.T) {
	f := newReqFlow("POST", "http://a.com/upload")
	f.Request.Body = []byte{0x00, 0x01, 0x02, 0xff, 0xfe} // 含 NUL = 二进制

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.BodyOmitted {
		t.Fatal("二进制 body 应置 BodyOmitted")
	}
	if strings.Contains(cmd.Command, "--data-raw") {
		t.Fatalf("二进制 body 不应内联: %q", cmd.Command)
	}
}

func TestBuildCurl_BodyTooLargeOmitted(t *testing.T) {
	f := newReqFlow("POST", "http://a.com/upload")
	body := make([]byte, MaxInlineBody+1)
	for i := range body {
		body[i] = 'a' // 纯文本但超限
	}
	f.Request.Body = body

	cmd, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.BodyOmitted {
		t.Fatal("超 64KB body 应置 BodyOmitted")
	}
	if strings.Contains(cmd.Command, "--data-raw") {
		t.Fatalf("超限 body 不应内联: %q", cmd.Command)
	}
}

func TestBuildCurl_BodyTruncatedOmitted(t *testing.T) {
	f := newReqFlow("POST", "http://a.com/upload")
	f.Request.Body = []byte(`{"small":true}`)
	f.Request.BodyTruncated = true // 捕获被截断，内联会丢数据

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.BodyOmitted {
		t.Fatal("截断 body 应置 BodyOmitted")
	}
}

func TestIsText(t *testing.T) {
	if !isText([]byte("hello 世界\n\t")) {
		t.Fatal("普通文本应判定为文本")
	}
	if isText([]byte("abc\x00def")) {
		t.Fatal("含 NUL 应判定为二进制")
	}
	if isText([]byte{0x01, 0x02, 0x03}) {
		t.Fatal("高比例控制字符应判定为二进制")
	}
	// 允许少量控制字符（<10%）
	mostlyText := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\x01")
	if !isText(mostlyText) {
		t.Fatal("仅 1/31 控制字符应仍为文本")
	}
}
