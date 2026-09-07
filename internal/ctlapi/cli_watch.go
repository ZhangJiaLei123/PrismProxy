package ctlapi

// cli_watch.go：`cli flows watch` 实时推送命令（CLI实时推送设计.md §6）。
// 先订阅（SSE 连接建立即开始缓冲帧）后拉快照（GET /flows），重叠区靠 upsert 幂等 +
// evict 忽略未知 ID 天然安全；reset/断线自动重连并重打快照，重连重读 endpoint 文件
// （实例重启 token 即变）。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"
)

// errReset 收到服务端 reset 帧：丢弃本地状态、立即重连重拉快照（非致命）
var errReset = errors.New("server reset")

// watchOpts watch 子命令解析结果
type watchOpts struct {
	filter string
	status bool
	format string // ndjson | sse
}

// runWatch 是 watch 命令的进程级入口：重连循环 + 指数退避 + Ctrl+C 退出码 0。
// 返回进程退出码：0=用户中断，1=连接失败/实例消失（连续 5 次重连失败）。
func runWatch(configDir, addrFlag, tokenFlag string, pos []string) int {
	opts, err := parseWatchOpts(pos)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	const maxFails = 5
	backoff := 500 * time.Millisecond
	fails := 0
	for {
		if ctx.Err() != nil {
			return 0 // Ctrl+C
		}
		err := watchOnce(ctx, configDir, addrFlag, tokenFlag, opts)
		if ctx.Err() != nil {
			return 0 // Ctrl+C 导致的中断不算失败
		}
		if errors.Is(err, errReset) {
			// 背压 reset：立即重连重拉快照（服务端要求对账），退避重置
			fails = 0
			continue
		}
		fails++
		if fails >= maxFails {
			fmt.Fprintf(os.Stderr, "错误: 连续 %d 次无法连接事件流（实例未运行？）: %v\n", maxFails, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "事件流中断（%v），%s 后重连（%d/%d）\n", err, backoff, fails, maxFails)
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(backoff):
		}
		if backoff < 8*time.Second {
			backoff *= 2 // 0.5s→1s→2s→4s→8s 封顶
		}
	}
}

// watchOnce 建立一次 SSE 连接并输出：snapshot → 增量帧，直到连接结束/reset/Ctrl+C。
func watchOnce(ctx context.Context, configDir, addrFlag, tokenFlag string, opts watchOpts) error {
	// 每次重连都重新构造 client = 重读 ctl-endpoint.json（实例重启 token/端口即变）；
	// 显式 --addr/--token 时沿用命令行值。
	c, err := newWatchClient(configDir, addrFlag, tokenFlag)
	if err != nil {
		return err
	}

	channels := "flows"
	if opts.status {
		channels = "flows,status"
	}
	eventsURL := c.base + "/events?channels=" + channels
	if opts.filter != "" {
		eventsURL += "&filter=" + url.QueryEscape(opts.filter)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	// 事件流长连接：Connection: close 使其响应连接不归还 keep-alive 池，
	// 彻底杜绝快照请求复用该连接、把事件帧当快照响应消费掉。
	req.Close = true
	resp, err := c.streamHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("连接事件流失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var e struct{ Error string `json:"error"` }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("订阅失败: %s", e.Error)
		}
		return fmt.Errorf("订阅失败: HTTP %d", resp.StatusCode)
	}

	// 连接已建立 = 订阅成功，服务端开始推帧（在连接缓冲中排队）。此时拉快照，
	// 保证"先订阅后快照"：快照与增量重叠，靠幂等收敛（设计 §5）。
	snapRaw, err := c.getSnapshot(ctx, opts.filter)
	if err != nil {
		return fmt.Errorf("拉取快照失败: %w", err)
	}
	if err := emitSnapshot(opts, snapRaw); err != nil {
		return err
	}

	// 逐帧解析并转发
	sc := newSSEScanner(resp.Body)
	for sc.Next() {
		event, data, raw := sc.Block()
		if event == "" {
			continue // 心跳注释块（`: ping`），无 event 行
		}
		if opts.format == "sse" {
			// 原样透传标准 SSE 文本（含 id: 行）
			if _, err := os.Stdout.WriteString(raw); err != nil {
				return err
			}
			if event == "reset" {
				return errReset
			}
			continue
		}
		// ndjson：合并为单行 {"event":...,...data 字段}
		if event == "reset" {
			fmt.Println(`{"event":"reset","reason":"backpressure"}`)
			return errReset
		}
		if event == "open" {
			continue // ndjson 下 open 仅调试用，不输出
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			continue // 忽略无法解析的帧
		}
		m["event"] = event
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		fmt.Println(string(b))
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err // EOF/读错误 → 触发重连
	}
	return ctx.Err()
}

// emitSnapshot 输出全量快照行：ndjson 为 {"event":"snapshot","flows":[...]}，
// sse 为一帧标准 SSE（event: snapshot）。之后服务端帧原样透传，整段输出均为合法 SSE。
func emitSnapshot(opts watchOpts, flowsRaw json.RawMessage) error {
	if opts.format == "sse" {
		b, _ := json.Marshal(map[string]any{"flows": flowsRaw})
		fmt.Printf("event: snapshot\ndata: %s\n\n", b)
		return nil
	}
	fmt.Printf(`{"event":"snapshot","flows":%s}`+"\n", flowsRaw)
	return nil
}

// getSnapshot 拉全量快照（GET /flows，带同一 filter，保证基线与增量口径一致）。
func (c *client) getSnapshot(ctx context.Context, filter string) (json.RawMessage, error) {
	u := c.base + "/flows"
	if filter != "" {
		u += "?filter=" + url.QueryEscape(filter)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.RawMessage(data), nil
}

// newWatchClient 构造 watch 专用 client：无总超时（现有 15s client 会掐断 SSE 长连接），
// 仅保留连接/Dial 级超时，读侧保活靠服务端 15s 心跳。
//
// 关键：事件流与快照请求共用同一 client，但事件流的长连接必须独占 TCP 连接——
// 若快照请求复用了事件流所在的 keep-alive 连接，事件帧会被快照响应消费掉
// （之后增量帧永远读不到）。故事件流 Transport 禁用 KeepAlives，快照请求另建连接。
func newWatchClient(configDir, addr, token string) (*client, error) {
	c, err := newClient(configDir, addr, token)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	c.http = &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			DisableKeepAlives:     false,
			// 不设 ResponseHeader 之后的任何读超时；无 Client.Timeout
		},
	}
	// 事件流使用独立 Transport 并禁止连接复用，确保不与快照/后续请求共享连接
	c.streamHTTP = &http.Client{
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   true,
		},
	}
	return c, nil
}

// parseWatchOpts 从 `flows watch ...` 参数解析 watch 选项（子命令自有 flag）。
func parseWatchOpts(pos []string) (watchOpts, error) {
	args := pos[1:] // 去掉 "flows"
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	filter := fs.String("filter", "", "按 host/URL/path 子串过滤（同 flows list）")
	withStatus := fs.Bool("status", false, "追加订阅 status 频道（代理/系统代理状态）")
	format := fs.String("format", "ndjson", "输出格式：ndjson（默认，每行一个 JSON）| sse（原样 SSE 文本）")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return watchOpts{}, err
	}
	if *format != "ndjson" && *format != "sse" {
		return watchOpts{}, fmt.Errorf("--format 须为 ndjson|sse")
	}
	return watchOpts{filter: *filter, status: *withStatus, format: *format}, nil
}

// subCmd 返回 args 中第一个非 flag 的位置参数（如 flows 的子命令 watch/list）。
func subCmd(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// ---------- SSE 帧解析 ----------

// sseBlock 一个 SSE 事件块：event 名、data 负载、原始文本（sse 透传用）
type sseBlock struct {
	event string
	data  []byte
	raw   string
}

// sseScanner 按 SSE 规范（空行分隔事件块）从流中逐块解析。
type sseScanner struct {
	r   *bufio.Reader
	blk sseBlock
	err error
}

func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{r: bufio.NewReaderSize(r, 64<<10)}
}

// Next 读取下一个事件块（按空行分隔）；纯注释/心跳块（无 event 行）跳过。
func (s *sseScanner) Next() bool {
	for {
		var event string
		var dataLines []string
		var raw strings.Builder
		hasField := false // 块内是否出现过 event/data/id 字段（区别于纯心跳块）
		for {
			line, err := s.r.ReadString('\n')
			if len(line) > 0 {
				raw.WriteString(line)
				trimmed := strings.TrimRight(line, "\r\n")
				if trimmed == "" {
					// 空行 = 事件块结束（心跳块也由 `: ping` + 空行构成）
					break
				}
				if strings.HasPrefix(trimmed, ":") {
					continue // 注释/心跳行
				}
				hasField = true
				field, value, _ := strings.Cut(trimmed, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "event":
					event = value
				case "data":
					dataLines = append(dataLines, value)
				case "id":
					// id 帧字段本期仅调试，sse 透传已含 raw；ndjson 忽略
				}
			}
			if err != nil {
				s.err = nil
				if err != io.EOF {
					s.err = err
				}
				if !hasField && raw.Len() == 0 {
					return false // 干净 EOF，无残留
				}
				// EOF 且缓冲中有内容：作为最后一个块处理（流末尾可能无尾空行）
				goto emit
			}
		}
		if !hasField {
			continue // 纯注释/心跳块：跳过
		}
	emit:
		if event == "" {
			continue // 无 event 行的块无法分发
		}
		s.blk = sseBlock{
			event: event,
			data:  []byte(strings.Join(dataLines, "\n")),
			raw:   raw.String(),
		}
		return true
	}
}

func (s *sseScanner) Block() (event string, data []byte, raw string) {
	return s.blk.event, s.blk.data, s.blk.raw
}

func (s *sseScanner) Err() error { return s.err }
