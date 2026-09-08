package ctlapi

import "testing"

// TestParseComposeHeaders_CookieSemicolon 回归：单个 --header 值内含分号（Cookie），
// 必须整体作为一个头，不得按分号拆成多个（M10 L1 遗留，真机实测发现）。
func TestParseComposeHeaders_CookieSemicolon(t *testing.T) {
	hs, err := parseComposeHeaders([]string{
		"Content-Type: application/json",
		"Cookie: a=1; b=2",
	})
	if err != nil {
		t.Fatalf("parseComposeHeaders 报错: %v", err)
	}
	if len(hs) != 2 {
		t.Fatalf("应解析为 2 个头，实际 %d: %#v", len(hs), hs)
	}
	if hs[0]["key"] != "Content-Type" || hs[0]["value"] != "application/json" {
		t.Fatalf("头1异常: %#v", hs[0])
	}
	if hs[1]["key"] != "Cookie" || hs[1]["value"] != "a=1; b=2" {
		t.Fatalf("Cookie 值内分号被误拆: %#v", hs[1])
	}
}

// TestParseComposeHeaders_InvalidMissingColon 缺冒号须报错。
func TestParseComposeHeaders_InvalidMissingColon(t *testing.T) {
	if _, err := parseComposeHeaders([]string{"BadHeaderNoColon"}); err == nil {
		t.Fatal("缺冒号的 --header 应报错")
	}
}
