package store

import (
	"fmt"
	"sync"

	"prismproxy/internal/capture"
)

// MaxPinned 置顶上限（方案 §4.4）：固定顶部展示、不参与淘汰、会话内不持久化
const MaxPinned = 200

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
		id, ok := s.evictOldestLocked()
		if !ok {
			break // 剩余全为置顶流，无法继续淘汰
		}
		evictIDs = append(evictIDs, id)
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

// evictOldestLocked 移除最旧的非置顶流（持锁调用）。置顶流跳过（方案 §4.4：不参与淘汰）；
// 全部为置顶流时返回 ok=false。删除后切片回归 FIFO 顺序（next 归零），
// 环形不变量"写满时 next 指向最旧"在后续 Add 中自然恢复。
func (s *Store) evictOldestLocked() (string, bool) {
	n := len(s.flows)
	if n == 0 {
		return "", false
	}
	start := 0
	if n == s.cap {
		start = s.next
	}
	idx := -1
	for i := 0; i < n; i++ {
		cand := s.flows[(start+i)%n]
		if !cand.Pinned {
			idx = (start + i) % n
			break
		}
	}
	if idx < 0 {
		return "", false // 全部置顶，无法淘汰
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
		pinned := old.Pinned // 置顶状态以 store 为准，capture 重发的 Flow 不携带该位
		*old = *f           // 保留切片中的指针身份，List 快照无需重排
		old.Pinned = pinned
		events = append(events, Event{Type: "update", Flow: old})
		if ids := s.evictOverBudgetLocked(1); len(ids) > 0 {
			events = append(events, Event{Type: "evict", IDs: ids})
		}
	} else {
		nf := *f
		if len(s.flows) < s.cap {
			s.flows = append(s.flows, &nf)
			s.bodyBytes += flowBodyBytes(&nf)
			s.acct[f.ID] = flowBodyBytes(&nf)
			s.index[f.ID] = &nf
			events = append(events, Event{Type: "new", Flow: &nf})
			if ids := s.evictOverBudgetLocked(1); len(ids) > 0 {
				events = append(events, Event{Type: "evict", IDs: ids})
			}
		} else {
			// 满环：环形覆盖最旧的非置顶流；全部置顶则丢弃新流（极端场景，受 2000/200 容量比限制）
			slot := -1
			for i := 0; i < s.cap; i++ {
				cand := s.flows[(s.next+i)%s.cap]
				if !cand.Pinned {
					slot = (s.next + i) % s.cap
					break
				}
			}
			if slot < 0 {
				s.mu.Unlock()
				return
			}
			victim := s.flows[slot]
			delete(s.index, victim.ID)
			s.bodyBytes -= s.acct[victim.ID]
			delete(s.acct, victim.ID)
			events = append(events, Event{Type: "evict", IDs: []string{victim.ID}})
			s.flows[slot] = &nf
			s.next = (slot + 1) % s.cap
			s.bodyBytes += flowBodyBytes(&nf)
			s.acct[f.ID] = flowBodyBytes(&nf)
			s.index[f.ID] = &nf
			events = append(events, Event{Type: "new", Flow: &nf})
			if ids := s.evictOverBudgetLocked(1); len(ids) > 0 {
				events = append(events, Event{Type: "evict", IDs: ids})
			}
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

// Clear 清空非置顶流并对其 ID 发 "evict"；置顶流保留（方案 §4.4）
func (s *Store) Clear() {
	s.mu.Lock()
	var ids []string
	kept := s.flows[:0]
	var keptBytes int64
	for _, f := range s.flows {
		if f.Pinned {
			kept = append(kept, f)
			keptBytes += s.acct[f.ID]
			continue
		}
		ids = append(ids, f.ID)
		delete(s.index, f.ID)
		delete(s.acct, f.ID)
	}
	s.flows = kept
	s.next = 0 // 置顶流数 < cap，后续写入走追加路径
	s.bodyBytes = keptBytes
	s.mu.Unlock()

	if len(ids) > 0 {
		s.emit([]Event{{Type: "evict", IDs: ids}})
	}
}

// SetPinned 设置/取消置顶（方案 §4.4）。置顶流固定顶部展示、不参与淘汰、Clear 保留；
// 置顶数达 MaxPinned 上限后再置顶返回错误。状态变更发 "update" 事件；状态不变为 no-op。
func (s *Store) SetPinned(id string, pinned bool) (*capture.Flow, error) {
	s.mu.Lock()
	f, ok := s.index[id]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("流不存在或已淘汰：%s", id)
	}
	if f.Pinned == pinned {
		s.mu.Unlock()
		return f, nil
	}
	if pinned {
		n := 0
		for _, fl := range s.flows {
			if fl.Pinned {
				n++
			}
		}
		if n >= MaxPinned {
			s.mu.Unlock()
			return nil, fmt.Errorf("置顶数量已达上限 %d", MaxPinned)
		}
	}
	f.Pinned = pinned
	s.mu.Unlock()

	s.emit([]Event{{Type: "update", Flow: f}})
	return f, nil
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
