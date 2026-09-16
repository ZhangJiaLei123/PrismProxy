package ctlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
)

// cliAI 处理 `ai` 子命令（HTTP 路由见 ai.go）。
//
//	ai config                       读取掩码配置（GET /ai/config）
//	ai config set --file JSON文件   部分更新配置（POST /ai/config；stdin 用 --file -）
//	ai test                         连通探测（POST /ai/test）
//	ai chat --mode M [--flow ID | --ids a,b] [--question 文本] [选项...]
//	                                 SSE 分析流，默认逐行打印 answer 文本；--raw 透传 ndjson 帧
func cliAI(c *client, pos []string) (any, error) {
	args := pos[1:]
	sub := "config"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "config":
		// ai config [set --file f]
		action := "get"
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			action, args = args[0], args[1:]
		}
		if action == "get" {
			return c.get("/ai/config")
		}
		if action != "set" {
			return nil, fmt.Errorf("用法: ai config [get|set --file JSON文件]")
		}
		fs := flag.NewFlagSet("ai-config-set", flag.ContinueOnError)
		file := fs.String("file", "", "JSON 部分更新文件（- = 从 stdin 读取）")
		if err := fs.Parse(reorderFlags(args)); err != nil {
			return nil, err
		}
		raw, err := readConfigJSON(*file, fs.Args())
		if err != nil {
			return nil, err
		}
		return c.post("/ai/config", raw)

	case "test":
		return c.post("/ai/test", nil)

	case "chat":
		return nil, runAIChat(c, args)

	default:
		return nil, fmt.Errorf("未知 ai 子命令: %s（config|test|chat）", sub)
	}
}

// readConfigJSON 读取配置 JSON：--file 指向文件；--file - 读 stdin；否则用剩余位置参数整体作为 JSON。
func readConfigJSON(file string, rest []string) (json.RawMessage, error) {
	var data []byte
	switch {
	case file == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("读取 stdin 失败: %w", err)
		}
		data = b
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
		data = b
	case len(rest) > 0:
		data = []byte(strings.Join(rest, " "))
	default:
		return nil, fmt.Errorf("需要 JSON：用 --file <文件>（或 --file - 从 stdin），或直接追加 JSON 文本")
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("不是合法 JSON: %w", err)
	}
	return json.RawMessage(data), nil
}

// runAIChat 发起一次 SSE 分析。默认人读模式：把 delta.text 增量直接写到 stdout；
// --raw 输出 ndjson（每行 {event,...}）。同步错误（未配置/参数/409）以普通错误返回。
func runAIChat(c *client, args []string) error {
	fs := flag.NewFlagSet("ai-chat", flag.ContinueOnError)
	mode := fs.String("mode", "", "分析模式：explain|intent|locate|flowmap")
	flowID := fs.String("flow", "", "单流 id（explain 模式）")
	idsCSV := fs.String("ids", "", "批量流 id，逗号分隔（intent/locate/flowmap）")
	question := fs.String("question", "", "分析目标/问题（locate/flowmap）")
	includeReq := fs.Bool("req-body", false, "分析时包含请求体")
	includeResp := fs.Bool("resp-body", false, "分析时包含响应体")
	raw := fs.Bool("raw", false, "透传 ndjson 帧（脚本用），默认仅打印答案文本")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if *mode == "" {
		return fmt.Errorf("用法: ai chat --mode explain|intent|locate|flowmap [--flow ID | --ids a,b] [--question 文本] [--req-body] [--resp-body] [--raw]")
	}
	req := AIChatRequest{
		Mode:     *mode,
		FlowID:   *flowID,
		Question: *question,
		Options: AIChatOptions{
			IncludeReqBody:  *includeReq,
			IncludeRespBody: *includeResp,
			Language:        "zh",
		},
	}
	if *idsCSV != "" {
		for _, id := range strings.Split(*idsCSV, ",") {
			if id = strings.TrimSpace(id); id != "" {
				req.IDs = append(req.IDs, id)
			}
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// SSE 长连接：无总超时、独立连接（不复用普通 15s client），避免长分析被超时掐断。
	stream := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/ai/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Close = true
	resp, err := stream.Do(httpReq)
	if err != nil {
		return fmt.Errorf("连接 AI 分析失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var e struct{ Error string `json:"error"` }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("AI 分析失败: HTTP %d", resp.StatusCode)
	}

	sc := newSSEScanner(resp.Body)
	for sc.Next() {
		event, data, _ := sc.Block()
		if event == "" {
			continue
		}
		if *raw {
			var m map[string]any
			if json.Unmarshal(data, &m) == nil {
				m["event"] = event
				if b, mErr := json.Marshal(m); mErr == nil {
					fmt.Println(string(b))
				}
			}
			continue
		}
		// 人读模式：仅打印答案文本增量；其余帧静默（meta/intent/match 等）。
		if event == AIEventDelta {
			var d struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(data, &d) == nil && d.Text != "" {
				fmt.Print(d.Text)
			}
		}
		if event == AIEventError {
			var d struct{ Message string `json:"message"` }
			if json.Unmarshal(data, &d) == nil {
				return fmt.Errorf("%s", d.Message)
			}
		}
	}
	if !*raw {
		fmt.Println() // 答案收尾换行
	}
	return nil
}
