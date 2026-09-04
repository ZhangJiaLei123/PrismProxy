package store

import (
	"sync"

	"prismproxy/internal/capture"
)

// Event 存储变更事件（UI 增量推送的数据源）
type Event struct {
	Type string        // "new" | "update" | "evict"
	Flow *capture.Flow // new/update 时携带
	IDs  []string      // evict 时携带
}

// Store 内存环形缓冲：按 ID upsert，写满后覆盖最旧记录并发出淘汰事件。
// 字节预算（M4）：按 Flow 请求/响应 body 实际字节统计，超预算时从最旧端淘汰；
// body 本身已由 capture.MaxBodyCapture 截断兜底。
type Store struct {
	mu        sync.RWMutex
	cap       int
	maxBytes  int64 // body 字节预算；<=0 不限
	bodyBytes int64 // 当前全部 Flow 的 body 字节合计
	flows     []*capture.Flow
	next      int // 写满后下一条覆盖位置
	index     map[string]*capture.Flow
	// acct 按 ID 记录"上次入库时计入预算的 body 字节数"。
	// 不能用 stored Flow 现算：调用方持有共享 Message 指针原地写 body（recorder 即如此），
	// 现算会得到与最新值相同的数字，差值恒为 0，预算漏记。
	acct map[string]int64

	subsMu sync.RWMutex
	subs   []func(Event)
}

func New(capacity int) *Store {
	if capacity <= 0 {
		capacity = 2000
	}
	return &Store{
		cap:   capacity,
		flows: make([]*capture.Flow, 0, capacity),
		index: make(map[string]*capture.Flow),
		acct:  make(map[string]int64),
	}
}

// SetLimits 调整容量与 body 字节预算（M4 设置项）；收缩时立即淘汰最旧记录
func (s *Store) SetLimits(capacity int, maxBytes int64) {
	if capacity <= 0 {
		capacity = 2000
	}
	s.mu.Lock()
	s.cap = capacity
	s.maxBytes = maxBytes
	var evictIDs []string
	for len(s.flows) > s.cap {
		if id, ok := s.evictOldestLocked(); ok {
			evictIDs = append(evictIDs, id)
		}
	}
	evictIDs = append(evictIDs, s.evictOverBudgetLocked(0)...)
	s.mu.Unlock()

	if len(evictIDs) > 0 {
		s.emit([]Event{{Type: "evict", IDs: evictIDs}})
	}
}

// flowBodyBytes 一条 Flow 占用的 body 字节（请求体+响应体）
func flowBodyBytes(f *capture.Flow) int64 {
	var n int64
	if f.Request != nil {
		n += int64(len(f.Request.Body))
	}
	if f.Response != nil {
		n += int64(len(f.Response.Body))
	}
	return n
}

// evictOldestLocked 移除最旧一条（持锁调用）。删除后切片回归 FIFO 顺序（next 归零），
// 环形不变量"写满时 next 指向最旧"在后续 Add 中自然恢复。
func (s *Store) evictOldestLocked() (string, bool) {
	n := len(s.flows)
	if n == 0 {
		return "", false
	}
	idx := 0
	if n == s.cap {
		idx = s.next
	}
	victim := s.flows[idx]
	delete(s.index, victim.ID)
	s.bodyBytes -= s.acct[victim.ID]
	delete(s.acct, victim.ID)
	copy(s.flows[idx:n-1], s.flows[idx+1:n])
	s.flows[n-1] = nil
	s.flows = s.flows[:n-1]
	s.next = 0
	return victim.ID, true
}

// evictOverBudgetLocked 超字节预算时从最旧端淘汰，至少保留 keepMin 条（持锁调用）
func (s *Store) evictOverBudgetLocked(keepMin int) []string {
	var ids []string
	for s.maxBytes > 0 && s.bodyBytes > s.maxBytes && len(s.flows) > keepMin {
		id, ok := s.evictOldestLocked()
		if !ok {
			break
		}
		ids = append(ids, id)
	}
	return ids
}

// Add 按 ID upsert：已存在则原位更新并发 "update"，否则追加并发 "new"；
// 环形覆盖或超字节预算时先对被淘汰 ID 发 "evict"
func (s *Store) Add(f *capture.Flow) {
	var events []Event

	s.mu.Lock()
	if old, ok := s.index[f.ID]; ok {
		nb := flowBodyBytes(f)
		s.bodyBytes += nb - s.acct[f.ID]
		s.acct[f.ID] = nb
		*old = *f // 保留切片中的指针身份，List 快照无需重排
		events = append(events, Event{Type: "update", Flow: old})
		if ids := s.evictOverBudgetLocked(1); len(ids) > 0 {
			events = append(events, Event{Type: "evict", IDs: ids})
		}
	} else {
		nf := *f
		s.bodyBytes += flowBodyBytes(&nf)
		if len(s.flows) < s.cap {
			s.flows = append(s.flows, &nf)
		} else {
			victim := s.flows[s.next]
			delete(s.index, victim.ID)
			s.bodyBytes -= s.acct[victim.ID]
			delete(s.acct, victim.ID)
			events = append(events, Event{Type: "evict", IDs: []string{victim.ID}})
			s.flows[s.next] = &nf
			s.next = (s.next + 1) % s.cap
		}
		s.acct[f.ID] = flowBodyBytes(&nf)
		s.index[f.ID] = &nf
		events = append(events, Event{Type: "new", Flow: &nf})
		if ids := s.evictOverBudgetLocked(1); len(ids) > 0 {
			events = append(events, Event{Type: "evict", IDs: ids})
		}
	}
	s.mu.Unlock()

	s.emit(events)
}

// Get O(1) 按 ID 查详情；ok=false 表示不存在或已淘汰
func (s *Store) Get(id string) (*capture.Flow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.index[id]
	return f, ok
}

// Clear 清空并对全部 ID 发 "evict"
func (s *Store) Clear() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.flows))
	for _, f := range s.flows {
		ids = append(ids, f.ID)
	}
	s.flows = s.flows[:0]
	s.next = 0
	s.bodyBytes = 0
	s.index = make(map[string]*capture.Flow)
	s.acct = make(map[string]int64)
	s.mu.Unlock()

	if len(ids) > 0 {
		s.emit([]Event{{Type: "evict", IDs: ids}})
	}
}

// List 返回当前快照（环形覆盖后不保证严格时间序，UI 以事件流为准）
func (s *Store) List() []*capture.Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*capture.Flow, len(s.flows))
	copy(out, s.flows)
	return out
}

// Subscribe 订阅存储事件；回调在锁外执行（允许回调内再调 Store 方法）
func (s *Store) Subscribe(fn func(Event)) {
	s.subsMu.Lock()
	s.subs = append(s.subs, fn)
	s.subsMu.Unlock()
}

func (s *Store) emit(events []Event) {
	s.subsMu.RLock()
	subs := make([]func(Event), len(s.subs))
	copy(subs, s.subs)
	s.subsMu.RUnlock()
	for _, ev := range events {
		for _, fn := range subs {
			fn(ev)
		}
	}
}
