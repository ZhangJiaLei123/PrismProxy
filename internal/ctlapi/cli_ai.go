package ctlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// AI SSE 帧间空闲上限：本地大模型单帧思考可能数分钟无输出，取 5 分钟兜底
	// 半开连接/服务端静默挂死；正常路径的上游看门狗（120s）会更早发 error 帧。
	aiChatIdleTimeout = 5 * time.Minute
)

// errAIStreamIdle 标记帧间空闲超时（idleTimeoutReader 到期关闭连接）。
var errAIStreamIdle = errors.New("AI 分析流空闲超时，连接可能已挂起")

// idleTimeoutReader 给 SSE 响应体加「距上一字节」的帧间空闲超时：
// 每次成功读到数据就重置定时器；超时则关闭底层连接解除阻塞中的 Read。
type idleTimeoutReader struct {
	rc       io.ReadCloser
	timer    *time.Timer
	timeout  time.Duration
	done     chan struct{}
	stopOnce sync.Once
	timedOut atomic.Bool
}

func newIdleTimeoutReader(rc io.ReadCloser, d time.Duration) *idleTimeoutReader {
	r := &idleTimeoutReader{rc: rc, timeout: d, timer: time.NewTimer(d), done: make(chan struct{})}
	go func() {
		select {
		case <-r.timer.C:
			r.timedOut.Store(true)
			_ = rc.Close()
		case <-r.done:
		}
	}()
	return r
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		if !r.timer.Stop() {
			select {
			case <-r.timer.C:
			default:
			}
		}
		r.timer.Reset(r.timeout)
	}
	if err != nil && r.timedOut.Load() {
		return n, fmt.Errorf("%w（%s 无数据）", errAIStreamIdle, r.timeout)
	}
	return n, err
}

func (r *idleTimeoutReader) Close() error {
	r.stopOnce.Do(func() { close(r.done) })
	if !r.timer.Stop() {
		select {
		case <-r.timer.C:
		default:
		}
	}
	return r.rc.Close()
}

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
		// runAIChat 自行向 stdout 输出流内容；成功时用 errOutputHandled 跳过分发的 printResult（否则尾部多打 null）。
		if err := runAIChat(c, args); err != nil {
			return nil, err
		}
		return nil, errOutputHandled

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

	// SSE 长连接：无总超时、独立连接（不复用普通 15s client），避免长分析被超时掐断；
	// 仅保留连接/响应头级超时，读侧帧间空闲由 idleTimeoutReader 兜底半开/静默挂死。
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	stream := &http.Client{Transport: &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableKeepAlives:     true,
	}}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/ai/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Close = true
	resp, err := stream.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil // 用户中断（Ctrl+C），退出码 0
		}
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

	bodyReader := newIdleTimeoutReader(resp.Body, aiChatIdleTimeout)
	defer bodyReader.Close()
	sc := newSSEScanner(bodyReader)
	sawDone := false
	var streamErr error
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
		} else if event == AIEventDelta {
			// 人读模式：仅打印答案文本增量；其余帧静默（meta/intent/match 等）。
			var d struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(data, &d) == nil && d.Text != "" {
				fmt.Print(d.Text)
			}
		}
		switch event {
		case AIEventDone:
			sawDone = true
		case AIEventError:
			var d struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(data, &d) == nil {
				streamErr = fmt.Errorf("%s", d.Message)
			} else {
				streamErr = fmt.Errorf("AI 分析流返回错误帧但内容无法解析")
			}
		}
	}
	// 收尾判定（两模式一致）：error 帧 > 读取错误（排除用户中断）> 缺 done 帧。
	if streamErr == nil {
		if rErr := sc.Err(); rErr != nil {
			if errors.Is(rErr, errAIStreamIdle) {
				streamErr = rErr
			} else if ctx.Err() == nil {
				streamErr = fmt.Errorf("读取 AI 分析流中断: %w", rErr)
			}
		} else if !sawDone && ctx.Err() == nil {
			streamErr = fmt.Errorf("AI 分析流异常结束（未收到完成帧）")
		}
	}
	if !*raw && streamErr == nil {
		fmt.Println() // 答案收尾换行
	}
	if *raw {
		return streamErr // 脚本模式也以退出码区分成败（帧已透传）
	}
	if streamErr != nil {
		fmt.Println() // 人读模式：错误另起一行，不接在半截答案后
	}
	return streamErr
}
