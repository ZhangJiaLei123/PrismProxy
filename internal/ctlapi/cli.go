package ctlapi

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// RunCLI 执行 `cli` 子命令（args 为 "cli" 之后的参数）。
// 成功时 JSON 结果打印到 stdout；错误信息打印到 stderr 并以非零码退出。
// configDir 用于发现 endpoint 文件（exe 同级 config）。
func RunCLI(configDir string, args []string) int {
	// 全局 flag：--pretty 人类可读；--addr/--token 覆盖自动发现
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pretty := fs.Bool("pretty", false, "人类可读输出（默认 JSON）")
	addrFlag := fs.String("addr", "", "控制 API 地址（默认自动发现）")
	tokenFlag := fs.String("token", "", "控制 API token（默认自动发现）")
	fs.Usage = func() { printCLIUsage(os.Stderr) }
	// 全局 flag（--pretty/--addr/--token）可出现在子命令前或后：前置后解析；
	// 未定义的子命令 flag（--filter 等）不在此 fs，遇首个位置参数即停止解析。
	fs.Parse(reorderFlagsGlobal(args))
	// pos[0]=子系统名（status/flows/rules/...），各处理器内部 pos[1:] 自行剥离
	pos := fs.Args()
	if len(pos) == 0 {
		printCLIUsage(os.Stderr)
		return 2
	}
	cmd := pos[0]
	if cmd == "help" {
		printCLIUsage(os.Stdout)
		return 0
	}

	// flows watch 是长运行流式命令（SSE），自管连接/重连与退出码，不走 newClient 主路径
	if cmd == "flows" && subCmd(pos[1:]) == "watch" {
		return runWatch(configDir, *addrFlag, *tokenFlag, pos)
	}

	c, err := newClient(configDir, *addrFlag, *tokenFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}

	// pos[0] 是子命令名（flows/rules/...）；各处理器内部自行剥离
	var out any
	switch cmd {
	case "status":
		out, err = c.get("/status")
	case "flows":
		out, err = cliFlows(c, pos)
	case "rules":
		out, err = cliRules(c, pos)
	case "sysproxy":
		out, err = cliSysProxy(c, pos)
	case "settings":
		out, err = cliSettings(c, pos)
	case "ui":
		out, err = cliUI(c, pos)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", cmd)
		printCLIUsage(os.Stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	printResult(out, *pretty)
	return 0
}

// printCLIUsage 输出命令帮助
func printCLIUsage(w io.Writer) {
	fmt.Fprint(w, `PrismProxy cli — 控制运行中的 PrismProxy 实例（M8，方案 §4.12）

用法: PrismProxy.exe cli <命令> [参数] [--pretty] [--addr 地址] [--token token]

命令:
  status                                 代理状态 / 流计数 / 系统代理状态
  flows list [--filter 子串] [--limit N] 流摘要列表（不含 body）
  flows get <id>                         单流详情（Headers 等）
  flows get <id> --body req|resp         单流消息体（JSON：raw/body/base64）
  flows clear                            清空记录列表（保留置顶）
  flows watch [--filter 子串] [--status] [--format ndjson|sse]
                                         实时监控：先输出全量 snapshot，再持续输出增量
                                         （upsert/evict/status/reset），断线自动重连
  rules list                             过滤规则组 + 解密规则
  rules ignore host <域名>               快捷忽略域名（自身+全部子域）
  rules ignore process <进程名>          快捷忽略进程
  rules group <id> enable|disable        启用/停用过滤规则组
  rules decrypt mitm|bypass <域名>       添加解密规则（mitm 解密 / bypass 透传）
  sysproxy status                        系统代理状态（off|on|occupied）
  sysproxy on                            接管系统代理
  sysproxy off                           恢复系统代理
  settings get                           读取全部配置（JSON）
  settings set <key> <value>             改单项配置（maxFlows/listenAddr/upstreamMode 等）
  ui clear                               清除 GUI 记录列表（headless 下仅清数据）
  ui settings [tab]                      打开 GUI 设置面板（tab: general|network|decrypt|capture|domains）

全局参数:
  --pretty   人类可读缩进输出（默认单行 JSON，便于 AI/脚本解析）
  --addr     控制 API 地址（默认从 config/ctl-endpoint.json 自动发现）
  --token    控制 API token（默认自动发现）

示例:
  PrismProxy.exe cli status --pretty
  PrismProxy.exe cli flows list --filter baidu --limit 20
  PrismProxy.exe cli rules ignore host api.example.com
  PrismProxy.exe cli sysproxy on
`)
}

// ---------- 子命令实现 ----------

func cliFlows(c *client, pos []string) (any, error) {
	// pos[0]="flows"；第一个非 flag 位置参数是动作（list/get/clear，默认 list）
	args := pos[1:]
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("flows", flag.ContinueOnError)
	filter := fs.String("filter", "", "按 host/URL 子串过滤")
	limit := fs.Int("limit", 0, "只返回最近 N 条")
	body := fs.String("body", "", "消息体：req|resp（配合 get）")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return nil, err
	}
	rem := fs.Args() // 动作之后的位置参数（如 get <id>）
	switch sub {
	case "list", "":
		q := ""
		if *filter != "" {
			q += "&filter=" + urlEncode(*filter)
		}
		if *limit > 0 {
			q += "&limit=" + fmt.Sprint(*limit)
		}
		return c.get("/flows?" + strings.TrimPrefix(q, "&"))
	case "get":
		if len(rem) == 0 {
			return nil, fmt.Errorf("flows get 需要 <id>")
		}
		if *body != "" {
			if *body != "req" && *body != "resp" {
				return nil, fmt.Errorf("--body 须为 req|resp")
			}
			return c.get(fmt.Sprintf("/flows/%s/body?which=%s", urlEncode(rem[0]), *body))
		}
		return c.get("/flows/" + urlEncode(rem[0]))
	case "clear":
		return c.post("/flows/clear", nil)
	default:
		return nil, fmt.Errorf("未知 flows 子命令: %s（list|get|clear）", sub)
	}
}

func cliRules(c *client, pos []string) (any, error) {
	// pos[0]="rules"；第一个非 flag 位置参数是动作（list/ignore/group/decrypt，默认 list）
	args := pos[1:]
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "":
		return c.get("/rules")
	case "ignore":
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: rules ignore host|process <值>")
		}
		target, value := args[0], strings.Join(args[1:], " ")
		if target != "host" && target != "process" {
			return nil, fmt.Errorf("ignore 目标须为 host|process")
		}
		return c.post("/rules", map[string]any{"action": "ignore", "target": target, "value": value})
	case "group":
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: rules group <id> enable|disable")
		}
		enabled := args[1] == "enable"
		if args[1] != "enable" && args[1] != "disable" {
			return nil, fmt.Errorf("须为 enable|disable")
		}
		return c.post(fmt.Sprintf("/rules/groups/%s/enabled", urlEncode(args[0])),
			map[string]any{"enabled": enabled})
	case "decrypt":
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: rules decrypt mitm|bypass <域名>")
		}
		kind := args[0]
		if kind != "mitm" && kind != "bypass" {
			return nil, fmt.Errorf("解密动作须为 mitm|bypass")
		}
		return c.post("/rules", map[string]any{"action": "decrypt", "kind": kind, "host": args[1]})
	default:
		return nil, fmt.Errorf("未知 rules 子命令: %s（list|ignore|group|decrypt）", sub)
	}
}

func cliSysProxy(c *client, pos []string) (any, error) {
	// pos[0]="sysproxy"；pos[1] 是动作（status/on/off，默认 status）
	args := pos[1:]
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "status":
		return c.get("/sysproxy")
	case "on":
		return c.post("/sysproxy", map[string]any{"action": "on"})
	case "off":
		return c.post("/sysproxy", map[string]any{"action": "off"})
	default:
		return nil, fmt.Errorf("未知 sysproxy 子命令: %s（status|on|off）", action)
	}
}

func cliSettings(c *client, pos []string) (any, error) {
	// pos[0]="settings"；pos[1] 是 get（默认）| set <key> <value...>
	args := pos[1:]
	sub := "get"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	if sub == "get" {
		return c.get("/settings")
	}
	if sub != "set" || len(args) < 2 {
		return nil, fmt.Errorf("用法: settings get | settings set <key> <value>")
	}
	key, val := args[0], strings.Join(args[1:], " ")
	// 读-改-写：取当前配置 → 改单项 → 整体提交（后端校验 + 热应用）
	cur, err := c.get("/settings")
	if err != nil {
		return nil, err
	}
	doc, err := json.Marshal(cur)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return nil, err
	}
	m[key] = parseScalar(val)
	body, _ := json.Marshal(m)
	var resp struct {
		OK       bool     `json:"ok"`
		Warnings []string `json:"warnings"`
	}
	raw, err := c.put("/settings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &resp)
	return map[string]any{"ok": true, "key": key, "value": m[key], "warnings": resp.Warnings}, nil
}

func cliUI(c *client, pos []string) (any, error) {
	// pos[0]="ui"；args[0] 是动作 clear|settings（缺省=settings 打开面板）
	args := pos[1:]
	sub := "settings"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "clear":
		return c.post("/ui/clear", nil)
	case "settings":
		tab := ""
		if len(args) > 0 {
			tab = args[0]
		}
		return c.post("/ui/settings", map[string]any{"tab": tab})
	default:
		return nil, fmt.Errorf("未知 ui 子命令: %q（clear|settings）", sub)
	}
}

// reorderFlagsGlobal 只把已知全局 flag（--pretty/--addr/--token）提到最前，
// 其余参数（含子命令自有 flag 如 --filter）保持原位，交由子命令 flagSet 解析。
func reorderFlagsGlobal(args []string) []string {
	known := map[string]bool{"-pretty": true, "--pretty": true,
		"-addr": true, "--addr": true, "-token": true, "--token": true}
	// 取值型 flag（bool 的 --pretty 不吞下一 token）
	valueFlags := map[string]bool{"-addr": true, "--addr": true, "-token": true, "--token": true}
	var globals, others []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		key := a
		if strings.Contains(a, "=") {
			key = strings.SplitN(a, "=", 2)[0]
		}
		if known[key] {
			globals = append(globals, a)
			// 仅取值型 flag 在此吞下一 token 作为值；bool 的 --pretty 不吞
			if valueFlags[key] && !strings.Contains(a, "=") && i+1 < len(args) {
				globals = append(globals, args[i+1])
				i++
			}
			continue
		}
		others = append(others, a)
	}
	return append(globals, others...)
}

// reorderFlags 把以 '-' 开头的 flag（连同其值）移到位置参数前，
// 兼容 `flows list --filter baidu --limit 10` 这类「子命令在 flag 前」的自然语序
// （Go flag 包默认在首个位置参数处停止解析）。
func reorderFlags(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// 形如 --key=value 的不取下一个参数；否则下一个参数是值（若不以 '-' 开头）
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// parseScalar 把命令行字符串值解析为 JSON 标量（true/false/数字/字符串）
func parseScalar(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	var n json.Number
	if err := json.Unmarshal([]byte(v), &n); err == nil {
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return v
}

// printResult 输出结果：pretty 缩进或单行 JSON
func printResult(v any, pretty bool) {
	enc := json.NewEncoder(os.Stdout)
	if pretty {
		enc.SetIndent("", "  ")
	}
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "输出编码失败:", err)
	}
}

// ---------- HTTP client ----------

type client struct {
	base       string
	token      string
	http       *http.Client // 普通请求-响应（含快照拉取）
	streamHTTP *http.Client // SSE 事件流长连接（watch 专用：无总超时、独立连接不复用）
}

func newClient(configDir, addr, token string) (*client, error) {
	if addr == "" || token == "" {
		ep, err := ReadEndpoint(EndpointFile(configDir))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("未发现运行中的 PrismProxy 实例（%s 不存在）——请先启动程序，或用 --addr/--token 指定", endpointFileName)
			}
			return nil, fmt.Errorf("读取 endpoint 文件失败: %w", err)
		}
		if addr == "" {
			addr = ep.Addr
		}
		if token == "" {
			token = ep.Token
		}
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return &client{
		base:   strings.TrimRight(addr, "/") + "/api/v1",
		token:  token,
		http:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *client) do(method, path string, body io.Reader) (json.RawMessage, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接控制 API 失败（实例未运行？）: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		var e struct{ Error string `json:"error"` }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.RawMessage(data), nil
}

func (c *client) get(path string) (any, error) {
	raw, err := c.do(http.MethodGet, path, nil)
	return rawMessage(raw), err
}

func (c *client) post(path string, v any) (any, error) {
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	raw, err := c.do(http.MethodPost, path, body)
	return rawMessage(raw), err
}

func (c *client) put(path string, body io.Reader) (json.RawMessage, error) {
	return c.do(http.MethodPut, path, body)
}

// rawMessage 把响应字节透传为可被 Encoder 原样输出的 JSON（避免 map 化丢字段顺序无关紧要）
func rawMessage(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{"ok": true}
	}
	return raw
}

func urlEncode(s string) string {
	// Flow ID 仅含数字与 '-'，但域名/进程名可能有特殊字符，统一路径转义
	return url.PathEscape(s)
}
