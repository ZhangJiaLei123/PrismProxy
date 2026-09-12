package app

import "testing"

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.2", "1.2.1", 1},
		{"1.2.2", "v1.2.1", 1},
		{"v1.2.1", "v1.2.1", 0},
		{"v1.3", "v1.2.9", 1},
		{"v2", "v1.9.9", 1},
		{"v1.2.0", "v1.2.1", -1},
		{"v1.2.10", "v1.2.9", 1}, // 数字比较而非字符串比较
		{"v1.2.2-beta", "v1.2.1", 1}, // 预发布后缀按数字段比较
		{"v1.2.2", "v1.2.2-beta.1", 0}, // 同上，两侧丢弃后缀视为相等
		{"", "v0.0.1", -1},
		{"v1.2.2-rc1", "v1.2.1", 1},
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
