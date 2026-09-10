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
	// PowerShell 已删除
	if _, err := BuildCurl(f, "powershell"); err == nil {
		t.Fatal("powershell 应不再是合法 shell")
	}
}

func TestBuildCurl_BasicGet(t *testing.T) {
	f := newReqFlow("GET", "http://example.com/path?q=1")

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	wantCmd := `curl --url ^"http://example.com/path?q=1^"`
	if cmd.Command != wantCmd {
		t.Fatalf("cmd 单行形态不一致:\n got: %q\nwant: %q", cmd.Command, wantCmd)
	}

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	wantBash := `curl --url 'http://example.com/path?q=1'`
	if bash.Command != wantBash {
		t.Fatalf("bash 单行形态不一致:\n got: %q\nwant: %q", bash.Command, wantBash)
	}
}

func TestBuildCurl_MethodInferenceAndMultiline(t *testing.T) {
	// 无 body 的 POST：推断为 GET，不一致 → 显式 -X POST；
	// 4 个 -H → 共 6 段，必须多行（cmd ^ / bash \ 续行，两空格缩进）。
	f := newReqFlow("POST", "https://api.example.com/v1/items")
	f.Request.Header.Set("Host", "api.example.com")         // 应跳过
	f.Request.Header.Set("Content-Length", "42")            // 应跳过
	f.Request.Header.Set("Accept-Encoding", "gzip")         // 应跳过（DevTools ignoredHeaders）
	f.Request.Header.Set("Authorization", "Bearer abc 123") // 含空格
	f.Request.Header.Add("X-Multi", "v1")
	f.Request.Header.Add("X-Multi", "v2")
	f.Request.Header.Set("Accept", "application/json")

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Command, "-X ^\"POST^\"") {
		t.Fatalf("无 body 的 POST 应显式 -X POST: %q", cmd.Command)
	}
	for _, skip := range []string{"Host:", "Content-Length:", "Accept-Encoding:"} {
		if strings.Contains(cmd.Command, skip) {
			t.Fatalf("应跳过头 %s: %q", skip, cmd.Command)
		}
	}
	if !strings.Contains(cmd.Command, "^\"Authorization: Bearer abc 123^\"") {
		t.Fatalf("含空格 header 应在 ^\" 内: %q", cmd.Command)
	}
	if !strings.Contains(cmd.Command, "^\"X-Multi: v1^\"") || !strings.Contains(cmd.Command, "^\"X-Multi: v2^\"") {
		t.Fatalf("多值 header 应逐值输出: %q", cmd.Command)
	}
	// header 按 key 排序：Accept 在 Authorization 前
	ai := strings.Index(cmd.Command, "Accept")
	au := strings.Index(cmd.Command, "Authorization")
	if ai < 0 || au < 0 || ai > au {
		t.Fatalf("header 应按 key 排序输出: %q", cmd.Command)
	}
	// ≥3 段多行：续行符 ^ 后紧跟换行、次行两空格缩进
	sep := " ^\n  "
	if strings.Count(cmd.Command, sep) != 5 {
		t.Fatalf("6 段应有 5 个 cmd 续行分隔符: %q", cmd.Command)
	}

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(bash.Command, " \\\n  ") != 5 {
		t.Fatalf("6 段应有 5 个 bash 续行分隔符: %q", bash.Command)
	}

	// 两段仍为单行（不足 3 段不换行）
	g := newReqFlow("DELETE", "http://a.com/x")
	two, err := BuildCurl(g, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(two.Command, "\n") {
		t.Fatalf("仅 2 段不应多行: %q", two.Command)
	}
	if !strings.Contains(two.Command, "-X ^\"DELETE^\"") {
		t.Fatalf("DELETE 应显式 -X: %q", two.Command)
	}
}

func TestBuildCurl_CmdEscapeStringWin(t *testing.T) {
	// 双引号：\" 后再加 ^，整体 ^" 包裹
	f := newReqFlow("GET", `http://a.com/p?x="hi"`)
	f.Request.Header.Set("X-Q", `say "yo"`)

	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Command, `^"http://a.com/p?x=^\^"hi^\^"^"`) {
		t.Fatalf("URL 内双引号应为 ^\\^\"（与 Chrome 样例一致）: %q", cmd.Command)
	}
	if !strings.Contains(cmd.Command, `^"X-Q: say ^\^"yo^\^"^"`) {
		t.Fatalf("header 内双引号应为 ^\\^\": %q", cmd.Command)
	}

	// { } | 等非白名单 ASCII 加 ^（对齐用户 Chrome 152 样例：^{ 与 ^|）
	j := newReqFlow("POST", "http://a.com/api")
	j.Request.Header.Set("Content-Type", "application/json")
	j.Request.Header.Set("Sec-Ch-Ua", `"Chromium";v="152"`)
	j.Request.Body = []byte(`{"datas":"a|b"}`)
	got, err := BuildCurl(j, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Command, `^{^\^"datas^\^":^\^"a^|b^\^"^}`) {
		t.Fatalf(`JSON body 应对齐 DevTools 样例（^{ ^| ^\^" 形态）: %q`, got.Command)
	}
	if !strings.Contains(got.Command, `^\^"Chromium^\^"`) {
		t.Fatalf("sec-ch-ua 引号形态异常: %q", got.Command)
	}

	// % 不在白名单先加 ^ 成 ^%；后跟标识符时再补一个 ^ 防环境变量展开
	p := newReqFlow("GET", "http://a.com/%PATH%")
	pc, err := BuildCurl(p, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pc.Command, `^%^PATH^%`) {
		t.Fatalf("%% 后随标识符应防展开（^%%^NAME^%%）: %q", pc.Command)
	}

	// 白名单含 ()：UA 中括号不加 ^（以 Chrome 152 真实样例为准）
	u := newReqFlow("GET", "http://a.com/")
	u.Request.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) Safari")
	uc, err := BuildCurl(u, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uc.Command, "^(") || strings.Contains(uc.Command, "^)") {
		t.Fatalf("白名单 () 不应加 ^: %q", uc.Command)
	}

	// 非 ASCII（中文）原样保留：不加 ^、不替换空格
	c := newReqFlow("GET", "https://x.com/搜索?q=1")
	cc, err := BuildCurl(c, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cc.Command, "https://x.com/搜索?q=1") {
		t.Fatalf("中文 URL 应原样保留: %q", cc.Command)
	}
}

func TestBuildCurl_BashQuoting(t *testing.T) {
	// 普通串：单引号包裹
	f := newReqFlow("GET", "http://a.com/p")
	plain, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain.Command, "curl --url 'http://a.com/p'") {
		t.Fatalf("普通串应用单引号: %q", plain.Command)
	}

	// 含单引号：ANSI-C $'...'，单引号转 \'
	g := newReqFlow("GET", `http://a.com/p?x=it's`)
	g.Request.Header.Set("X-Q", `don't go`)
	ansi, err := BuildCurl(g, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ansi.Command, `$'http://a.com/p?x=it\'s'`) {
		t.Fatalf("bash 含单引号应走 ANSI-C: %q", ansi.Command)
	}
	if !strings.Contains(ansi.Command, `$'X-Q: don\'t go'`) {
		t.Fatalf("bash header 含单引号应走 ANSI-C: %q", ansi.Command)
	}

	// 含 !：\u0021 防历史展开
	h := newReqFlow("GET", "http://a.com/")
	h.Request.Header.Set("X-T", "a!b")
	ex, err := BuildCurl(h, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ex.Command, `$'X-T: a\u0021b'`) {
		t.Fatalf("bash ! 应转 \\u0021: %q", ex.Command)
	}
}

func TestBuildCurl_UrlGlobbingEscape(t *testing.T) {
	// bash：{ } [ ] 前统一加 \
	f := newReqFlow("GET", `http://a.com/a[b]{1}`)
	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bash.Command, `'http://a.com/a\[b\]\{1\}'`) {
		t.Fatalf("bash URL 应防 globbing: %q", bash.Command)
	}

	// cmd：[ 先被加 ^ 再被插 \，恰为 ^\[
	cmd, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`a^\[b^\]`, `^\{1^\}`} {
		if !strings.Contains(cmd.Command, frag) {
			t.Fatalf("cmd URL 转义缺 %s: %q", frag, cmd.Command)
		}
	}
}

func TestBuildCurl_CookieAndEmptyHeader(t *testing.T) {
	f := newReqFlow("GET", "http://a.com/")
	f.Request.Header.Set("Cookie", "sid=abc; a=1") // 含 '=' → -b
	withEq, err := BuildCurl(f, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withEq.Command, "-b ^\"sid=abc; a=1^\"") || strings.Contains(withEq.Command, "Cookie:") {
		t.Fatalf("含 '=' 的 Cookie 应用 -b: %q", withEq.Command)
	}

	g := newReqFlow("GET", "http://a.com/")
	g.Request.Header.Set("Cookie", "sessiontoken") // 无 '=' → 回退 -H 防 curl 读文件
	noEq, err := BuildCurl(g, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(noEq.Command, "-H ^\"Cookie: sessiontoken^\"") {
		t.Fatalf("无 '=' 的 Cookie 应回退 -H: %q", noEq.Command)
	}

	h := newReqFlow("GET", "http://a.com/")
	h.Request.Header.Set("X-Empty", "   ") // 纯空白 → 'k;'
	empty, err := BuildCurl(h, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.Command, "-H ^\"X-Empty;^\"") {
		t.Fatalf("纯空白头应输出 k;: %q", empty.Command)
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
	// 有内联 body → 推断 POST，不应再输出 -X
	if strings.Contains(cmd.Command, "-X ") {
		t.Fatalf("有内联 body 的 POST 不应输出 -X: %q", cmd.Command)
	}
	want := `--data-raw ^"^{^\^"name^\^":^\^"张三^\^",^\^"ok^\^":true^}^"`
	if !strings.Contains(cmd.Command, want) {
		t.Fatalf("cmd body 转义形态异常:\n got: %q\nwant 含: %q", cmd.Command, want)
	}

	bash, err := BuildCurl(f, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bash.Command, `--data-raw '{"name":"张三","ok":true}'`) {
		t.Fatalf("bash body 单引号包裹即可: %q", bash.Command)
	}
}

func TestBuildCurl_BodyOmittedKeepsMethod(t *testing.T) {
	// 二进制 body：省略 data 段，但必须显式 -X POST，否则 curl 默认发 GET
	f := newReqFlow("POST", "http://a.com/upload")
	f.Request.Body = []byte{0x00, 0x01, 0x02, 0xff, 0xfe}
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
	if !strings.Contains(cmd.Command, "-X ^\"POST^\"") {
		t.Fatalf("省略 body 的 POST 必须显式 -X POST: %q", cmd.Command)
	}

	// 超限
	g := newReqFlow("POST", "http://a.com/upload")
	body := make([]byte, MaxInlineBody+1)
	for i := range body {
		body[i] = 'a'
	}
	g.Request.Body = body
	big, err := BuildCurl(g, ShellBash)
	if err != nil {
		t.Fatal(err)
	}
	if !big.BodyOmitted || !strings.Contains(big.Command, "-X 'POST'") {
		t.Fatalf("超限 body 应省略且保留 -X POST: %q", big.Command)
	}

	// 已截断
	h := newReqFlow("POST", "http://a.com/upload")
	h.Request.Body = []byte(`{"small":true}`)
	h.Request.BodyTruncated = true
	tr, err := BuildCurl(h, ShellCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.BodyOmitted || !strings.Contains(tr.Command, "-X ^\"POST^\"") {
		t.Fatalf("截断 body 应省略且保留 -X POST: %q", tr.Command)
	}
}

func TestIsText(t *testing.T) {
	if !isText([]byte("hello 世界\n\t")) {
		t.Fatal("普通文本（含中文/换行/TAB）应判定为文本")
	}
	if isText([]byte("abc\x00def")) {
		t.Fatal("含 NUL 应判定为二进制")
	}
	// 对齐前端 lib/curl.ts：只按 NUL 判定，少量控制字符仍视为文本
	if !isText([]byte{0x01, 0x02, 0x03}) {
		t.Fatal("无 NUL 的控制字符序列应判定为文本（与前端一致）")
	}
}
