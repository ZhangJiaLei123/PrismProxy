package capture

import (
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"
)

// FlowStore 存储抽象（避免 capture 与 store 循环依赖）
type FlowStore interface {
	Add(f *Flow)
}

// Recorder 负责分配 Flow ID、落库并输出日志
type Recorder struct {
	store FlowStore
	seq   atomic.Uint64
	// Filter 记录过滤器：返回 false 则该 Flow 不记录（正常转发不受影响）。
	// 捕获规则 exclude / 进程规则 exclude 走此（方案 §4.7）。nil=全记录。
	Filter func(f *Flow) bool
}

func NewRecorder(st FlowStore) *Recorder {
	return &Recorder{store: st}
}

// NewID M1 临时格式：时间戳毫秒-序号（后续视需要换 ULID）
func (r *Recorder) NewID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixMilli(), r.seq.Add(1))
}

// Begin 一条 Flow 开始（落库，UI 实时出现 pending/streaming 行；Store 按 ID upsert）
func (r *Recorder) Begin(f *Flow) {
	if r.Filter != nil && !r.Filter(f) {
		return
	}
	r.store.Add(f)
}

// Update 中途状态推进（如响应头已到、进入流式；不落日志）
func (r *Recorder) Update(f *Flow) {
	if r.Filter != nil && !r.Filter(f) {
		return
	}
	r.store.Add(f)
}

// Finish 收尾一条 Flow：补计时、入库、打日志
func (r *Recorder) Finish(f *Flow) {
	if r.Filter != nil && !r.Filter(f) {
		return
	}
	if f.Timing != nil {
		f.Timing.Duration = time.Since(f.Timing.Start)
	}
	r.store.Add(f)

	proc := "unknown"
	if f.Process != nil {
		if f.Process.Name != "" {
			proc = fmt.Sprintf("%s(pid=%d)", f.Process.Name, f.Process.PID)
		} else {
			proc = fmt.Sprintf("pid=%d", f.Process.PID)
		}
	}
	method, url, status := "", "", "-"
	if f.Request != nil {
		method, url = f.Request.Method, f.Request.URL
	}
	if f.Response != nil && f.Response.StatusCode != 0 {
		status = strconv.Itoa(f.Response.StatusCode)
	}
	log.Printf("flow %s [%s] %s %s -> %s state=%s up=%dB down=%dB dur=%v err=%q",
		f.ID, proc, method, url, status, f.State, f.BytesUp, f.BytesDown, f.Timing.Duration, f.Err)
}
