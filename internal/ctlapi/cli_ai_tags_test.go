package ctlapi

import (
	"errors"
	"io"
	"testing"
	"time"
)

// TestNormalizeIgnoreKind 校验 tags ignore 的 kind 归一化：
// 服务端合法值 host|path|proc|method|status 原样；rules 侧 process 兼容映射为 proc；非法值为空。
func TestNormalizeIgnoreKind(t *testing.T) {
	cases := map[string]string{
		"host":    "host",
		"path":    "path",
		"proc":    "proc",
		"method":  "method",
		"status":  "status",
		"process": "proc", // 兼容 rules 侧写法
		" process ": "proc",
		" host ":  "host", // 规范值带空格也必须 trim 后返回（不得透传带空格值导致服务端 400）
		" method ": "method",
		"":        "",
		"procname": "",
		"PROCESS": "", // 大小写不做隐式转换（服务端仅小写）
	}
	for in, want := range cases {
		if got := normalizeIgnoreKind(in); got != want {
			t.Errorf("normalizeIgnoreKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// blockingReader Read 永久阻塞，直到 Close 被调用后返回 io.ErrClosedPipe。
type blockingReader struct {
	closed chan struct{}
}

func newBlockingReader() *blockingReader { return &blockingReader{closed: make(chan struct{})} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingReader) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// TestIdleTimeoutReader_Timeout 帧间空闲超时：watchdog 到期关闭底层连接，
// 阻塞的 Read 必须解除并返回包装 errAIStreamIdle 的错误（CLI 据此退出非 0）。
func TestIdleTimeoutReader_Timeout(t *testing.T) {
	r := newIdleTimeoutReader(newBlockingReader(), 30*time.Millisecond)
	start := time.Now()
	_, err := r.Read(make([]byte, 16))
	if err == nil {
		t.Fatal("空闲超时后 Read 应返回错误")
	}
	if !errors.Is(err, errAIStreamIdle) {
		t.Fatalf("Read 错误应包装 errAIStreamIdle，实际: %v", err)
	}
	if d := time.Since(start); d < 25*time.Millisecond || d > 2*time.Second {
		t.Fatalf("超时触发时机异常: %v", d)
	}
	// Close 幂等且不泄漏（watchdog goroutine 已自然退出）。
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// pulseReader 前 pulses 次 Read 各等 gap 后返回 1 字节，之后返回 EOF。
// 用于验证「每次收到数据都重置空闲定时器」：单段 gap < timeout，但累计时长 > timeout。
type pulseReader struct {
	gap    time.Duration
	pulses int
	n      int
}

func (s *pulseReader) Read(p []byte) (int, error) {
	if s.n >= s.pulses {
		return 0, io.EOF
	}
	s.n++
	time.Sleep(s.gap)
	return 1, nil
}
func (s *pulseReader) Close() error { return nil }

// TestIdleTimeoutReader_ActivityResets 多次脉冲（间隔 80ms < 300ms 超时，累计 480ms > 300ms）
// 必须因逐次 Reset 而不触发空闲超时、收齐全部字节；若 n>0 分支未 Reset，
// 首个 300ms 到期就会关闭连接，ReadAll 得到 errAIStreamIdle 而非完整数据。
func TestIdleTimeoutReader_ActivityResets(t *testing.T) {
	const timeout, gap = 300 * time.Millisecond, 80 * time.Millisecond
	const pulses = 6
	r := newIdleTimeoutReader(&pulseReader{gap: gap, pulses: pulses}, timeout)
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("持续有数据（间隔小于超时）不应报错，实际: %v", err)
	}
	if len(data) != pulses {
		t.Fatalf("应收齐 %d 字节，实际 %d", pulses, len(data))
	}
}
