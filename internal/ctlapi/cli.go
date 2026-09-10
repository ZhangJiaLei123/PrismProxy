package ctlapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// errOutputHandled 哨兵：处理器已自行向 stdout 输出原文（如 rules export），
// RunCLI 不再做 JSON 结果封装打印（否则尾随 null 破坏重定向文件）。
var errOutputHandled = errors.New("output already handled")

// RunCLI 执行 `cli` 子命令（args 为 "cli" 之后的参数）。
// 成功时 JSON 结果打印到 stdout；错误信息打印到 stderr 并以非零码退出。
// configDir 用于发现 endpoint 文件（exe 同级 config）。
func RunCLI(configDir string, args []string) int {
	// 全局 flag：--pretty 人类可读；--addr/--token 覆盖自动发现；--project/-P 目标项目（M9）
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pretty := fs.Bool("pretty", false, "人类可读输出（默认 JSON）")
	addrFlag := fs.String("addr", "", "控制 API 地址（默认自动发现）")
	tokenFlag := fs.String("token", "", "控制 API token（默认自动发现）")
	projectFlag := fs.String("project", "", "目标项目 id|名称（规则/设置类命令；默认当前项目）")
	projectShort := fs.String("P", "", "目标项目 id|名称（--project 简写）")
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
	// 目标项目（M9，设计 §6.3）：--project 优先于 -P；仅规则/设置类命令生效
	c.project = *projectFlag
	if c.project == "" {
		c.project = *projectShort
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
	case "project":
		out, err = cliProject(c, pos)
	case "ui":
		out, err = cliUI(c, pos)
	case "proxy":
		out, err = cliProxy(c, pos)
	case "adb":
		out, err = cliADB(c, pos)
	case "domains":
		out, err = cliDomains(c, pos)
	case "compose":
		out, err = cliCompose(c, pos)
	case "processes":
		out, err = c.get("/processes")
	case "ca":
		out, err = cliCA(c, pos)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", cmd)
		printCLIUsage(os.Stderr)
		return 2
	}
	if err != nil {
		if errors.Is(err, errOutputHandled) {
			return 0 // 处理器已自行输出原文（rules export），无需封装打印
		}
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	printResult(out, *pretty)
	return 0
}

// printCLIUsage 输出命令帮助
func printCLIUsage(w io.Writer) {
	fmt.Fprint(w, `PrismProxy cli — 控制运行中的 PrismProxy 实例（M8，方案 §4.12）

用法: PrismProxy.exe cli <命令> [参数] [--pretty] [--addr 地址] [--token token] [--project 项目]

命令:
  status                                 代理状态 / 流计数 / 系统代理状态 / 当前项目
  flows list [--filter 子串] [--limit N] 流摘要列表（不含 body）
  flows get <id>                         单流详情（Headers 等）
  flows get <id> --body req|resp         单流消息体（JSON：raw/body/base64）
  flows clear                            清空记录列表（保留置顶）
  flows pin <id> [--pin false]           置顶/取消置顶流（置顶流不淘汰、清空保留）
  flows curl <id> [--shell cmd|bash]
                                         生成该流的可执行 cURL 命令
  flows watch [--filter 子串] [--status] [--format ndjson|sse]
                                         实时监控：先输出全量 snapshot，再持续输出增量
                                         （upsert/evict/status/reset），断线自动重连
  proxy start|stop                       启动/停止代理监听（sysproxy on 会隐式启动）
  ca install                             安装根证书到当前用户受信根存储（免管理员）
  adb devices                            已配置的 ADB 设备清单（名称/path/serial/autoSet）
  adb test [--adb 路径]                   测试 adb 连通性并列出已连接设备
  adb set [--adb 路径] [--serial 序列号]  给设备写全局 http_proxy（指向本机代理）
  adb clear [--adb 路径] [--serial ...]   清除设备全局 http_proxy
  domains list                           域名组清单（--project 可指定目标项目）
  domains get <id>                       查看域名组原始文本
  domains save <id> <域名...>            新建/覆盖域名组（空格分隔，每行一个域名）
  domains import <文件路径|URL> [--id]   从本地 txt 或 http(s) URL 导入域名组
  domains delete <id>                    删除自定义域名组
  rules export [--embed]                 导出规则 JSON（原文直出 stdout，可 > 文件）
  rules import <文件路径|URL>            导入规则（整体替换，内嵌域名组自动补建）
  compose <URL> [--method M] [--header 'K: V']... [--body 文本] [--insecure]
                                         调试重发：独立直连目标，结果作为新流入列表
                                         （--header 可重复；值可含分号如 Cookie，每个 --header 一个头）
  processes                              枚举系统运行中进程名（ignore process 候选）
  project list                           项目列表 + 当前项目
  project switch <id|名称>               切换当前项目（运行中热切换，规则与流量历史随之切换）
  project create <名称> [--from id|名称] 新建项目并切换；--from 从指定项目复制规则+域名组
  project rename <id|名称> <新名称>      重命名项目（名称含空格时用 --name 指定新名）
  project delete <id|名称>               删除项目（删当前项目后自动打开剩余首个；无剩余则进欢迎页）
  project close                         关闭当前项目（不删除），进入欢迎页态
  rules list                             过滤规则组 + 解密规则
  rules ignore host <域名>               快捷忽略域名（自身+全部子域）
  rules ignore path <路径>               快捷忽略路径（精确+下级路径，支持 *? 通配，自动去 query）
  rules ignore process <进程名>          快捷忽略进程
  rules group <id> enable|disable        启用/停用过滤规则组
  rules decrypt mitm|bypass <域名>       添加解密规则（mitm 解密 / bypass 透传）
  sysproxy status                        系统代理状态（off|on|occupied）
  sysproxy on                            接管系统代理
  sysproxy off                           恢复系统代理
  settings get                           读取全部配置（JSON）
  settings set <key> <value>             改单项配置（maxFlows/listenAddr/upstreamMode 等）
  ui clear                               清除 GUI 记录列表（headless 下仅清数据）
  ui settings [tab]                      打开 GUI 设置面板（tab: general|network|adb|decrypt|capture|domains）

全局参数:
  --pretty      人类可读缩进输出（默认单行 JSON，便于 AI/脚本解析）
  --addr        控制 API 地址（默认从 config/ctl-endpoint.json 自动发现）
  --token       控制 API token（默认自动发现）
  --project, -P 目标项目 id|名称（仅规则/设置类命令生效；默认当前项目。
                指定非当前项目时只改其配置文件，不切换当前项目、不影响运行中的代理；
                flows/sysproxy/status/ui 命令不受此参数影响）

示例:
  PrismProxy.exe cli status --pretty
  PrismProxy.exe cli flows list --filter baidu --limit 20
  PrismProxy.exe cli rules ignore host api.example.com
  PrismProxy.exe cli rules list --project 商城联调
  PrismProxy.exe cli project switch 商城联调
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
	shell := fs.String("shell", "cmd", "cURL 目标 shell：cmd|bash（配合 curl）")
	pinned := fs.String("pin", "", "置顶/取消置顶：true|false（配合 <id>）")
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
	case "pin":
		// flows pin <id> [--pin false]（默认置顶；--pin false 取消）
		if len(rem) == 0 {
			return nil, fmt.Errorf("flows pin 需要 <id>")
		}
		on := true
		if *pinned == "false" || *pinned == "0" {
			on = false
		} else if *pinned != "" && *pinned != "true" && *pinned != "1" {
			return nil, fmt.Errorf("--pin 须为 true|false")
		}
		return c.post(fmt.Sprintf("/flows/%s/pin", urlEncode(rem[0])), map[string]any{"pinned": on})
	case "curl":
		if len(rem) == 0 {
			return nil, fmt.Errorf("flows curl 需要 <id>")
		}
		if *shell != "cmd" && *shell != "bash" {
			return nil, fmt.Errorf("--shell 须为 cmd|bash")
		}
		return c.get(fmt.Sprintf("/flows/%s/curl?shell=%s", urlEncode(rem[0]), *shell))
	default:
		return nil, fmt.Errorf("未知 flows 子命令: %s（list|get|clear|pin|curl）", sub)
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
	// 导入导出为独立动作（不与 list 共用 flagSet）
	if sub == "export" {
		fs := flag.NewFlagSet("rules-export", flag.ContinueOnError)
		embed := fs.Bool("embed", false, "导出时内嵌规则引用到的域名组全文")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		if rem := fs.Args(); len(rem) > 0 {
			return nil, fmt.Errorf("export 不接受位置参数: %s", strings.Join(rem, " "))
		}
		path := "/rules/export"
		q := ""
		if *embed {
			q = "embed=1"
		}
		if c.project != "" {
			if q != "" {
				q += "&"
			}
			q += "project=" + url.QueryEscape(c.project)
		}
		if q != "" {
			path += "?" + q
		}
		raw, err := c.do(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		// 规则文件原文直出 stdout（供 > file.json 重定向保存），不走结果封装
		if _, err := os.Stdout.Write(append(raw, '\n')); err != nil {
			return nil, fmt.Errorf("写出失败: %w", err)
		}
		return nil, errOutputHandled
	}
	if sub == "import" {
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: rules import <本地文件路径|http(s) URL>")
		}
		return c.post(c.withProject("/rules/import"), map[string]any{"src": args[0]})
	}

	switch sub {
	case "list", "":
		return c.get(c.withProject("/rules"))
	case "ignore":
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: rules ignore host|path|process <值>")
		}
		target, value := args[0], strings.Join(args[1:], " ")
		if target != "host" && target != "path" && target != "process" {
			return nil, fmt.Errorf("ignore 目标须为 host|path|process")
		}
		return c.post(c.withProject("/rules"), map[string]any{"action": "ignore", "target": target, "value": value})
	case "group":
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: rules group <id> enable|disable")
		}
		enabled := args[1] == "enable"
		if args[1] != "enable" && args[1] != "disable" {
			return nil, fmt.Errorf("须为 enable|disable")
		}
		return c.post(c.withProject(fmt.Sprintf("/rules/groups/%s/enabled", urlEncode(args[0]))),
			map[string]any{"enabled": enabled})
	case "decrypt":
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: rules decrypt mitm|bypass <域名>")
		}
		kind := args[0]
		if kind != "mitm" && kind != "bypass" {
			return nil, fmt.Errorf("解密动作须为 mitm|bypass")
		}
		return c.post(c.withProject("/rules"), map[string]any{"action": "decrypt", "kind": kind, "host": args[1]})
	default:
		return nil, fmt.Errorf("未知 rules 子命令: %s（list|ignore|group|decrypt|import|export）", sub)
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
		return c.get(c.withProject("/settings"))
	}
	if sub != "set" || len(args) < 2 {
		return nil, fmt.Errorf("用法: settings get | settings set <key> <value>")
	}
	key, val := args[0], strings.Join(args[1:], " ")
	// 读-改-写：取目标项目配置 → 改单项 → 整体提交（后端校验 + 热应用）
	cur, err := c.get(c.withProject("/settings"))
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
	raw, err := c.put(c.withProject("/settings"), bytes.NewReader(body))
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

// cliProject 项目管理（M9，设计 §6.3）：list/switch/create [--from]/rename/delete
func cliProject(c *client, pos []string) (any, error) {
	// pos[0]="project"；第一个非 flag 位置参数是动作（list/switch/create/rename/delete，默认 list）
	args := pos[1:]
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "":
		return c.get("/projects")
	case "switch":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: project switch <id|名称>")
		}
		return c.post("/projects", map[string]any{"action": "switch", "id": strings.Join(args, " ")})
	case "create":
		fs := flag.NewFlagSet("project-create", flag.ContinueOnError)
		from := fs.String("from", "", "从指定项目复制规则+域名组（id|名称）")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		rem := fs.Args()
		if len(rem) < 1 {
			return nil, fmt.Errorf("用法: project create <名称> [--from <id|名称>]")
		}
		return c.post("/projects", map[string]any{"action": "create", "name": strings.Join(rem, " "), "from": *from})
	case "rename":
		// 目标（id|名称）与新名都可能含空格，位置参数形态无法区分：优先 --name 指定新名，
		// 目标取全部位置参数 Join；无 --name 时保持旧语义（args[0]=目标，其余=新名）
		fs := flag.NewFlagSet("project-rename", flag.ContinueOnError)
		nameFlag := fs.String("name", "", "新名称（项目名/目标名含空格时必须使用）")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		rem := fs.Args()
		var id, newName string
		if *nameFlag != "" {
			if len(rem) < 1 {
				return nil, fmt.Errorf("用法: project rename <id|名称> --name <新名称>")
			}
			id, newName = strings.Join(rem, " "), *nameFlag
		} else {
			if len(rem) < 2 {
				return nil, fmt.Errorf("用法: project rename <id|名称> <新名称>（名称含空格时用 --name 指定新名）")
			}
			id, newName = rem[0], strings.Join(rem[1:], " ")
		}
		return c.post("/projects", map[string]any{"action": "rename", "id": id, "name": newName})
	case "delete":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: project delete <id|名称>")
		}
		return c.post("/projects", map[string]any{"action": "delete", "id": strings.Join(args, " ")})
	case "close":
		return c.post("/projects", map[string]any{"action": "close"})
	default:
		return nil, fmt.Errorf("未知 project 子命令: %s（list|switch|create|rename|delete|close）", sub)
	}
}

// ---------- 代理生命周期 / CA（M10 补面） ----------

func cliProxy(c *client, pos []string) (any, error) {
	args := pos[1:]
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "start":
		return c.post("/proxy", map[string]any{"action": "start"})
	case "stop":
		return c.post("/proxy", map[string]any{"action": "stop"})
	default:
		return nil, fmt.Errorf("未知 proxy 子命令: %s（start|stop）", action)
	}
}

func cliCA(c *client, pos []string) (any, error) {
	args := pos[1:]
	action := "install"
	if len(args) > 0 {
		action = args[0]
	}
	if action != "install" {
		return nil, fmt.Errorf("未知 ca 子命令: %s（install）", action)
	}
	return c.post("/ca/install", nil)
}

// ---------- ADB 设备代理（M10 补面） ----------

func cliADB(c *client, pos []string) (any, error) {
	args := pos[1:]
	action := "devices"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("adb", flag.ContinueOnError)
	adbPath := fs.String("adb", "", "adb 可执行文件路径（默认取已配置设备）")
	serial := fs.String("serial", "", "设备序列号（多设备时必填）")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return nil, err
	}
	body := map[string]any{"adbPath": *adbPath, "serial": *serial}
	switch action {
	case "devices":
		// 已配置设备清单（GET /adb）
		return c.get("/adb")
	case "test", "set", "clear":
		return c.post("/adb/"+action, body)
	default:
		return nil, fmt.Errorf("未知 adb 子命令: %s（devices|test|set|clear）", action)
	}
}

// ---------- 域名组（M10 补面；rules/settings 类，受 --project 影响） ----------

func cliDomains(c *client, pos []string) (any, error) {
	args := pos[1:]
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "":
		return c.get(c.withProject("/domains"))
	case "get":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: domains get <id>")
		}
		return c.get(c.withProject("/domains/" + urlEncode(args[0])))
	case "delete", "rm":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: domains delete <id>")
		}
		return c.del(c.withProject("/domains/" + urlEncode(args[0])))
	case "save", "set":
		// domains save <id> <内容...>（每行一个域名，用 \n 分隔）
		if len(args) < 2 {
			return nil, fmt.Errorf("用法: domains save <id> <域名，空格或 \\n 分隔>")
		}
		id := args[0]
		content := strings.Join(args[1:], "\n")
		return c.post(c.withProject("/domains/"+urlEncode(id)), map[string]any{"content": content})
	case "import":
		fs := flag.NewFlagSet("domains-import", flag.ContinueOnError)
		id := fs.String("id", "", "导入后的组 id（缺省取来源文件名）")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		rem := fs.Args()
		if len(rem) < 1 {
			return nil, fmt.Errorf("用法: domains import <本地文件路径|http(s) URL> [--id <组id>]")
		}
		return c.post(c.withProject("/domains/import"), map[string]any{"source": rem[0], "id": *id})
	default:
		return nil, fmt.Errorf("未知 domains 子命令: %s（list|get|save|delete|import）", sub)
	}
}

// ---------- 调试重发（Composer，M10 补面） ----------

// repeatHeader 支持 --header 可重复传入，每个值整体作为一个 "Key: Value" 请求头；
// 值本身可含分号（如 Cookie: a=1; b=2），只按第一个冒号切分键值。
type repeatHeader []string

func (h *repeatHeader) String() string { return strings.Join(*h, "; ") }
func (h *repeatHeader) Set(v string) error {
	*h = append(*h, v)
	return nil
}

// parseComposeHeaders 把每个 --header 值整体解析为一个请求头。
// 只按第一个冒号切分键值，值本身可含分号（如 Cookie: a=1; b=2）与冒号；
// 多个头请重复传入 --header。
func parseComposeHeaders(headers []string) ([]map[string]string, error) {
	var hs []map[string]string
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("--header 格式须为 'Key: Value'（多个头请重复传入 --header）: %q", h)
		}
		hs = append(hs, map[string]string{"key": strings.TrimSpace(k), "value": strings.TrimSpace(v)})
	}
	return hs, nil
}

func cliCompose(c *client, pos []string) (any, error) {
	args := pos[1:]
	fs := flag.NewFlagSet("compose", flag.ContinueOnError)
	method := fs.String("method", "GET", "HTTP 方法")
	var headers repeatHeader
	fs.Var(&headers, "header", "请求头，可重复：--header 'Key: Value'（值可含分号如 Cookie）")
	body := fs.String("body", "", "请求体（字符串）")
	insecure := fs.Bool("insecure", false, "跳过 HTTPS 证书校验（等同 --skip-verify）")
	skipVerify := fs.Bool("skip-verify", false, "跳过 HTTPS 证书校验")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return nil, err
	}
	rem := fs.Args()
	if len(rem) < 1 {
		return nil, fmt.Errorf("用法: compose <URL> [--method GET] [--header 'Key: Value']... [--body 内容] [--insecure]")
	}
	hs, err := parseComposeHeaders(headers)
	if err != nil {
		return nil, err
	}
	return c.post("/compose", map[string]any{
		"url":        rem[0],
		"method":     *method,
		"headers":    hs,
		"body":       *body,
		"skipVerify": *insecure || *skipVerify,
	})
}

// reorderFlagsGlobal 只把已知全局 flag（--pretty/--addr/--token/--project/-P）提到最前，
// 其余参数（含子命令自有 flag 如 --filter）保持原位，交由子命令 flagSet 解析。
func reorderFlagsGlobal(args []string) []string {
	known := map[string]bool{"-pretty": true, "--pretty": true,
		"-addr": true, "--addr": true, "-token": true, "--token": true,
		"-project": true, "--project": true, "-P": true, "--P": true}
	// 取值型 flag（bool 的 --pretty 不吞下一 token）
	valueFlags := map[string]bool{"-addr": true, "--addr": true, "-token": true, "--token": true,
		"-project": true, "--project": true, "-P": true, "--P": true}
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
	project    string       // 目标项目（M9：全局 --project/-P；空=当前项目）
	http       *http.Client // 普通请求-响应（含快照拉取）
	streamHTTP *http.Client // SSE 事件流长连接（watch 专用：无总超时、独立连接不复用）
}

// withProject 给规则/设置类请求路径拼 ?project=（c.project 为空时原样返回）
func (c *client) withProject(path string) string {
	if c.project == "" {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "project=" + url.QueryEscape(c.project)
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

func (c *client) del(path string) (any, error) {
	raw, err := c.do(http.MethodDelete, path, nil)
	return rawMessage(raw), err
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
