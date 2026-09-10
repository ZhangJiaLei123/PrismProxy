// prompt.go：四模式 Prompt 构造 + 双预算裁剪 + 分层脱敏 + 文末 JSON 块解析。
// 纯函数、可单测；FlowInput 为本包自定义输入结构（P2 接线层从 persist 行映射，
// 不 import capture/persist，保持零依赖）。
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ChatMode 四种分析模式（设计稿 §三 M13）。
type ChatMode string

const (
	ModeExplain ChatMode = "explain" // 单流解读
	ModeIntent  ChatMode = "intent"  // 批量意图标注
	ModeLocate  ChatMode = "locate"  // 自然语言定位接口
	ModeFlowmap ChatMode = "flowmap" // 业务链路整理
)

const (
	defaultMaxFlows = 50
	defaultMaxKB    = 64
	perSideMin      = 1024 // 单侧正文下限 1KB（流多时保底可读性）
	perSideMax      = 8192 // 单侧正文上限 8KB（explain 单流即 8KB/侧）
	connectMethod   = "CONNECT"
)

// FlowInput 送审流输入（接线层映射；Header 原样传入，脱敏在渲染时做）。
type FlowInput struct {
	FlowID     string
	StartedAt  time.Time
	Method     string
	URL        string // 完整 URL 含 query；CONNECT 时为 host:port
	StatusCode int
	DurationMS int64
	Process    string
	ReqCT      string
	RespCT     string
	ReqHeaders map[string][]string
	ReqBody    []byte
	RespBody   []byte
}

// BuildOptions Prompt 构造选项。
type BuildOptions struct {
	Mode            ChatMode
	Question        string // locate/flowmap 的用户目标；explain 可空
	IncludeReqBody  bool
	IncludeRespBody bool
	MaxFlows        int // 截前 MaxFlows 条（StartedAt 降序后）；<=0 用默认 50
	MaxKB           int // 正文总预算 KB；<=0 用默认 64
	Redact          bool
	Language        string // 一期仅 zh（模板即中文），字段留给多语言扩展
}

// BuildResult 构造结果（meta 事件数据源：Total/Sent/Truncated/SentKB）。
type BuildResult struct {
	System    string
	User      string
	Total     int  // 候选总数（截断前）
	Sent      int  // 实际发送流数
	Truncated bool // 流数超 MaxFlows 被截
	SentKB    int  // 实际发送正文字节（KB 向上取整）
}

// DefaultBodies 各模式默认是否带正文（§5.3：explain/flowmap 带，intent/locate 仅元数据+关键头）。
func DefaultBodies(m ChatMode) (req, resp bool) {
	switch m {
	case ModeExplain, ModeFlowmap:
		return true, true
	default: // intent / locate
		return false, false
	}
}

// ---------- Prompt 模板 ----------

const systemBase = "你是资深接口逆向/抓包分析助手。输入是 HTTP 抓包记录（可能来自 App/小程序/网页）。要求：\n" +
	"- 只基于提供的证据分析，证据不足处明确说明「证据不足/推测」，不要编造；\n" +
	"- 输出中文 Markdown。"

func systemPrompt(m ChatMode) string {
	switch m {
	case ModeExplain:
		return systemBase + "\n用户会提供一条 HTTP 抓包记录，请解读：业务语义（在做什么）、关键参数含义、鉴权方式、响应结构与错误语义。简明分节。"
	case ModeIntent:
		return systemBase + "\n下面按时间倒序编号给出多条抓包记录（仅元数据与关键头），请为每一条 [#n] 输出一句话业务意图：\n" +
			"- 意图短句 ≤20 字，动宾结构优先（如「查询订单列表/提交下单请求/上报埋点」）；\n" +
			"- 证据不足时给「疑似…」并把 confidence 降为 low/medium；\n" +
			"- 路径完全无语义、无法判断时置 needsBody:true（提示用户开正文重析），不要编造；\n" +
			"- 先逐条输出列表（`- [#n] 意图`），最后输出一个 ```json 代码块：\n" +
			"```json\n{\"intents\":[{\"seq\":1,\"flowId\":\"f_xxx\",\"intent\":\"提交订单\",\"confidence\":\"high\",\"needsBody\":false}]}\n```\n" +
			"- flowId 必须原样使用提供的值；除列表与该 JSON 块外不要输出其他内容。"
	case ModeLocate:
		return systemBase + "\n根据用户描述的目标，从编号的抓包记录中找出最相关的接口：\n" +
			"- 先输出 Markdown 分析（候选比对思路）；\n" +
			"- 最后输出一个 ```json 代码块：\n" +
			"```json\n{\"matches\":[{\"flowId\":\"f_xxx\",\"rank\":1,\"reason\":\"一句话理由\",\"confidence\":\"high\"}]}\n```\n" +
			"- matches 按相关性排序，rank 从 1 开始；只引用提供的 flowId；无匹配时 matches 为空数组。"
	case ModeFlowmap:
		return systemBase + "\n请梳理抓包记录反映的业务链路：\n" +
			"- 用编号步骤串联接口调用顺序（步骤 1/2/3…），每步标注 [#n]、方法 + URL、作用；\n" +
			"- 标注依据（先后顺序/状态码/参数传递，如「步骤 2 的 token 出现在步骤 3 请求头」）；\n" +
			"- 证据不足的环节显式标注「推测」；不要编造不存在的接口。"
	}
	return systemBase
}

// ---------- 构建 ----------

// flowRender 单流渲染中间态（正文已截断/标注/脱敏）。
type flowRender struct {
	f         FlowInput
	seq       int
	reqText   string // ""=不送（未启用/无正文/预算丢弃）
	respText  string
	reqBytes  int // 计入预算的字节数
	respBytes int
}

// BuildPrompt 组装四模式 Prompt：稳定排序（StartedAt 降序，seq 1=最新）→ 截前 MaxFlows →
// 单侧预算 → 逐流渲染（头白名单无条件脱敏；正文按需脱敏/截断/二进制标注）→
// 总超限按流序贪心丢侧（最老流先丢，先丢响应体再丢请求体）。
func BuildPrompt(flows []FlowInput, opt BuildOptions) BuildResult {
	maxFlows := clampInt(opt.MaxFlows, 1, 100, defaultMaxFlows)
	maxKB := clampInt(opt.MaxKB, 8, 256, defaultMaxKB)

	// 1. 稳定排序：StartedAt 降序（seq 1 = 最新，与前端列表默认排序一致）
	sorted := make([]FlowInput, len(flows))
	copy(sorted, flows)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].StartedAt.After(sorted[j].StartedAt)
	})

	total := len(sorted)
	sent := total
	truncated := false
	if sent > maxFlows {
		sent = maxFlows
		truncated = true
	}
	selected := sorted[:sent]

	// 2. 单侧预算 = clamp(总预算/(流数×侧数), 1KB, 8KB)
	sides := 0
	if opt.IncludeReqBody {
		sides++
	}
	if opt.IncludeRespBody {
		sides++
	}
	perSide := 0
	if sides > 0 && sent > 0 {
		perSide = clampInt(maxKB*1024/(sent*sides), perSideMin, perSideMax, perSideMax)
	}

	// 3. 逐流准备正文（截断/二进制标注/脱敏），统计正文字节
	rs := make([]flowRender, sent)
	bodyTotal := 0
	for i := 0; i < sent; i++ {
		r := flowRender{f: selected[i], seq: i + 1}
		if opt.IncludeReqBody {
			r.reqText, r.reqBytes = prepareBody(selected[i].ReqBody, selected[i].ReqCT, perSide, opt.Redact)
		}
		if opt.IncludeRespBody {
			r.respText, r.respBytes = prepareBody(selected[i].RespBody, selected[i].RespCT, perSide, opt.Redact)
		}
		bodyTotal += r.reqBytes + r.respBytes
		rs[i] = r
	}

	// 4. 总超限贪心丢侧：最老流先丢，先丢响应体再丢请求体
	budget := maxKB * 1024
	for i := sent - 1; i >= 0 && bodyTotal > budget; i-- {
		if rs[i].respBytes > 0 {
			bodyTotal -= rs[i].respBytes
			rs[i].respText, rs[i].respBytes = "", 0
		}
		if bodyTotal <= budget {
			break
		}
		if rs[i].reqBytes > 0 {
			bodyTotal -= rs[i].reqBytes
			rs[i].reqText, rs[i].reqBytes = "", 0
		}
	}

	// 5. 渲染 user 消息
	var b strings.Builder
	if opt.Question != "" {
		b.WriteString("用户目标：" + strings.TrimSpace(opt.Question) + "\n\n")
	}
	fmt.Fprintf(&b, "下面是 %d 条抓包记录（按时间倒序编号，[#1] 最新）\n\n", sent)
	for i := range rs {
		renderFlow(&b, &rs[i])
	}

	return BuildResult{
		System:    systemPrompt(opt.Mode),
		User:      b.String(),
		Total:     total,
		Sent:      sent,
		Truncated: truncated,
		SentKB:    (bodyTotal + 1023) / 1024,
	}
}

// ---------- 流渲染 ----------

func renderFlow(b *strings.Builder, r *flowRender) {
	f := r.f
	status := "-"
	if f.StatusCode > 0 {
		status = fmt.Sprintf("%d", f.StatusCode)
	}
	when := "-"
	if !f.StartedAt.IsZero() {
		when = f.StartedAt.Format("2006-01-02 15:04:05.000")
	}
	proc := ""
	if f.Process != "" {
		proc = " | 进程: " + f.Process
	}
	fmt.Fprintf(b, "[#%d] flowId=%s | %s | %s %s | %s | %dms%s\n", r.seq, f.FlowID, when, f.Method, f.URL, status, f.DurationMS, proc)

	if f.Method == connectMethod {
		b.WriteString("（TLS 隧道，无解密内容）\n\n")
		return
	}
	if hs := renderHeaders(f.ReqHeaders); hs != "" {
		b.WriteString("请求头:\n" + hs)
	}
	if r.reqText != "" {
		b.WriteString("请求正文:\n" + r.reqText + "\n")
	}
	if r.respText != "" {
		b.WriteString("响应正文:\n" + r.respText + "\n")
	}
	b.WriteString("\n")
}

// ---------- 头渲染：白名单 + 敏感头无条件脱敏（结构性，不受 Redact 开关影响）----------

var headerWhitelist = map[string]bool{
	"host": true, "content-type": true, "content-length": true, "user-agent": true,
	"referer": true, "origin": true, "accept": true, "x-requested-with": true,
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// headerAllowed 关键头白名单：固定集合 + cookie（送名字、值强制脱敏）+ x-* 前缀 + 名称含 auth/token/key/sign 的业务头。
func headerAllowed(k string) bool {
	lk := strings.ToLower(k)
	if headerWhitelist[lk] || lk == "cookie" {
		return true
	}
	return strings.HasPrefix(lk, "x-") || containsAny(lk, "auth", "token", "key", "sign")
}

// redactHeaderValue 敏感头脱敏（无条件）：Authorization 族保留 scheme，其余（含 Cookie）整值 [REDACTED]。
func redactHeaderValue(k, v string) string {
	lk := strings.ToLower(k)
	if lk == "authorization" || lk == "proxy-authorization" {
		if sp := strings.IndexByte(v, ' '); sp > 0 {
			return v[:sp+1] + "[REDACTED]"
		}
		return "[REDACTED]"
	}
	return "[REDACTED]"
}

// isSensitiveHeader 名称含 auth/token/key/sign 或 Cookie 族——值必须脱敏。
func isSensitiveHeader(k string) bool {
	lk := strings.ToLower(k)
	return containsAny(lk, "auth", "token", "key", "sign") || lk == "cookie" || lk == "set-cookie"
}

// renderHeaders 排序渲染白名单内请求头，敏感头无条件脱敏。
func renderHeaders(h map[string][]string) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		if headerAllowed(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			if isSensitiveHeader(k) {
				v = redactHeaderValue(k, v)
			}
			fmt.Fprintf(&b, "  %s: %s\n", k, v)
		}
	}
	return b.String()
}

// ---------- 正文处理 ----------

// isTextCT 文本 Content-Type 白名单（§5.4：text/json/xml/javascript/html/urlencoded/yaml/csv/graphql）。
func isTextCT(ct string) bool {
	ct = strings.ToLower(ct)
	if ct == "" {
		return true // 无 CT 保守按文本，二进制由 NUL 检测兜底
	}
	return containsAny(ct, "text/", "json", "xml", "javascript", "html",
		"urlencoded", "yaml", "csv", "graphql")
}

// prepareBody 单侧正文处理：空→不送；二进制→标注；否则截断+脱敏，返回渲染文本与计入预算的字节数。
func prepareBody(body []byte, ct string, perSide int, redact bool) (string, int) {
	if len(body) == 0 {
		return "", 0
	}
	if !isTextCT(ct) || bytes.IndexByte(body, 0) >= 0 {
		return fmt.Sprintf("[binary, %d bytes]", len(body)), 0
	}
	out := string(body)
	origKB := (len(body) + 1023) / 1024
	truncated := false
	if perSide > 0 && len(out) > perSide {
		out = out[:perSide]
		truncated = true
	}
	if redact {
		out = redactBody(out)
	}
	if truncated {
		out += fmt.Sprintf("\n…（已截断，原始 %d KB）", origKB)
	}
	return out, len(out)
}

// ---------- 脱敏（正文，受 Redact 开关；头脱敏在 renderHeaders 无条件做）----------

var (
	jwtRe   = regexp.MustCompile(`eyJ[\w-]+\.[\w-]+\.[\w-]+`)
	credRe  = regexp.MustCompile(`(?i)"(access_token|refresh_token|api_key|apikey|password|passwd|pwd|secret|token)"\s*:\s*"[^"]*"`)
	phoneRe = regexp.MustCompile(`1[3-9]\d{9}`)
)

// redactBody 正文脱敏：JWT → [REDACTED]；凭据键值 → "***"；手机号 → 前3+****+后4。
// 尽力而为，不能保证覆盖所有敏感字段（文案已在 UI 告知）。
func redactBody(s string) string {
	s = jwtRe.ReplaceAllString(s, "[REDACTED]")
	s = credRe.ReplaceAllString(s, `"${1}":"***"`)
	return maskPhones(s)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// maskPhones 手机号脱敏：1[3-9]xxxxxxxxx，前后不能是数字（避免误伤长数字串中的 11 位）。
func maskPhones(s string) string {
	idx := phoneRe.FindAllStringIndex(s, -1)
	if len(idx) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, m := range idx {
		if m[0] > 0 && isDigit(s[m[0]-1]) {
			continue
		}
		if m[1] < len(s) && isDigit(s[m[1]]) {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(s[m[0]:m[0]+3] + "****" + s[m[1]-4:m[1]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// ---------- 文末 JSON 块解析（路线 A）----------

// IntentItem intent 模式结构化输出项。
type IntentItem struct {
	Seq        int // 流序号（对应 [#n]）；缺失按数组序补
	FlowID     string
	Intent     string
	Confidence string // high|medium|low
	NeedsBody  bool
}

// MatchItem locate 模式结构化输出项。
type MatchItem struct {
	FlowID     string
	Rank       int // 缺失按数组序补
	Reason     string
	Confidence string
}

// lastJSONBlock 找最后一个 ```json 围栏（大小写不敏感；兼容 ```json{...}``` 内联形态）。
// 返回块内容与围栏整体 span（[spanStart, spanEnd)，含围栏本身，供剥离）；ok=false 表示无完整块。
func lastJSONBlock(md string) (content string, spanStart, spanEnd int, ok bool) {
	for i := strings.LastIndex(md, "```"); i >= 0; i = strings.LastIndex(md[:i], "```") {
		rest := md[i+3:]
		// 语言标记 "json" 须紧贴 ```，其后接空白或 '{'（内联 ```json{...}``` 无换行形态）
		low := strings.ToLower(rest)
		if !strings.HasPrefix(low, "json") {
			continue
		}
		if len(low) > 4 {
			switch low[4] {
			case ' ', '\t', '\n', '\r', '{':
			default:
				continue
			}
		}
		start := i + 3 + 4 // 跳过 ```json
		// 标准形态跳过语言行行尾换行；内联形态 start 直接落在 '{'
		if strings.HasPrefix(md[start:], "\r\n") {
			start += 2
		} else if strings.HasPrefix(md[start:], "\n") {
			start++
		}
		rel := strings.Index(md[start:], "```")
		if rel < 0 {
			return "", 0, 0, false // 围栏未闭合：不剥离
		}
		return strings.TrimSpace(md[start : start+rel]), i, start + rel + 3, true
	}
	return "", 0, 0, false
}

// trimJoin 剥离围栏后拼接左右文本：左去尾空白、右去首尾空白，两者都有时间插空行。
func trimJoin(left, right string) string {
	l := strings.TrimRight(left, " \t\n\r")
	r := strings.TrimSpace(right)
	switch {
	case l == "":
		return r
	case r == "":
		return l
	default:
		return l + "\n\n" + r
	}
}

// ExtractIntents 解析 intent 模式文末 JSON 块。
// validFlowIDs 非空时跳过未知 flowId（幻觉防护）；缺 seq 按数组序补；同 flowId 重复后覆盖前；按 seq 升序。
// ok=false 表示无有效块（调用方仅展示 Markdown，不报错）。
func ExtractIntents(md string, validFlowIDs map[string]bool) (items []IntentItem, cleaned string, ok bool) {
	cleaned = strings.TrimSpace(md)
	block, s, e, has := lastJSONBlock(md)
	if !has {
		return nil, cleaned, false
	}
	var parsed struct {
		Intents []IntentItem `json:"intents"`
	}
	if err := json.Unmarshal([]byte(block), &parsed); err != nil || len(parsed.Intents) == 0 {
		return nil, cleaned, false
	}
	out := make([]IntentItem, 0, len(parsed.Intents))
	seen := make(map[string]bool, len(parsed.Intents))
	for i, it := range parsed.Intents {
		if it.FlowID == "" {
			continue
		}
		if validFlowIDs != nil && !validFlowIDs[it.FlowID] {
			continue
		}
		if it.Seq == 0 {
			it.Seq = i + 1
		}
		if seen[it.FlowID] {
			for j := range out { // 重复 flowId：后覆盖前
				if out[j].FlowID == it.FlowID {
					out[j] = it
					break
				}
			}
			continue
		}
		seen[it.FlowID] = true
		out = append(out, it)
	}
	if len(out) == 0 {
		return nil, cleaned, false
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, trimJoin(md[:s], md[e:]), true
}

// ExtractMatches 解析 locate 模式文末 JSON 块，规则同 ExtractIntents（缺 rank 按数组序补，按 rank 升序）。
func ExtractMatches(md string, validFlowIDs map[string]bool) (items []MatchItem, cleaned string, ok bool) {
	cleaned = strings.TrimSpace(md)
	block, s, e, has := lastJSONBlock(md)
	if !has {
		return nil, cleaned, false
	}
	var parsed struct {
		Matches []MatchItem `json:"matches"`
	}
	if err := json.Unmarshal([]byte(block), &parsed); err != nil || len(parsed.Matches) == 0 {
		return nil, cleaned, false
	}
	out := make([]MatchItem, 0, len(parsed.Matches))
	seen := make(map[string]bool, len(parsed.Matches))
	for i, it := range parsed.Matches {
		if it.FlowID == "" {
			continue
		}
		if validFlowIDs != nil && !validFlowIDs[it.FlowID] {
			continue
		}
		if it.Rank == 0 {
			it.Rank = i + 1
		}
		if seen[it.FlowID] {
			for j := range out {
				if out[j].FlowID == it.FlowID {
					out[j] = it
					break
				}
			}
			continue
		}
		seen[it.FlowID] = true
		out = append(out, it)
	}
	if len(out) == 0 {
		return nil, cleaned, false
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out, trimJoin(md[:s], md[e:]), true
}

// ---------- 小工具 ----------

// clampInt v<=0 用默认；否则夹到 [min,max]。
func clampInt(v, min, max, def int) int {
	if v <= 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
