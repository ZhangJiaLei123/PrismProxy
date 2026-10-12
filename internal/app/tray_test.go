package app

import "testing"

func TestTrayStateLine(t *testing.T) {
	cases := []struct {
		name string
		s    trayState
		want string
	}{
		{"无项目+运行中", trayState{proxyOn: true, proxyAddr: "127.0.0.1:9090"}, "未打开项目 · 代理 127.0.0.1:9090 运行中"},
		{"有项目+运行中", trayState{projectOn: true, projectName: "修车助手", proxyOn: true, proxyAddr: "127.0.0.1:9091"}, "项目 修车助手 · 代理 127.0.0.1:9091 运行中"},
		{"有项目+停止", trayState{projectOn: true, projectName: "修车助手"}, "项目 修车助手 · 代理已停止"},
		{"启动失败", trayState{projectOn: true, projectName: "p", startErr: "port in use"}, "项目 p · 代理启动失败"},
	}
	for _, c := range cases {
		if got := c.s.line(); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestTrayStateNameTruncate(t *testing.T) {
	long := make([]rune, trayStateMaxName+10)
	for i := range long {
		long[i] = 'a'
	}
	s := trayState{projectOn: true, projectName: string(long)}
	// 截断后 = 上限 rune + 省略号，且总行宽可控（tooltip 128 字符上限）
	want := string(long[:trayStateMaxName]) + "…"
	if got := s.line(); got != "项目 "+want+" · 代理已停止" {
		t.Errorf("truncate: got %q", got)
	}
	// 中文按 rune 截断不产生乱码半字节
	cn := make([]rune, trayStateMaxName+5)
	for i := range cn {
		cn[i] = '修'
	}
	got := truncateName(string(cn))
	want2 := string(cn[:trayStateMaxName]) + "…"
	if got != want2 {
		t.Errorf("truncate cn: got %q want %q", got, want2)
	}
}
