package ai

import (
	"strings"
	"testing"
	"time"
)

func mkFlow(id string, at time.Time, method, url string) FlowInput {
	return FlowInput{FlowID: id, StartedAt: at, Method: method, URL: url, StatusCode: 200, DurationMS: 12}
}

// TestDefaultBodies 四模式默认正文开关。
func TestDefaultBodies(t *testing.T) {
	cases := []struct {
		m        ChatMode
		req, rsp bool
	}{
		{ModeExplain, true, true},
		{ModeIntent, false, false},
		{ModeLocate, false, false},
		{ModeFlowmap, true, true},
	}
	for _, tc := range cases {
		req, rsp := DefaultBodies(tc.m)
		if req != tc.req || rsp != tc.rsp {
			t.Errorf("DefaultBodies(%s)=(%v,%v) want (%v,%v)", tc.m, req, rsp, tc.req, tc.rsp)
		}
	}
}

// TestSystemPrompts 四模式 system 模板关键差异 + 用户目标前缀。
func TestSystemPrompts(t *testing.T) {
	checks := map[ChatMode]string{
		ModeExplain: "鉴权方式",
		ModeIntent:  "intents",
		ModeLocate:  "matches",
		ModeFlowmap: "业务链路",
	}
	for m, kw := range checks {
		sp := systemPrompt(m)
		if !strings.Contains(sp, "只基于提供的证据") || !strings.Contains(sp, kw) {
			t.Errorf("%s system 缺关键内容", m)
		}
	}
	r := BuildPrompt(nil, BuildOptions{Mode: ModeLocate, Question: "  找下单接口  "})
	if !strings.HasPrefix(r.User, "用户目标：找下单接口\n\n") {
		t.Fatalf("Question 前缀缺失：%q", r.User[:30])
	}
}

// TestBuildPromptOrderAndSeq 排序：StartedAt 降序，seq=1 最新。
func TestBuildPromptOrderAndSeq(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	flows := []FlowInput{
		mkFlow("f_mid", base.Add(1*time.Minute), "GET", "http://a/2"),
		mkFlow("f_new", base.Add(2*time.Minute), "POST", "http://a/3"),
		mkFlow("f_old", base, "GET", "http://a/1"),
	}
	r := BuildPrompt(flows, BuildOptions{Mode: ModeExplain, IncludeReqBody: true, IncludeRespBody: true})
	if r.Total != 3 || r.Sent != 3 || r.Truncated {
		t.Fatalf("meta=%+v", r)
	}
	i1 := strings.Index(r.User, "[#1] flowId=f_new")
	i2 := strings.Index(r.User, "[#2] flowId=f_mid")
	i3 := strings.Index(r.User, "[#3] flowId=f_old")
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Fatalf("seq 顺序错误：%d %d %d", i1, i2, i3)
	}
}

// TestBuildPromptTruncateFlows AC11 锚点：60 流截前 50，最老被截掉。
func TestBuildPromptTruncateFlows(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	flows := make([]FlowInput, 60)
	for i := 0; i < 60; i++ { // i=0 最老
		flows[i] = mkFlow(fID(i), base.Add(time.Duration(i)*time.Minute), "GET", "http://a/"+fID(i))
	}
	r := BuildPrompt(flows, BuildOptions{Mode: ModeIntent}) // MaxFlows/MaxKB 走默认
	if r.Total != 60 || r.Sent != 50 || !r.Truncated {
		t.Fatalf("meta=%+v", r)
	}
	if !strings.Contains(r.User, "下面是 50 条抓包记录") {
		t.Fatal("头行条数错误")
	}
	if strings.Contains(r.User, "flowId=f00") {
		t.Fatal("最老流 f00 应被截掉")
	}
	if !strings.Contains(r.User, "flowId=f49") || !strings.Contains(r.User, "flowId=f59") {
		t.Fatal("最新 50 条应保留")
	}
}

func fID(i int) string {
	if i < 10 {
		return "f0" + string(rune('0'+i))
	}
	return "f" + string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// TestBuildPromptBodyTruncate 单流 34KB：8KB/侧 截断 + 原始 KB 标注。
func TestBuildPromptBodyTruncate(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	body := strings.Repeat("a", 34*1024) // 34816 字节
	flows := []FlowInput{{
		FlowID: "f1", StartedAt: base, Method: "POST", URL: "http://a/x", StatusCode: 200,
		DurationMS: 12, ReqCT: "application/json", RespCT: "application/json",
		ReqBody: []byte(body), RespBody: []byte(body),
	}}
	r := BuildPrompt(flows, BuildOptions{Mode: ModeExplain, IncludeReqBody: true, IncludeRespBody: true})
	if r.Truncated || r.Sent != 1 || r.Total != 1 {
		t.Fatalf("meta=%+v", r)
	}
	if got := strings.Count(r.User, "…（已截断，原始 34 KB）"); got != 2 {
		t.Fatalf("截断标注=%d 次 want 2（请求+响应各一）", got)
	}
	// 每侧 8192 + 标注 34 字节 → 16452B → ceil = 17KB
	if r.SentKB != 17 {
		t.Fatalf("SentKB=%d want 17", r.SentKB)
	}
}

// TestBuildPromptGreedyDrop 总超限贪心丢侧：最老流先丢、先丢响应体再丢请求体。
// 50 流 × 2 侧 × 1024B = 102400 > 65536 → 丢最老 18 流（seq 33..50），剩 32 流，SentKB=64。
func TestBuildPromptGreedyDrop(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	flows := make([]FlowInput, 50)
	for i := 0; i < 50; i++ {
		flows[i] = FlowInput{
			FlowID: fID(i), StartedAt: base.Add(time.Duration(i) * time.Minute),
			Method: "POST", URL: "http://a/" + fID(i), StatusCode: 200, DurationMS: 12,
			ReqCT: "application/json", RespCT: "application/json",
			ReqBody: []byte(strings.Repeat("a", 1024)), RespBody: []byte(strings.Repeat("b", 1024)),
		}
	}
	r := BuildPrompt(flows, BuildOptions{Mode: ModeFlowmap, IncludeReqBody: true, IncludeRespBody: true})
	if got := strings.Count(r.User, "请求正文:"); got != 32 {
		t.Fatalf("请求正文段=%d want 32", got)
	}
	if got := strings.Count(r.User, "响应正文:"); got != 32 {
		t.Fatalf("响应正文段=%d want 32", got)
	}
	if r.SentKB != 64 {
		t.Fatalf("SentKB=%d want 64", r.SentKB)
	}
	tail := r.User[strings.Index(r.User, "[#49]"):]
	if strings.Contains(tail, "请求正文") || strings.Contains(tail, "响应正文") {
		t.Fatal("最老流（seq 49）正文应被丢弃")
	}
	if seg := r.User[strings.Index(r.User, "[#32]"):strings.Index(r.User, "[#33]")]; !strings.Contains(seg, "请求正文") {
		t.Fatal("seq 32 边界流正文应保留")
	}
}

// TestHeaderRedact 头脱敏结构性无条件：不受 Redact 开关影响。
func TestHeaderRedact(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	hdr := map[string][]string{
		"authorization":  {"Bearer abc.def"},
		"cookie":         {"sid=xyz"},
		"x-token":        {"secret-val"},
		"x-app-version":  {"1.2.3"},
		"content-type":   {"application/json"},
		"internal-debug": {"1"},
		"accept":         {"application/json"},
	}
	for _, redact := range []bool{true, false} {
		r := BuildPrompt(
			[]FlowInput{{FlowID: "f1", StartedAt: base, Method: "GET", URL: "http://a/", ReqHeaders: hdr}},
			BuildOptions{Mode: ModeExplain, Redact: redact})
		u := r.User
		for _, want := range []string{
			"authorization: Bearer [REDACTED]",
			"cookie: [REDACTED]",
			"x-token: [REDACTED]",
			"x-app-version: 1.2.3",
			"content-type: application/json",
			"accept: application/json",
		} {
			if !strings.Contains(u, want) {
				t.Errorf("Redact=%v 缺 %q", redact, want)
			}
		}
		for _, banned := range []string{"abc.def", "sid=xyz", "secret-val", "internal-debug", "1.2.3\r"} {
			if strings.Contains(u, banned) {
				t.Errorf("Redact=%v 不应出现 %q", redact, banned)
			}
		}
	}
}

// TestBodyRedact AC10 锚点：固定报文体脱敏断言（JWT/凭据键/手机号/长数字防误伤）。
func TestBodyRedact(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	raw := `{"access_token":"abc123","token":"tk-9","jwt":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.s1gn4tur3",` +
		`"phone":"13812345678","order_no":"139000012345678901234","amount":1}`
	mk := func() FlowInput {
		return FlowInput{FlowID: "f1", StartedAt: base, Method: "POST", URL: "http://a/",
			ReqCT: "application/json", ReqBody: []byte(raw)}
	}

	r := BuildPrompt([]FlowInput{mk()}, BuildOptions{Mode: ModeExplain, IncludeReqBody: true, Redact: true})
	u := r.User
	for _, want := range []string{
		`"access_token":"***"`,
		`"token":"***"`,
		`"jwt":"[REDACTED]"`,
		"138****5678",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("脱敏缺 %q", want)
		}
	}
	for _, banned := range []string{"abc123", "tk-9", "eyJhbGciOiJIUzI1NiJ9", "13812345678"} {
		if strings.Contains(u, banned) {
			t.Errorf("脱敏后不应出现 %q", banned)
		}
	}
	// 长数字串（订单号）不误伤：11 位窗口两侧必有数字
	if !strings.Contains(u, `"order_no":"139000012345678901234"`) {
		t.Error("长数字串不应被手机号规则误伤")
	}

	r2 := BuildPrompt([]FlowInput{mk()}, BuildOptions{Mode: ModeExplain, IncludeReqBody: true, Redact: false})
	if !strings.Contains(r2.User, `"access_token":"abc123"`) || !strings.Contains(r2.User, "13812345678") {
		t.Error("Redact=false 原文应保留")
	}
}

// TestBinaryAndConnect 二进制标注（CT 非白名单 / NUL 兜底）与 CONNECT 隧道渲染。
func TestBinaryAndConnect(t *testing.T) {
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local)
	flows := []FlowInput{
		{
			FlowID: "f1", StartedAt: base, Method: "POST", URL: "http://a/upload", StatusCode: 200,
			DurationMS: 12, ReqCT: "application/json", RespCT: "application/octet-stream",
			ReqBody:  []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05}, // PK 头 + NUL
			RespBody: []byte("plain-text-payload"),
		},
		mkFlow("f2", base.Add(-time.Minute), "CONNECT", "api.example.com:443"), // 时间最早 → seq 排最后
	}
	r := BuildPrompt(flows, BuildOptions{Mode: ModeExplain, IncludeReqBody: true, IncludeRespBody: true})
	u := r.User
	if !strings.Contains(u, "[binary, 10 bytes]") {
		t.Error("含 NUL 的正文应标注二进制")
	}
	if !strings.Contains(u, "[binary, 18 bytes]") {
		t.Error("非白名单 CT 的正文应标注二进制")
	}
	// 二进制计 0 字节：SentKB 只来自 0 字节 → 0
	if r.SentKB != 0 {
		t.Errorf("SentKB=%d want 0（二进制不计预算）", r.SentKB)
	}
	i := strings.Index(u, "[#2] flowId=f2")
	if i < 0 {
		t.Fatal("CONNECT 流缺失")
	}
	seg := u[i:]
	if !strings.Contains(seg, "（TLS 隧道，无解密内容）") {
		t.Error("CONNECT 流应渲染隧道说明")
	}
	if strings.Contains(seg, "请求头") || strings.Contains(seg, "请求正文") {
		t.Error("CONNECT 流不应有头/正文段")
	}
}

// TestExtractIntents JSON 块全语义：缺 seq 补数组序、重复 flowId 后覆盖前、未知/空 flowId 跳过、
// 剥离围栏、大小写容错、失败降级 ok=false。
func TestExtractIntents(t *testing.T) {
	js := `{"intents":[` +
		`{"seq":2,"flowId":"f2","intent":"旧查询"},` +
		`{"flowId":"f1","intent":"提交订单"},` +
		`{"seq":3,"flowId":"f3","intent":"上报埋点","confidence":"low","needsBody":true},` +
		`{"flowId":"fX","intent":"幻觉"},` +
		`{"flowId":"","intent":"空"},` +
		`{"seq":1,"flowId":"f2","intent":"新查询","confidence":"high"}]}`
	md := "逐条意图：\n- [#1] 提交订单\n- [#2] 查询订单\n\n```json\n" + js + "\n```\n以上。"

	items, cleaned, ok := ExtractIntents(md, map[string]bool{"f1": true, "f2": true, "f3": true})
	if !ok {
		t.Fatal("ok=false")
	}
	if len(items) != 3 {
		t.Fatalf("items=%+v", items)
	}
	if items[0].Seq != 1 || items[0].FlowID != "f2" || items[0].Intent != "新查询" || items[0].Confidence != "high" {
		t.Errorf("f2 覆盖错误：%+v", items[0])
	}
	if items[1].Seq != 2 || items[1].FlowID != "f1" || items[1].Intent != "提交订单" {
		t.Errorf("f1 补序错误：%+v", items[1])
	}
	if items[2].Seq != 3 || items[2].NeedsBody != true || items[2].Confidence != "low" {
		t.Errorf("f3 字段错误：%+v", items[2])
	}
	if strings.Contains(cleaned, "```") || strings.Contains(cleaned, "flowId") {
		t.Errorf("围栏未剥净：%q", cleaned)
	}
	if !strings.Contains(cleaned, "以上。") || !strings.Contains(cleaned, "[#2] 查询订单") {
		t.Errorf("cleaned 拼接错误：%q", cleaned)
	}

	// nil validFlowIDs：不过滤，幻觉项保留（缺 seq 按数组序补=4）
	items2, _, ok2 := ExtractIntents(md, nil)
	if !ok2 || len(items2) != 4 {
		t.Fatalf("nil 过滤：%d 项 ok=%v", len(items2), ok2)
	}
	if items2[3].FlowID != "fX" || items2[3].Seq != 4 {
		t.Errorf("fX 补序错误：%+v", items2[3])
	}

	// 失败降级
	if _, _, ok := ExtractIntents("纯 Markdown 无代码块", nil); ok {
		t.Error("无块应 ok=false")
	}
	if _, _, ok := ExtractIntents("```json\n{\"intents\":[]}\n```\n", nil); ok {
		t.Error("空 intents 应 ok=false")
	}
	if _, _, ok := ExtractIntents("```json\n{\"intents\":[{\"flowId\":\"f1\"}]}\n", nil); ok {
		t.Error("未闭合围栏应 ok=false")
	}

	// 大写 ```JSON
	if _, _, ok := ExtractIntents("```JSON\n{\"intents\":[{\"flowId\":\"f1\",\"intent\":\"x\"}]}\n```\n", nil); !ok {
		t.Error("大写 JSON 应识别")
	}
}

// TestExtractMatches 缺 rank 补数组序、未知跳过、重复后覆盖前、排序。
func TestExtractMatches(t *testing.T) {
	js := `{"matches":[` +
		`{"flowId":"fA","reason":"候选一"},` +
		`{"rank":2,"flowId":"fB","reason":"候选二"},` +
		`{"flowId":"fZ","reason":"幻觉"}]}`
	md := "分析如下\n```json\n" + js + "\n```\n"

	items, cleaned, ok := ExtractMatches(md, map[string]bool{"fA": true, "fB": true})
	if !ok || len(items) != 2 {
		t.Fatalf("items=%+v ok=%v", items, ok)
	}
	if items[0].FlowID != "fA" || items[0].Rank != 1 || items[0].Reason != "候选一" {
		t.Errorf("fA 补序错误：%+v", items[0])
	}
	if items[1].FlowID != "fB" || items[1].Rank != 2 {
		t.Errorf("fB 错误：%+v", items[1])
	}
	if strings.Contains(cleaned, "```") {
		t.Errorf("围栏未剥净：%q", cleaned)
	}

	// 重复 flowId：后覆盖前
	js2 := `{"matches":[{"rank":2,"flowId":"fA","reason":"旧"},{"rank":1,"flowId":"fA","reason":"新"}]}`
	items2, _, ok2 := ExtractMatches("```json\n"+js2+"\n```\n", nil)
	if !ok2 || len(items2) != 1 || items2[0].Rank != 1 || items2[0].Reason != "新" {
		t.Fatalf("覆盖错误：%+v ok=%v", items2, ok2)
	}
}

// TestLastJSONBlock 解析器直接覆盖：标准/大写/内联/未闭合/多块取最后。
func TestLastJSONBlock(t *testing.T) {
	c := `{"a":1}`
	if b, _, _, ok := lastJSONBlock("```json\n" + c + "\n```"); !ok || b != c {
		t.Errorf("标准形态：%q ok=%v", b, ok)
	}
	if b, _, _, ok := lastJSONBlock("```JSON\n" + c + "\n```"); !ok || b != c {
		t.Errorf("大写：%q ok=%v", b, ok)
	}
	if b, _, _, ok := lastJSONBlock("```json" + c + "```"); !ok || b != c {
		t.Errorf("内联形态：%q ok=%v", b, ok)
	}
	if _, _, _, ok := lastJSONBlock("```json\n" + c + "\n"); ok {
		t.Error("未闭合应 false")
	}
	if _, _, _, ok := lastJSONBlock("```go\nfmt.Println()\n```"); ok {
		t.Error("非 json 语言应 false")
	}
	// 多块取最后一个
	md := "```json\n{\"a\":1}\n```\n中间\n```json\n{\"a\":2}\n```\n"
	if b, _, _, ok := lastJSONBlock(md); !ok || b != `{"a":2}` {
		t.Errorf("多块取最后：%q ok=%v", b, ok)
	}
}
