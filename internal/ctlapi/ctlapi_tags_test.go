package ctlapi

// M12 标签/复盘路由矩阵 + 静态托管测试（设计 §4.4/§6.3、AC10 路由面）。

import (
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// tagDo 发起带 token 的请求（路径为 /api/v1 之后部分），返回状态码与响应体。
func tagDo(t *testing.T, method, addr, token, path, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, "http://"+addr+"/api/v1"+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// TestTagRoutesMatrix 方法×路径矩阵：合法组合 200，错误方法/未鉴权 405/401。
func TestTagRoutesMatrix(t *testing.T) {
	_, addr, token := startTestServer(t, &fakeService{})

	ok := func(code int, want int, ctx string) {
		t.Helper()
		if code != want {
			t.Fatalf("%s: 状态 %d，期望 %d", ctx, code, want)
		}
	}

	// 标签列表
	code, _ := tagDo(t, "GET", addr, token, "/tags", "")
	ok(code, 200, "GET /tags")
	code, _ = tagDo(t, "GET", addr, "", "/tags", "")
	ok(code, 401, "GET /tags 无 token")
	code, _ = tagDo(t, "DELETE", addr, token, "/tags", "")
	ok(code, 405, "DELETE /tags")

	// 打标
	code, _ = tagDo(t, "POST", addr, token, "/tags/flows", `{"ids":["f1"],"name":"x"}`)
	ok(code, 200, "POST /tags/flows")
	code, _ = tagDo(t, "GET", addr, token, "/tags/flows", "")
	ok(code, 405, "GET /tags/flows（打标仅 POST；详情走 /tags/flows/{id}）")

	// 标签下流分页
	tid := "t_001122334455"
	code, body := tagDo(t, "GET", addr, token, "/tags/"+tid+"/flows?limit=10&offset=20", "")
	ok(code, 200, "GET /tags/{id}/flows")
	if !strings.Contains(body, `"limit":10`) || !strings.Contains(body, `"offset":20`) {
		t.Fatalf("分页参数未透传: %s", body)
	}
	// limit 上限钳制：5000 → 1000
	code, body = tagDo(t, "GET", addr, token, "/tags/"+tid+"/flows?limit=5000", "")
	ok(code, 200, "GET /tags/{id}/flows 大 limit")
	if !strings.Contains(body, `"limit":1000`) {
		t.Fatalf("limit 应钳制到 1000: %s", body)
	}

	// 单流详情 / 正文
	code, _ = tagDo(t, "GET", addr, token, "/tags/flows/f100", "")
	ok(code, 200, "GET /tags/flows/{id}")
	code, _ = tagDo(t, "GET", addr, token, "/tags/flows/f100/body?which=req", "")
	ok(code, 200, "GET /tags/flows/{id}/body")
	code, _ = tagDo(t, "POST", addr, token, "/tags/flows/f100", "")
	ok(code, 405, "POST /tags/flows/{id}")

	// 重命名
	code, _ = tagDo(t, "POST", addr, token, "/tags/"+tid+"/rename", `{"name":"新名"}`)
	ok(code, 200, "POST /tags/{id}/rename")
	code, _ = tagDo(t, "GET", addr, token, "/tags/"+tid+"/rename", "")
	ok(code, 404, "GET /tags/{id}/rename 应不落任何分支 → 404")

	// 删除标签
	code, body = tagDo(t, "DELETE", addr, token, "/tags/"+tid+"?flows=1", "")
	ok(code, 200, "DELETE /tags/{id}?flows=1")
	if !strings.Contains(body, `"deletedFlows":0`) {
		t.Fatalf("删除响应缺 deletedFlows: %s", body)
	}
	code, _ = tagDo(t, "GET", addr, token, "/tags/"+tid, "")
	ok(code, 404, "GET /tags/{id}（无此单层分支）")
}

// TestStaticHosting 复盘页静态托管（不套 auth；/api/ 未匹配 404；短链 302）。
func TestStaticHosting(t *testing.T) {
	dist := fstest.MapFS{
		"review.html":     &fstest.MapFile{Data: []byte("<!doctype html>review")},
		"assets/x.js":     &fstest.MapFile{Data: []byte("console.log(1)")},
		"index.html":      &fstest.MapFile{Data: []byte("main")},
	}
	srv := NewServer("127.0.0.1:0", "tok-static", "", &fakeService{}, fs.FS(dist))
	if err := srv.Start(); err != nil {
		t.Fatalf("启动: %v", err)
	}
	t.Cleanup(srv.Close)
	base := "http://" + srv.Addr()

	get := func(path string) (int, string) {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}

	// /review.html 无需 token 即可访问（静态页不套 auth）
	if code, body := get("/review.html"); code != 200 || !strings.Contains(body, "review") {
		t.Fatalf("/review.html = %d %q", code, body)
	}
	// /assets 静态资源
	if code, _ := get("/assets/x.js"); code != 200 {
		t.Fatalf("/assets/x.js = %d", code)
	}
	// 短链 302：http.Get 默认跟随重定向，须用不跟随 client 才能观测 302
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/", "/review"} {
		r, err := noRedirect.Get(base + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		r.Body.Close()
		if r.StatusCode != 302 || r.Header.Get("Location") != "/review.html" {
			t.Fatalf("%s = %d Location=%q，期望 302 → /review.html", p, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// /api/ 未匹配路径：即便无 token 也应是路由层 404（不被静态兜底吞掉）
	if code, _ := get("/api/v1/nope"); code != 404 {
		t.Fatalf("未匹配 /api/ 路径 = %d，期望 404", code)
	}
	// 数据 API 仍需 token（静态放行不波及 /api）
	req, _ := http.NewRequest("GET", base+"/api/v1/tags", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("无 token GET /tags: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("无 token GET /tags = %d，期望 401", resp2.StatusCode)
	}
}

// TestStaticDisabled nil staticFS 时非 API 路径 404（测试/未注入 embed 场景）。
func TestStaticDisabled(t *testing.T) {
	srv := NewServer("127.0.0.1:0", "tok", "", &fakeService{}, nil)
	if err := srv.Start(); err != nil {
		t.Fatalf("启动: %v", err)
	}
	t.Cleanup(srv.Close)
	resp, err := http.Get("http://" + srv.Addr() + "/review.html")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("未托管静态资源时 /review.html = %d，期望 404", resp.StatusCode)
	}
}
