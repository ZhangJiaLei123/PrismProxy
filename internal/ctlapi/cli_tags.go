package ctlapi

import (
	"flag"
	"fmt"
	"net/url"
	"strings"
)

// cliTags 处理 `tags` 子命令（标签与数据复盘，HTTP 路由见 server.go handleTags/handleTagSub）。
//
//	tags list                                   标签概览（GET /tags）
//	tags add --name 名称 <flowId...>            打标/追加到标签（POST /tags/flows）
//	tags flows <tagId|all> [过滤参数...]         标签内流（GET /tags/{id}/flows）
//	tags flow <flowId>                          归档单流详情
//	tags body <flowId> [--which req|resp]       归档单流正文
//	tags rename <tagId> --name 新名称
//	tags delete <tagId> [--flows]              删除标签（--flows 连带删流）
//	tags histogram <tagId> [--scope --buckets]  密度直方图
//	tags ignore list                            忽略名单
//	tags ignore add --kind host|path|proc --value 值 [--note 备注]
//	tags ignore rm --kind K --value V
func cliTags(c *client, pos []string) (any, error) {
	args := pos[1:]
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "":
		return c.get("/tags")

	case "add":
		fs := flag.NewFlagSet("tags-add", flag.ContinueOnError)
		name := fs.String("name", "", "标签名（必填）")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		ids := fs.Args()
		if strings.TrimSpace(*name) == "" {
			return nil, fmt.Errorf("用法: tags add --name 名称 <flowId...>（--name 必填）")
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("用法: tags add --name 名称 <flowId...>（至少一个 flowId）")
		}
		return c.post("/tags/flows", map[string]any{"ids": ids, "name": *name})

	case "flows":
		if len(args) < 1 || strings.HasPrefix(args[0], "-") {
			return nil, fmt.Errorf("用法: tags flows <tagId|all> [--scope archived|all] [--q 子串] [--limit N] [--offset N] [--sort time|host|...] [--dir asc|desc] [--show-ignored]")
		}
		id := args[0]
		fs := flag.NewFlagSet("tags-flows", flag.ContinueOnError)
		scope := fs.String("scope", "archived", "范围：archived（默认）|all（仅 id=all 生效）")
		q := fs.String("q", "", "method/host/path 子串过滤")
		limit := fs.Int("limit", 200, "每页条数（上限 1000）")
		offset := fs.Int("offset", 0, "偏移")
		sort := fs.String("sort", "", "排序键：time|method|status|host|path|size|proc")
		dir := fs.String("dir", "", "方向：asc|desc")
		showIgnored := fs.Bool("show-ignored", false, "显示已忽略的流")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return nil, err
		}
		v := url.Values{}
		if *scope != "" {
			v.Set("scope", *scope)
		}
		if *q != "" {
			v.Set("q", *q)
		}
		v.Set("limit", fmt.Sprintf("%d", *limit))
		if *offset > 0 {
			v.Set("offset", fmt.Sprintf("%d", *offset))
		}
		if *sort != "" {
			v.Set("sort", *sort)
		}
		if *dir != "" {
			v.Set("dir", *dir)
		}
		if *showIgnored {
			v.Set("showIgnored", "1")
		}
		return c.get("/tags/" + url.PathEscape(id) + "/flows?" + v.Encode())

	case "flow":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: tags flow <flowId>")
		}
		return c.get("/tags/flows/" + url.PathEscape(args[0]))

	case "body":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: tags body <flowId> [--which req|resp]")
		}
		id := args[0]
		fs := flag.NewFlagSet("tags-body", flag.ContinueOnError)
		which := fs.String("which", "resp", "req|resp（默认 resp）")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return nil, err
		}
		return c.get("/tags/flows/" + url.PathEscape(id) + "/body?which=" + url.QueryEscape(*which))

	case "rename":
		if len(args) < 1 || strings.HasPrefix(args[0], "-") {
			return nil, fmt.Errorf("用法: tags rename <tagId> --name 新名称")
		}
		id := args[0]
		fs := flag.NewFlagSet("tags-rename", flag.ContinueOnError)
		name := fs.String("name", "", "新名称（必填）")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return nil, err
		}
		if strings.TrimSpace(*name) == "" {
			return nil, fmt.Errorf("--name 必填")
		}
		return c.post("/tags/"+url.PathEscape(id)+"/rename", map[string]any{"name": *name})

	case "delete", "rm":
		if len(args) < 1 {
			return nil, fmt.Errorf("用法: tags delete <tagId> [--flows]")
		}
		fs := flag.NewFlagSet("tags-delete", flag.ContinueOnError)
		withFlows := fs.Bool("flows", false, "连带删除归档流（默认仅删标签、保留流）")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return nil, err
		}
		path := "/tags/" + url.PathEscape(args[0])
		if *withFlows {
			path += "?flows=1"
		}
		return c.del(path)

	case "histogram":
		if len(args) < 1 || strings.HasPrefix(args[0], "-") {
			return nil, fmt.Errorf("用法: tags histogram <tagId|all> [--scope archived|all] [--buckets N]")
		}
		id := args[0]
		fs := flag.NewFlagSet("tags-hist", flag.ContinueOnError)
		scope := fs.String("scope", "archived", "archived|all")
		buckets := fs.Int("buckets", 120, "桶数（上限 500）")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return nil, err
		}
		v := url.Values{}
		v.Set("scope", *scope)
		v.Set("buckets", fmt.Sprintf("%d", *buckets))
		return c.get("/tags/" + url.PathEscape(id) + "/histogram?" + v.Encode())

	case "ignore":
		return cliTagIgnore(c, args)

	default:
		return nil, fmt.Errorf("未知 tags 子命令: %s（list|add|flows|flow|body|rename|delete|histogram|ignore）", sub)
	}
}

// cliTagIgnore 处理 `tags ignore list|add|rm`（/tags/ignores）。
func cliTagIgnore(c *client, args []string) (any, error) {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "":
		return c.get("/tags/ignores")
	case "add":
		fs := flag.NewFlagSet("tags-ignore-add", flag.ContinueOnError)
		kind := fs.String("kind", "", "类型：host|path|proc|method|status")
		value := fs.String("value", "", "忽略值（域名/路径/进程名）")
		note := fs.String("note", "", "备注（可选）")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		normKind := normalizeIgnoreKind(*kind)
		if normKind == "" || strings.TrimSpace(*value) == "" {
			return nil, fmt.Errorf("用法: tags ignore add --kind host|path|proc|method|status --value 值 [--note 备注]")
		}
		body := map[string]any{"kind": normKind, "value": *value}
		if *note != "" {
			body["note"] = *note
		}
		return c.post("/tags/ignores", body)
	case "rm", "delete":
		fs := flag.NewFlagSet("tags-ignore-rm", flag.ContinueOnError)
		kind := fs.String("kind", "", "类型：host|path|proc|method|status")
		value := fs.String("value", "", "忽略值")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		normKind := normalizeIgnoreKind(*kind)
		if normKind == "" || strings.TrimSpace(*value) == "" {
			return nil, fmt.Errorf("用法: tags ignore rm --kind host|path|proc|method|status --value 值")
		}
		// value 可能含 "/"（path），走 query。
		q := url.Values{}
		q.Set("kind", normKind)
		q.Set("value", *value)
		return c.del("/tags/ignores?" + q.Encode())
	default:
		return nil, fmt.Errorf("未知 tags ignore 子命令: %s（list|add|rm）", sub)
	}
}

// normalizeIgnoreKind 归一化忽略类型到服务端口径（persist.ValidIgnoreKind）：
// host|path|proc|method|status；兼容 rules 侧的 "process" 写法，其余非法值返空串。
func normalizeIgnoreKind(k string) string {
	switch t := strings.TrimSpace(k); t {
	case "host", "path", "proc", "method", "status":
		return t
	case "process":
		return "proc"
	default:
		return ""
	}
}
