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
	if len(args) == 0 {
		printCLIUsage(os.Stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]

	// 全局 flag：--pretty 人类可读；--addr/--token 覆盖自动发现
	fs := flag.NewFlagSet("cli "+cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pretty := fs.Bool("pretty", false, "人类可读输出（默认 JSON）")
	addrFlag := fs.String("addr", "", "控制 API 地址（默认自动发现）")
	tokenFlag := fs.String("token", "", "控制 API token（默认自动发现）")
	fs.Usage = func() { printCLIUsage(os.Stderr) }
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	pos := fs.Args()

	c, err := newClient(configDir, *addrFlag, *tokenFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}

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
	case "help", "-h", "--help":
		printCLIUsage(os.Stdout)
		return 0
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
	sub := ""
	if len(pos) > 0 {
		sub = pos[1] // pos[0]="flows"
	}
	fs := flag.NewFlagSet("flows", flag.ContinueOnError)
	filter := fs.String("filter", "", "按 host/URL 子串过滤")
	limit := fs.Int("limit", 0, "只返回最近 N 条")
	body := fs.String("body", "", "消息体：req|resp（配合 get）")
	if err := fs.Parse(pos[1:]); err != nil {
		return nil, err
	}
	args := fs.Args()
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
		if len(args) == 0 {
			return nil, fmt.Errorf("flows get 需要 <id>")
		}
		if *body != "" {
			if *body != "req" && *body != "resp" {
				return nil, fmt.Errorf("--body 须为 req|resp")
			}
			return c.get(fmt.Sprintf("/flows/%s/body?which=%s", urlEncode(args[0]), *body))
		}
		return c.get("/flows/" + urlEncode(args[0]))
	case "clear":
		return c.post("/flows/clear", nil)
	default:
		return nil, fmt.Errorf("未知 flows 子命令: %s（list|get|clear）", sub)
	}
}

func cliRules(c *client, pos []string) (any, error) {
	if len(pos) == 1 || pos[1] == "list" {
		return c.get("/rules")
	}
	sub := pos[1]
	args := pos[2:]
	switch sub {
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
	action := "status"
	if len(pos) > 1 {
		action = pos[1]
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
	if len(pos) == 1 || pos[1] == "get" {
		return c.get("/settings")
	}
	if pos[1] != "set" || len(pos) < 4 {
		return nil, fmt.Errorf("用法: settings get | settings set <key> <value>")
	}
	key, val := pos[2], strings.Join(pos[3:], " ")
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
	// pos 已去掉 "ui"
	if len(pos) == 0 {
		return nil, fmt.Errorf("用法: ui clear | ui settings [tab]")
	}
	switch pos[0] {
	case "clear":
		return c.post("/ui/clear", nil)
	case "settings":
		tab := ""
		if len(pos) > 1 {
			tab = pos[1]
		}
		return c.post("/ui/settings", map[string]any{"tab": tab})
	default:
		return nil, fmt.Errorf("未知 ui 子命令: %s（clear|settings）", pos[0])
	}
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
	base   string
	token  string
	http   *http.Client
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
