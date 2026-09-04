package sysproxy

import "testing"

func TestMergeOverrideEmptyExisting(t *testing.T) {
	got := MergeOverride("", []string{"<-loopback>", "localhost", "trae.cn"})
	want := "<-loopback>;localhost;trae.cn"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMergeOverridePreservesUserValues(t *testing.T) {
	got := MergeOverride("my.corp.com;10.*", []string{"<-loopback>", "trae.cn"})
	// 用户值在前且原样保留，<-loopback> 提首
	want := "<-loopback>;my.corp.com;10.*;trae.cn"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMergeOverrideDedupCaseInsensitive(t *testing.T) {
	got := MergeOverride("Trae.cn;LOCALHOST", []string{"trae.cn", "localhost", "<-loopback>"})
	want := "<-loopback>;Trae.cn;LOCALHOST" // 去重但保留首次出现的原始大小写
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMergeOverrideSkipsEmptySegments(t *testing.T) {
	got := MergeOverride("a.com;; b.com ;", []string{"c.com"})
	want := "a.com;b.com;c.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMergeOverrideNoLoopback(t *testing.T) {
	got := MergeOverride("a.com", []string{"b.com"})
	want := "a.com;b.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
