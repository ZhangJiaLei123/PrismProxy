package ctlapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeFlow 实现 Filterable 的测试流（字段导出以便 JSON 序列化验证内容）
type fakeFlow struct {
	Host string `json:"host"`
	URL  string `json:"url"`
	Path string `json:"path"`
}

func (f fakeFlow) FilterFields() (host, urlStr, path string) { return f.Host, f.URL, f.Path }

func drain(sink <-chan Frame, into *[]Frame, done chan struct{}) {
	for {
		select {
		case f, ok := <-sink:
			if !ok {
				close(done)
				return
			}
			*into = append(*into, f)
		case <-time.After(300 * time.Millisecond):
			close(done)
			return
		}
	}
}

// fan-out：订阅者收到 publish 的帧；非订阅频道不收到
func TestHubFanOut(t *testing.T) {
	h := NewHub()
	unsub, sink, _, ok := h.Subscribe([]string{"flows"}, "")
	if !ok {
		t.Fatal("Subscribe 应成功")
	}
	defer unsub()

	h.Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"a", "b"}})
	h.Publish("status", map[string]any{"running": true}) // 未订阅 status

	var got []Frame
	done := make(chan struct{})
	go drain(sink, &got, done)
	<-done

	if len(got) != 1 {
		t.Fatalf("flows 订阅者应只收 1 帧（status 不应到达），得 %d", len(got))
	}
	if got[0].Event != "flows" || !strings.Contains(string(got[0].Data), `"evict"`) {
		t.Fatalf("帧内容异常: event=%s data=%s", got[0].Event, got[0].Data)
	}
	if got[0].ID == 0 {
		t.Fatal("fan-out 帧应携带单调 ID")
	}
}

// filter：不匹配 host/URL/path 的 upsert 不投递；evict 不受 filter 影响
func TestHubFilter(t *testing.T) {
	h := NewHub()
	unsub, sink, _, ok := h.Subscribe([]string{"flows"}, "Baidu") // 大写验证不区分大小写
	if !ok {
		t.Fatal("Subscribe 应成功")
	}
	defer unsub()

	h.Publish("flows", FlowsUpsert{Type: "upsert", Flows: []Filterable{
		fakeFlow{Host: "api.baidu.com", URL: "https://api.baidu.com/x", Path: "/x"},
		fakeFlow{Host: "other.com", URL: "https://other.com/y", Path: "/y"},
	}})
	// evict 帧即使不匹配 filter 也必须送达
	h.Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"x"}})

	var got []Frame
	done := make(chan struct{})
	go drain(sink, &got, done)
	<-done

	if len(got) != 2 {
		t.Fatalf("应收 upsert（1 条匹配）+ evict 共 2 帧，得 %d", len(got))
	}
	// 只做反序列化到具体结构（FlowsUpsert.Flows 是接口字段，生产侧只 Marshal 不 Unmarshal；
	// 测试按过滤结果的具体 fakeFlow 反序列化验证）
	var u struct {
		Type  string     `json:"type"`
		Flows []fakeFlow `json:"flows"`
	}
	if err := json.Unmarshal(got[0].Data, &u); err != nil {
		t.Fatalf("upsert 帧解析失败: %v", err)
	}
	if len(u.Flows) != 1 || u.Flows[0].Host != "api.baidu.com" {
		t.Fatalf("filter 后应只剩 baidu 1 条流，得 %+v", u.Flows)
	}
	if !strings.Contains(string(got[1].Data), `"evict"`) {
		t.Fatalf("第二帧应为 evict: %s", got[1].Data)
	}
}

// 背压：订阅者缓冲满 → 摘除 + reset 通知；Hub.SubscriberCount 归零
func TestHubBackpressureReset(t *testing.T) {
	h := NewHub()
	unsub, sink, reset, ok := h.Subscribe([]string{"flows"}, "")
	if !ok {
		t.Fatal("Subscribe 应成功")
	}
	defer unsub()

	// 不读 sink，灌爆缓冲（容量 subBufferSize，多灌一些）
	for i := 0; i < subBufferSize+10; i++ {
		h.Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"z"}})
	}

	select {
	case <-reset:
	case <-time.After(time.Second):
		t.Fatal("缓冲满后应收到 reset 通知")
	}
	if c := h.SubscriberCount(); c != 0 {
		t.Fatalf("溢出订阅者应被摘除，剩余 %d", c)
	}
	// unsub 幂等：摘除后再调不应 panic（sink 未被 close，GC 回收）
	unsub()
	_ = sink
}

// 订阅者上限：第 MaxSubscribers+1 个订阅返回 ok=false
func TestHubSubscriberLimit(t *testing.T) {
	h := NewHub()
	var unsubs []func()
	for i := 0; i < MaxSubscribers; i++ {
		unsub, _, _, ok := h.Subscribe([]string{"flows"}, "")
		if !ok {
			t.Fatalf("第 %d 个订阅应成功", i+1)
		}
		unsubs = append(unsubs, unsub)
	}
	if _, _, _, ok := h.Subscribe([]string{"flows"}, ""); ok {
		t.Fatal("超过上限应返回 ok=false")
	}
	for _, u := range unsubs {
		u()
	}
	if c := h.SubscriberCount(); c != 0 {
		t.Fatalf("全部退订后应为 0，得 %d", c)
	}
}

// Close 后订阅者 sink 关闭（handler 收到 EOF）；Publish 不再投递
func TestHubClose(t *testing.T) {
	h := NewHub()
	unsub, sink, _, ok := h.Subscribe([]string{"flows"}, "")
	if !ok {
		t.Fatal("Subscribe 应成功")
	}
	defer unsub()
	h.Close()
	select {
	case _, ok := <-sink:
		if ok {
			t.Fatal("Close 后 sink 应关闭")
		}
	case <-time.After(time.Second):
		t.Fatal("Close 后 sink 应立即关闭")
	}
	h.Publish("flows", FlowsEvict{Type: "evict", IDs: []string{"x"}}) // 不应 panic/投递
}
