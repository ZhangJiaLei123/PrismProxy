package proxypool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prismproxy/internal/settings"
)

// ---------- 解析夹具（设计 §5.4：text/json 两套样例，真账号接入时校准） ----------

func TestParseFetchJSONListObject(t *testing.T) {
	now := time.Now()
	body := `{"code":0,"msg":"ok","data":{"surplus_ip_quantity":9850,"list":[
		{"ip":"1.2.3.4","port":8080,"expire_time":4102444800,"username":"u1","password":"p1","region":"江苏 南京"},
		{"ip":"5.6.7.8","port":"9090","ttl":300,"account":"u2","pwd":"p2"}
	]}}`
	entries, surplus, err := parseFetchBody([]byte(body), now)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if surplus != 9850 {
		t.Fatalf("surplus=%d, 期望 9850", surplus)
	}
	if len(entries) != 2 {
		t.Fatalf("条目数=%d, 期望 2", len(entries))
	}
	e := entries[0]
	if e.Host != "1.2.3.4" || e.Port != 8080 || e.Username != "u1" || e.Password != "p1" {
		t.Fatalf("首条解析错误: %+v", e)
	}
	if e.ExpireAt.Unix() != 4102444800 {
		t.Fatalf("expire_time 解析错误: %v", e.ExpireAt)
	}
	if e.Region != "江苏 南京" {
		t.Fatalf("region=%q", e.Region)
	}
	if e.Source != Vendor91HTTP {
		t.Fatalf("source=%q", e.Source)
	}
	// 第二条：port 为字符串、ttl 相对秒、account/pwd 键
	e2 := entries[1]
	if e2.Port != 9090 || e2.Username != "u2" || e2.Password != "p2" {
		t.Fatalf("次条解析错误: %+v", e2)
	}
	if d := time.Until(e2.ExpireAt); d < 290*time.Second || d > 310*time.Second {
		t.Fatalf("ttl 相对秒换算异常: %v", d)
	}
}

func TestParseFetchJSONProxyListStrings(t *testing.T) {
	now := time.Now()
	body := `{"code":200,"data":{"proxy_list":["9.9.9.9:10000,4102444800,u3,p3","8.8.8.8:20000"]}}`
	entries, _, err := parseFetchBody([]byte(body), now)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("条目数=%d, 期望 2", len(entries))
	}
	if entries[0].Addr() != "9.9.9.9:10000" || entries[0].Username != "u3" || entries[0].Password != "p3" {
		t.Fatalf("扩展行解析错误: %+v", entries[0])
	}
	if entries[0].ExpireAt.Unix() != 4102444800 {
		t.Fatalf("expire 解析错误: %v", entries[0].ExpireAt)
	}
	if entries[1].Addr() != "8.8.8.8:20000" || !entries[1].ExpireAt.IsZero() {
		t.Fatalf("纯 ip:port 行解析错误: %+v", entries[1])
	}
}

func TestParseFetchJSONTopArrayAndAddr(t *testing.T) {
	now := time.Now()
	// data 顶层数组 + addr 合一键 + expire_time_str 时间串
	body := `{"code":0,"data":[{"addr":"2.2.2.2:3128","expire_time_str":"2100-01-01 08:00:00","user":"u4","pass":"p4","province":"江苏","city":"南京"}]}`
	entries, _, err := parseFetchBody([]byte(body), now)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("条目数=%d", len(entries))
	}
	e := entries[0]
	if e.Addr() != "2.2.2.2:3128" || e.Username != "u4" || e.Password != "p4" {
		t.Fatalf("解析错误: %+v", e)
	}
	if e.ExpireAt.IsZero() || e.ExpireAt.Year() != 2100 {
		t.Fatalf("expire_time_str 解析错误: %v", e.ExpireAt)
	}
	if e.Region != "江苏 南京" {
		t.Fatalf("region 拼接错误: %q", e.Region)
	}
}

func TestParseFetchJSONErrorCode(t *testing.T) {
	body := `{"code":1001,"msg":"提取过于频繁"}`
	_, _, err := parseFetchBody([]byte(body), time.Now())
	if err == nil || !strings.Contains(err.Error(), "提取过于频繁") {
		t.Fatalf("错误码未携带 msg: %v", err)
	}
}

func TestParseFetchJSONEmptyList(t *testing.T) {
	body := `{"code":0,"data":{"list":[]}}`
	_, _, err := parseFetchBody([]byte(body), time.Now())
	if err == nil {
		t.Fatalf("空列表应报错")
	}
}

func TestParseFetchText(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		line     string
		wantAddr string
		check    func(t *testing.T, e ProxyEntry)
	}{
		{"纯ipport", "1.1.1.1:8080", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if !e.ExpireAt.IsZero() {
				t.Fatalf("不应有 expire: %v", e.ExpireAt)
			}
		}},
		{"expire时间戳加账密", "1.1.1.1:8080,4102444800,u,p", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if e.ExpireAt.Unix() != 4102444800 || e.Username != "u" || e.Password != "p" {
				t.Fatalf("v1 四段解析错误: %+v", e)
			}
		}},
		{"expire时间串加账密", "1.1.1.1:8080,2100-01-01 08:00:00,u,p", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if e.ExpireAt.Year() != 2100 || e.Username != "u" {
				t.Fatalf("时间串四段解析错误: %+v", e)
			}
		}},
		{"老版地区邮编秒数", "1.1.1.1:8080,江苏 南京,210000,600", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if e.Region != "江苏 南京" {
				t.Fatalf("老版 region=%q", e.Region)
			}
			if d := time.Until(e.ExpireAt); d < 590*time.Second || d > 610*time.Second {
				t.Fatalf("老版剩余秒数换算异常: %v", d)
			}
			if e.Username != "" {
				t.Fatalf("老版不应有账密: %+v", e)
			}
		}},
		{"三段账密", "1.1.1.1:8080,u5,p5", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if e.Username != "u5" || e.Password != "p5" {
				t.Fatalf("三段账密解析错误: %+v", e)
			}
		}},
		{"两段剩余秒", "1.1.1.1:8080,300", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if d := time.Until(e.ExpireAt); d < 290*time.Second || d > 310*time.Second {
				t.Fatalf("两段剩余秒异常: %v", d)
			}
		}},
		{"两段地区", "1.1.1.1:8080,广东 深圳", "1.1.1.1:8080", func(t *testing.T, e ProxyEntry) {
			if e.Region != "广东 深圳" {
				t.Fatalf("两段地区=%q", e.Region)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := parseFetchText(tt.line, now)
			if len(entries) != 1 {
				t.Fatalf("条目数=%d", len(entries))
			}
			if entries[0].Addr() != tt.wantAddr {
				t.Fatalf("addr=%s", entries[0].Addr())
			}
			tt.check(t, entries[0])
		})
	}
}

func TestParseFetchTextGarbageSkipped(t *testing.T) {
	now := time.Now()
	// 混合行：有效 + 垃圾（无端口/非 ip）
	entries := parseFetchText("1.1.1.1:8080\r\ngarbage\r\n2.2.2.2:9090\n", now)
	if len(entries) != 2 {
		t.Fatalf("有效条目=%d, 期望 2", len(entries))
	}
}

// ---------- httptest 行为级单测 ----------

// newTestServer 记录 query 并按路径回放的测试服务器
func newTestServer(t *testing.T, handler func(path string, q map[string]string) (int, string)) (*httptest.Server, *map[string]map[string]string) {
	t.Helper()
	calls := map[string]map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		calls[r.URL.Path] = q
		code, body := handler(r.URL.Path, q)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testConf() settings.ProxyProviderConf {
	return settings.ProxyProviderConf{
		ID: "p1", Name: "默认", Vendor: Vendor91HTTP,
		TradeNo: "t123", Secret: "s456",
	}
}

func TestFetchRequestParams(t *testing.T) {
	srv, calls := newTestServer(t, func(path string, q map[string]string) (int, string) {
		if path != "/v1/get-ip" {
			t.Fatalf("意外路径: %s", path)
		}
		return 200, `{"code":0,"data":{"list":[{"ip":"1.2.3.4","port":8080}]}}`
	})
	p := new91HTTP(srv.URL)
	conf := testConf()
	conf.Province = "江苏"
	conf.City = "南京"
	conf.ISP = "1"
	conf.FilterUsed = true
	res, err := p.Fetch(context.Background(), conf, 3)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("条目数=%d", len(res.Entries))
	}
	q := (*calls)["/v1/get-ip"]
	for k, want := range map[string]string{
		"trade_no": "t123", "secret": "s456", "num": "3",
		"format": "json", "time": "1", "pw": "1", "protocol": "1",
		"province": "江苏", "city": "南京", "isp": "1", "filter": "1",
	} {
		if q[k] != want {
			t.Fatalf("参数 %s=%q, 期望 %q", k, q[k], want)
		}
	}
}

func TestFetchTextFallback(t *testing.T) {
	// 厂商忽略 format=json 返回 text 时同 body 回退解析
	srv, _ := newTestServer(t, func(path string, q map[string]string) (int, string) {
		return 200, "1.1.1.1:8080\r\n2.2.2.2:9090"
	})
	p := new91HTTP(srv.URL)
	res, err := p.Fetch(context.Background(), testConf(), 2)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("条目数=%d", len(res.Entries))
	}
}

func TestFetchUserpassFallback(t *testing.T) {
	srv, _ := newTestServer(t, func(path string, q map[string]string) (int, string) {
		return 200, `{"code":0,"data":{"list":[{"ip":"1.2.3.4","port":8080}]}}`
	})
	p := new91HTTP(srv.URL)
	conf := testConf()
	conf.Username = "manual_u"
	conf.Password = "manual_p"
	res, err := p.Fetch(context.Background(), conf, 1)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if res.Entries[0].Username != "manual_u" || res.Entries[0].Password != "manual_p" {
		t.Fatalf("手填账密未补全: %+v", res.Entries[0])
	}
	// 白名单模式不补账密
	conf.AuthMode = settings.PoolAuthWhitelist
	res, err = p.Fetch(context.Background(), conf, 1)
	if err != nil {
		t.Fatalf("Fetch 失败: %v", err)
	}
	if res.Entries[0].Username != "" {
		t.Fatalf("白名单模式不应补账密: %+v", res.Entries[0])
	}
}

func TestFetchVendorError(t *testing.T) {
	srv, _ := newTestServer(t, func(path string, q map[string]string) (int, string) {
		return 200, `{"code":1002,"msg":"白名单未配置"}`
	})
	p := new91HTTP(srv.URL)
	_, err := p.Fetch(context.Background(), testConf(), 1)
	if err == nil || !strings.Contains(err.Error(), "白名单未配置") {
		t.Fatalf("厂商错误未透传: %v", err)
	}
}

func TestClientForceDirect(t *testing.T) {
	p := new91HTTP("http://example.invalid")
	tr, ok := p.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型异常")
	}
	if tr.Proxy != nil {
		t.Fatalf("铁律 1 违反：Proxy 必须显式为 nil")
	}
	if p.hc.Timeout != 10*time.Second {
		t.Fatalf("铁律 2 违反：整体超时应 10s, 实际 %v", p.hc.Timeout)
	}
}

func TestAccountInfo(t *testing.T) {
	srv, calls := newTestServer(t, func(path string, q map[string]string) (int, string) {
		if path != "/v1/order/get-info" {
			t.Fatalf("意外路径: %s", path)
		}
		return 200, `{"code":0,"data":{"auth_username":"inst_1","auth_password":"pw8","surplus_ip_quantity":9850,"white_ip":"111.222.3.4"}}`
	})
	p := new91HTTP(srv.URL)
	info, err := p.AccountInfo(context.Background(), testConf())
	if err != nil {
		t.Fatalf("AccountInfo 失败: %v", err)
	}
	if info.AuthUsername != "inst_1" || info.AuthPassword != "pw8" {
		t.Fatalf("账密解析错误: %+v", info)
	}
	if info.SurplusTotal != 9850 {
		t.Fatalf("余量=%d", info.SurplusTotal)
	}
	if info.WhitelistIP != "111.222.3.4" {
		t.Fatalf("white_ip=%q", info.WhitelistIP)
	}
	if (*calls)["/v1/order/get-info"]["trade_no"] != "t123" {
		t.Fatalf("凭据未携带")
	}
}

func TestWhitelistOps(t *testing.T) {
	srv, calls := newTestServer(t, func(path string, q map[string]string) (int, string) {
		switch path {
		case "/v1/white-ip/add", "/v1/white-ip/del":
			return 200, `{"code":0,"msg":"ok"}`
		case "/v1/white-ip/get":
			return 200, `{"code":0,"data":{"max_white_ip":5,"white_list":["1.1.1.1",{"ip":"2.2.2.2"}]}}`
		}
		t.Fatalf("意外路径: %s", path)
		return 500, ""
	})
	p := new91HTTP(srv.URL)
	ctx := context.Background()
	if err := p.AddWhitelist(ctx, testConf(), "1.1.1.1"); err != nil {
		t.Fatalf("AddWhitelist: %v", err)
	}
	if (*calls)["/v1/white-ip/add"]["add_ip"] != "1.1.1.1" {
		t.Fatalf("add_ip 参数错误: %v", (*calls)["/v1/white-ip/add"])
	}
	if err := p.DelWhitelist(ctx, testConf(), "2.2.2.2"); err != nil {
		t.Fatalf("DelWhitelist: %v", err)
	}
	if (*calls)["/v1/white-ip/del"]["del_ip"] != "2.2.2.2" {
		t.Fatalf("del_ip 参数错误")
	}
	wl, err := p.ListWhitelist(ctx, testConf())
	if err != nil {
		t.Fatalf("ListWhitelist: %v", err)
	}
	if len(wl.IPs) != 2 || wl.IPs[0] != "1.1.1.1" || wl.IPs[1] != "2.2.2.2" {
		t.Fatalf("白名单解析错误: %+v", wl)
	}
}

func TestGetMyIP(t *testing.T) {
	// 文本形态
	srv, _ := newTestServer(t, func(path string, q map[string]string) (int, string) {
		return 200, "111.222.3.4"
	})
	p := new91HTTP(srv.URL)
	ip, err := p.GetMyIP(context.Background())
	if err != nil || ip != "111.222.3.4" {
		t.Fatalf("文本形态: ip=%q err=%v", ip, err)
	}
	// JSON 形态
	srv2, _ := newTestServer(t, func(path string, q map[string]string) (int, string) {
		return 200, `{"code":0,"data":{"ip":"5.6.7.8"}}`
	})
	p2 := new91HTTP(srv2.URL)
	ip, err = p2.GetMyIP(context.Background())
	if err != nil || ip != "5.6.7.8" {
		t.Fatalf("JSON 形态: ip=%q err=%v", ip, err)
	}
}

// TestFetchTransportErrorNoSecret 回归审计 M1：传输层错误 *url.Error 自带完整 URL
// （?secret=xxx&trade_no=），经 Fetch 透传到 Manager.lastErr→PoolStatus→SSE/UI/CLI/日志。
// 脱敏后错误串不得含 secret/trade_no 值，但应保留路径，便于排障定位。
func TestFetchTransportErrorNoSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // 立即关闭：请求连接被拒，hc.Do 返回 *url.Error

	p := new91HTTP(base)
	_, err := p.Fetch(context.Background(), testConf(), 1)
	if err == nil {
		t.Fatalf("连接失败应返回错误")
	}
	msg := err.Error()
	for _, leak := range []string{"s456", "t123", "secret=", "trade_no="} {
		if strings.Contains(msg, leak) {
			t.Fatalf("传输层错误泄漏敏感串 %q: %s", leak, msg)
		}
	}
	if !strings.Contains(msg, "/v1/get-ip") {
		t.Fatalf("脱敏后应保留请求路径以便排障: %s", msg)
	}
}

func TestRegistry(t *testing.T) {
	if !VendorRegistered(Vendor91HTTP) {
		t.Fatalf("91http 未注册")
	}
	p, ok := Lookup(Vendor91HTTP)
	if !ok || p.Vendor() != Vendor91HTTP {
		t.Fatalf("Lookup 失败")
	}
	if _, ok := Lookup("no-such-vendor"); ok {
		t.Fatalf("未注册厂商不应命中")
	}
	vendors := Vendors()
	if len(vendors) != 1 || vendors[0].ID != Vendor91HTTP || !vendors[0].SupportUserPass || !vendors[0].SupportWhitelist {
		b, _ := json.Marshal(vendors)
		t.Fatalf("厂商元信息异常: %s", b)
	}
}
