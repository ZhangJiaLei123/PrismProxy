package rules

import "testing"

func TestDefaultsAllAllow(t *testing.T) {
	e, err := NewEngine(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !e.ShouldCapture("example.com", "https://example.com/", "GET") {
		t.Fatal("捕获默认应为 include")
	}
	if !e.ShouldDecrypt("example.com") {
		t.Fatal("解密默认应为 MITM")
	}
	if !e.ShouldCaptureProcess("x.exe") {
		t.Fatal("进程默认应为 include")
	}
	// nil Engine 接收者同样走默认
	var ne *Engine
	if !ne.ShouldCapture("a", "b", "c") || !ne.ShouldDecrypt("a") || !ne.ShouldCaptureProcess("a") {
		t.Fatal("nil Engine 应返回默认动作")
	}
}

func TestFirstMatchWins(t *testing.T) {
	e, err := NewEngine([]CaptureRule{
		{Action: ActionExclude, Host: "api.example.com"},
		{Action: ActionInclude, Host: "example.com"},
	}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.ShouldCapture("api.example.com", "/", "GET") {
		t.Fatal("首条命中应生效：api.example.com 应 exclude")
	}
	if !e.ShouldCapture("www.example.com", "/", "GET") {
		t.Fatal("www.example.com 应命中第二条 include")
	}
	if !e.ShouldCapture("other.com", "/", "GET") {
		t.Fatal("未命中应走默认 include")
	}
}

func TestBareDomainMatchesSelfAndSub(t *testing.T) {
	e, _ := NewEngine(nil, []DecryptRule{{Action: ActionBypass, Host: "example.com"}}, nil, nil)
	for _, h := range []string{"example.com", "a.example.com", "deep.a.example.com", "EXAMPLE.com"} {
		if e.ShouldDecrypt(h) {
			t.Fatalf("%s 应命中 bypass", h)
		}
	}
	for _, h := range []string{"notexample.com", "example.com.evil.com", "example"} {
		if !e.ShouldDecrypt(h) {
			t.Fatalf("%s 不应命中", h)
		}
	}
	// *. 前缀等价裸域名
	e2, _ := NewEngine(nil, []DecryptRule{{Action: ActionBypass, Host: "*.example.com"}}, nil, nil)
	if e2.ShouldDecrypt("example.com") || e2.ShouldDecrypt("a.example.com") {
		t.Fatal("*.example.com 应等价裸域名")
	}
}

func TestGroupReference(t *testing.T) {
	groups := map[string][]string{"ai": {"trae.cn", "doubao.com"}}
	e, _ := NewEngine([]CaptureRule{{Action: ActionExclude, Host: "@ai"}}, nil, nil, groups)
	if e.ShouldCapture("api.trae.cn", "/", "GET") {
		t.Fatal("@ai 组内子域应命中")
	}
	if e.ShouldCapture("doubao.com", "/", "GET") {
		t.Fatal("@ai 组内裸域应命中")
	}
	if !e.ShouldCapture("example.com", "/", "GET") {
		t.Fatal("组外域名不应命中")
	}
	// 引用不存在的组：永不命中（走默认）
	e2, _ := NewEngine([]CaptureRule{{Action: ActionExclude, Host: "@nope"}}, nil, nil, groups)
	if !e2.ShouldCapture("anything.com", "/", "GET") {
		t.Fatal("不存在的组应不命中，走默认 include")
	}
}

func TestCaptureRuleMethodAndURLRe(t *testing.T) {
	e, err := NewEngine([]CaptureRule{
		{Action: ActionExclude, URLRe: `/telemetry/`, Method: "post, put"},
	}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.ShouldCapture("x.com", "https://x.com/telemetry/v1", "POST") {
		t.Fatal("POST + 正则命中应 exclude")
	}
	if !e.ShouldCapture("x.com", "https://x.com/telemetry/v1", "GET") {
		t.Fatal("方法不匹配应不命中")
	}
	if !e.ShouldCapture("x.com", "https://x.com/api", "POST") {
		t.Fatal("URL 正则不匹配应不命中")
	}
}

func TestProcessRuleCaseInsensitive(t *testing.T) {
	e, _ := NewEngine(nil, nil, []ProcessRule{{Action: ActionExclude, Name: "DNPlayer.exe"}}, nil)
	if e.ShouldCaptureProcess("dnplayer.exe") {
		t.Fatal("进程名匹配应不区分大小写")
	}
	if !e.ShouldCaptureProcess("") {
		t.Fatal("进程未知视为未命中，走默认 include")
	}
	if !e.ShouldCaptureProcess("other.exe") {
		t.Fatal("未命中走默认 include")
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewEngine([]CaptureRule{{Action: "nope"}}, nil, nil, nil); err == nil {
		t.Fatal("非法动作应报错")
	}
	if _, err := NewEngine([]CaptureRule{{Action: ActionExclude, URLRe: "["}}, nil, nil, nil); err == nil {
		t.Fatal("非法正则应报错")
	}
	if _, err := NewEngine(nil, []DecryptRule{{Action: ActionBypass}}, nil, nil); err == nil {
		t.Fatal("解密规则空 host 应报错")
	}
	if _, err := NewEngine(nil, []DecryptRule{{Action: ActionInclude, Host: "x.com"}}, nil, nil); err == nil {
		t.Fatal("解密规则动作只允许 mitm|bypass")
	}
	if _, err := NewEngine(nil, nil, []ProcessRule{{Action: ActionExclude}}, nil); err == nil {
		t.Fatal("进程规则空 name 应报错")
	}
}

func TestHolderHotSwap(t *testing.T) {
	h := &Holder{}
	if !h.Get().ShouldDecrypt("a.com") {
		t.Fatal("空 Holder 应走默认")
	}
	e, _ := NewEngine(nil, []DecryptRule{{Action: ActionBypass, Host: "a.com"}}, nil, nil)
	h.Set(e)
	if h.Get().ShouldDecrypt("a.com") {
		t.Fatal("热替换后应命中 bypass")
	}
}
