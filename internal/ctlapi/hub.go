// hub.go：SSE 实时推送的订阅者管理与 fan-out（CLI实时推送设计.md §4.1）。
// Hub 不依赖 main 包类型：flows 过滤所需字段经 Filterable 接口暴露
// （ctlapi 不能 import main，反向即循环依赖）。
package ctlapi

import (
	"encoding/json"
	"strings"
	"sync"
)

// MaxSubscribers 并发订阅者上限：每个订阅者 = 一条 handler goroutine + 128 帧缓冲
// （upsert 帧可达数十 KB），不设上限会被本机异常进程刷连接耗尽内存；超出返回 503。
const MaxSubscribers = 32

// subBufferSize 每订阅者帧缓冲容量：合帧后频率 ≤20 帧/秒，约 6 秒背压容差。
const subBufferSize = 128

// Frame 一帧待写出的 SSE 事件（event 名 + 已序列化的 data 字节 + 单调序号）。
// ID>0 时写出 `id:` 行（fan-out 的频道帧）；seed/open/reset 等控制帧 ID=0 不携带。
type Frame struct {
	ID    int64
	Event string
	Data  []byte
}

// Filterable 由接线层的 FlowMeta 实现，供 Hub 按订阅者 filter 过滤 flows upsert。
type Filterable interface {
	FilterFields() (host, url, path string)
}

// FlowsUpsert / FlowsEvict flows 频道帧结构（接线层构造，Hub 负责过滤与序列化）。
type FlowsUpsert struct {
	Type  string       `json:"type"` // "upsert"
	Flows []Filterable `json:"flows"`
}

// FlowsEvict evict 帧：不经 filter 过滤（淘汰通知必须送达，否则客户端残留失效 ID）。
type FlowsEvict struct {
	Type string   `json:"type"` // "evict"
	IDs  []string `json:"ids"`
}

// MarshalJSON 自定义序列化：Flows 是 []Filterable 接口切片，
// encoding/json 遇到接口类型字段无法导出（会输出 [{}]），改为逐条序列化具体类型后组装。
func (u FlowsUpsert) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteString(`{"type":`)
	tb, err := json.Marshal(u.Type)
	if err != nil {
		return nil, err
	}
	sb.Write(tb)
	sb.WriteString(`,"flows":[`)
	for i, f := range u.Flows {
		if i > 0 {
			sb.WriteByte(',')
		}
		fb, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		sb.Write(fb)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String()), nil
}

type subscriber struct {
	ch     chan Frame
	reset  chan struct{} // 缓冲满时关闭：handler 收到后发 reset 帧并断开
	closed bool

	channels map[string]bool
	filter   string // ToLower 后的子串；空=不过滤
}

// Hub 向所有 SSE 订阅者 fan-out 事件（server 持有）。
type Hub struct {
	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	nextID  int64
	closed  bool
}

// NewHub 创建空 Hub。
func NewHub() *Hub { return &Hub{subs: make(map[*subscriber]struct{})} }

// Subscribe 注册订阅者；返回的 unsub 由 handler 在连接关闭时调用（幂等）。
// channels 为已校验的频道白名单集合；filter 为 flows 子串过滤。
// 订阅者达上限返回 ok=false（handler 据此回 503）。
func (h *Hub) Subscribe(channels []string, filter string) (unsub func(), sink <-chan Frame, reset <-chan struct{}, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) >= MaxSubscribers {
		return nil, nil, nil, false
	}
	set := make(map[string]bool, len(channels))
	for _, c := range channels {
		set[c] = true
	}
	s := &subscriber{
		ch:       make(chan Frame, subBufferSize),
		reset:    make(chan struct{}),
		channels: set,
		filter:   strings.ToLower(strings.TrimSpace(filter)),
	}
	h.subs[s] = struct{}{}
	unsub = func() {
		h.mu.Lock()
		if _, exists := h.subs[s]; exists {
			delete(h.subs, s)
			close(s.ch)
		}
		h.mu.Unlock()
	}
	return unsub, s.ch, s.reset, true
}

// Publish 由接线层在事件发生时调用（channel="flows"|"status"）。
// flows 传 FlowsUpsert/FlowsEvict，status 传任意可 JSON 序列化快照。
// 非阻塞：订阅者缓冲满则摘除并通知其 handler 发 reset（§5 背压）。
func (h *Hub) Publish(channel string, payload any) {
	h.mu.Lock()
	if h.closed || len(h.subs) == 0 {
		h.mu.Unlock()
		return
	}

	// 无 filter 的订阅者共享一份预 marshal 字节；有 filter 的各自筛选后单独 marshal。
	// evict 与 status 帧与 filter 无关，同样走共享字节。
	var shared []byte
	upsert, isUpsert := payload.(FlowsUpsert)
	if !isUpsert {
		shared = marshalFrame(payload)
	}
	h.nextID++
	id := h.nextID

	for s := range h.subs {
		if !s.channels[channel] {
			continue
		}
		var data []byte
		if isUpsert {
			if ff := upsertFor(s, upsert); ff != nil {
				data = marshalFrame(ff)
			} else {
				// 该订阅者 filter 筛掉了全部流：此帧对它无内容可发
				continue
			}
		} else {
			data = shared
		}
		select {
		case s.ch <- Frame{ID: id, Event: channel, Data: data}:
		default:
			// 缓冲满：摘除订阅者并通知 handler 发 reset 帧。
			// 只 close(reset) 不 close(ch)——否则 handler 的 sink 分支与 reset 分支
			// 同时就绪，select 可能选中 sink 关闭而漏掉 reset 帧；
			// ch 已无写入者（订阅者已摘除），随 handler 退出被 GC。
			delete(h.subs, s)
			if !s.closed {
				s.closed = true
				close(s.reset)
			}
		}
	}
	h.mu.Unlock()
}

// upsertFor 按订阅者 filter 筛选 flows；无 filter 原样返回。
// 匹配口径与 flows list --filter 一致：host/URL/path 子串、不区分大小写。
func upsertFor(s *subscriber, u FlowsUpsert) any {
	if s.filter == "" {
		return u
	}
	kept := make([]Filterable, 0, len(u.Flows))
	for _, f := range u.Flows {
		host, urlstr, path := f.FilterFields()
		if strings.Contains(strings.ToLower(host), s.filter) ||
			strings.Contains(strings.ToLower(urlstr), s.filter) ||
			strings.Contains(strings.ToLower(path), s.filter) {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		return nil // 触发调用方跳过该帧
	}
	return FlowsUpsert{Type: u.Type, Flows: kept}
}

// Close 关闭全部订阅者 channel，SSE handler 随之退出，客户端立即收到 EOF。
// Server.Close 时在 http.Server.Shutdown 之前调用（Shutdown 不会主动断开活跃长连接）。
func (h *Hub) Close() {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		for s := range h.subs {
			delete(h.subs, s)
			close(s.ch)
		}
	}
	h.mu.Unlock()
}

// SubscriberCount 当前订阅者数（测试/诊断用）。
func (h *Hub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func marshalFrame(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":"frame marshal failed"}`)
	}
	return b
}
