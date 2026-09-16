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

// slowReader 第一次 Read 等待 delay（不超过 timeout），之后返回 EOF。
type slowReader struct {
	delay time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(s.delay)
	return 0, io.EOF
}
func (s *slowReader) Close() error { return nil }

// TestIdleTimeoutReader_ActivityResets 在超时窗口内有数据/返回时不误报空闲超时。
func TestIdleTimeoutReader_ActivityResets(t *testing.T) {
	r := newIdleTimeoutReader(&slowReader{delay: 20 * time.Millisecond}, 200*time.Millisecond)
	defer r.Close()
	if _, err := r.Read(make([]byte, 16)); !errors.Is(err, io.EOF) {
		t.Fatalf("窗口内结束应得到底层 EOF，实际: %v", err)
	}
}
