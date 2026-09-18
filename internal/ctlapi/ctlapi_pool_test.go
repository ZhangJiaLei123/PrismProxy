package ctlapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ---------- 代理池 HTTP 路由（P3-8；fakeService 桩 + doRequest，统一 poolErrCode 映射） ----------

// GET /proxy/pool/status 200 返回池状态视图
func TestPoolStatusEndpoint(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)
	code, m := doRequest(t, "GET", addr, token, "/proxy/pool/status", nil)
	if code != 200 {
		t.Fatalf("pool status 应 200，得 %d (%v)", code, m)
	}
	if m["mode"] != "direct" {
		t.Fatalf("mode 应为 direct，得 %v", m["mode"])
	}
}

// GET /proxy/pool/vendors 200 含 91http 元信息（响应为顶层数组，doRequest 按 map 解析不适用，自行解码）
func TestPoolVendorsEndpoint(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/proxy/pool/vendors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("vendors 应 200，得 %d", resp.StatusCode)
	}
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil || len(arr) == 0 {
		t.Fatalf("vendors 应为非空数组: %v %v", arr, err)
	}
	if arr[0]["id"] != "91http" {
		t.Fatalf("首个厂商应为 91http: %v", arr[0])
	}
}

// providers：GET 清单 / POST 新增 / PUT 整体替换（路径 id 注入）/ DELETE / parts 防御 404
func TestPoolProvidersEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	if code, m := doRequest(t, "GET", addr, token, "/proxy/pool/providers", nil); code != 200 {
		t.Fatalf("GET providers 应 200，得 %d (%v)", code, m)
	}
	// POST 新增：body 原样到 svc
	code, _ := doRequest(t, "POST", addr, token, "/proxy/pool/providers",
		strings.NewReader(`{"name":"A","vendor":"91http"}`))
	if code != 200 {
		t.Fatalf("POST providers 应 200，得 %d", code)
	}
	if !strings.Contains(string(svc.poolSaveRaw), `"name":"A"`) {
		t.Fatalf("POST body 应透传到 svc: %q", svc.poolSaveRaw)
	}
	// PUT /providers/{id}：路径 id 注入请求体（body 可不带 id）
	code, _ = doRequest(t, "PUT", addr, token, "/proxy/pool/providers/pid-9",
		strings.NewReader(`{"name":"B"}`))
	if code != 200 {
		t.Fatalf("PUT providers/{id} 应 200，得 %d", code)
	}
	if !strings.Contains(string(svc.poolSaveRaw), `"id":"pid-9"`) ||
		!strings.Contains(string(svc.poolSaveRaw), `"name":"B"`) {
		t.Fatalf("PUT 应注入路径 id 并保留 body 字段: %q", svc.poolSaveRaw)
	}
	// DELETE /providers/{id}
	code, _ = doRequest(t, "DELETE", addr, token, "/proxy/pool/providers/pid-9", nil)
	if code != 200 {
		t.Fatalf("DELETE providers/{id} 应 200，得 %d", code)
	}
	if svc.poolID != "pid-9" {
		t.Fatalf("delete id 应为 pid-9，得 %q", svc.poolID)
	}
	// parts 防御：/providers/a/b → 404
	if code, _ := doRequest(t, "DELETE", addr, token, "/proxy/pool/providers/a/b", nil); code != 404 {
		t.Fatalf("多余路径段应 404，得 %d", code)
	}
}

// use/mode/test/rotate：200 + 桩字段记录
func TestPoolActionEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	code, _ := doRequest(t, "POST", addr, token, "/proxy/pool/use", strings.NewReader(`{"id":"pid-1"}`))
	if code != 200 || svc.poolID != "pid-1" {
		t.Fatalf("use 应 200 且记录 id: %d %q", code, svc.poolID)
	}
	code, _ = doRequest(t, "POST", addr, token, "/proxy/pool/mode", strings.NewReader(`{"mode":"pool"}`))
	if code != 200 || svc.poolMode != "pool" {
		t.Fatalf("mode 应 200 且记录 mode: %d %q", code, svc.poolMode)
	}
	code, _ = doRequest(t, "POST", addr, token, "/proxy/pool/onempty", strings.NewReader(`{"onEmpty":"error"}`))
	if code != 200 || svc.poolOnEmpty != "error" {
		t.Fatalf("onempty 应 200 且记录 error: %d %q", code, svc.poolOnEmpty)
	}
	// policy 别名同样透传
	code, _ = doRequest(t, "POST", addr, token, "/proxy/pool/onempty", strings.NewReader(`{"policy":"direct"}`))
	if code != 200 || svc.poolOnEmpty != "direct" {
		t.Fatalf("policy 别名应透传: %d %q", code, svc.poolOnEmpty)
	}
	code, _ = doRequest(t, "POST", addr, token, "/proxy/pool/test", strings.NewReader(`{}`))
	if code != 200 || strings.TrimSpace(string(svc.poolTestRaw)) != "{}" {
		t.Fatalf("test 应 200 且记录 raw: %d %q", code, svc.poolTestRaw)
	}
	if code, m := doRequest(t, "POST", addr, token, "/proxy/pool/rotate", nil); code != 200 {
		t.Fatalf("rotate 应 200，得 %d (%v)", code, m)
	}
	// 坏 JSON → 400（use 走 Decode 失败）
	if code, _ := doRequest(t, "POST", addr, token, "/proxy/pool/use", strings.NewReader(`{bad`)); code != 400 {
		t.Fatalf("坏 JSON 应 400，得 %d", code)
	}
}

// whitelist：GET / POST {ip} / POST {auto,yes} / DELETE ?ip= / DELETE 缺 ip 400
func TestPoolWhitelistEndpoints(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)

	if code, m := doRequest(t, "GET", addr, token, "/proxy/pool/whitelist", nil); code != 200 {
		t.Fatalf("GET whitelist 应 200，得 %d (%v)", code, m)
	}
	// POST {ip}
	code, _ := doRequest(t, "POST", addr, token, "/proxy/pool/whitelist", strings.NewReader(`{"ip":"1.2.3.4"}`))
	if code != 200 || svc.poolWlIP != "1.2.3.4" || svc.poolWlAuto {
		t.Fatalf("POST ip 加白异常: %d ip=%q auto=%v", code, svc.poolWlIP, svc.poolWlAuto)
	}
	// POST {auto,yes}
	svc.poolWlIP, svc.poolWlAuto, svc.poolWlYes = "", false, false
	code, _ = doRequest(t, "POST", addr, token, "/proxy/pool/whitelist", strings.NewReader(`{"auto":true,"yes":true}`))
	if code != 200 || !svc.poolWlAuto || !svc.poolWlYes {
		t.Fatalf("POST auto+yes 加白异常: %d auto=%v yes=%v", code, svc.poolWlAuto, svc.poolWlYes)
	}
	// DELETE ?ip=
	code, _ = doRequest(t, "DELETE", addr, token, "/proxy/pool/whitelist?ip=9.9.9.9", nil)
	if code != 200 || svc.poolWlIP != "9.9.9.9" {
		t.Fatalf("DELETE ip 异常: %d ip=%q", code, svc.poolWlIP)
	}
	// DELETE 缺 ip → 400
	if code, _ := doRequest(t, "DELETE", addr, token, "/proxy/pool/whitelist", nil); code != 400 {
		t.Fatalf("DELETE 缺 ip 应 400，得 %d", code)
	}
}

// 错误码映射（§8.4）：四个 sentinel → 400/404/409/502；%w 包装穿透；未知错误 500
func TestPoolErrorCodeMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ErrPoolBadReq, 400},
		{fmt.Errorf("%w: 厂商 foo 未注册", ErrPoolBadReq), 400}, // %w 包装穿透
		{ErrPoolNotFound, 404},
		{ErrPoolConflict, 409},
		{ErrPoolUpstream, 502},
		{fmt.Errorf("杂散错误"), 500},
	}
	for _, tc := range cases {
		svc := &fakeService{poolErr: tc.err}
		_, addr, token := startTestServer(t, svc)
		if code, m := doRequest(t, "GET", addr, token, "/proxy/pool/status", nil); code != tc.want {
			t.Fatalf("%v → 应 %d，得 %d (%v)", tc.err, tc.want, code, m)
		}
		// rotate 同映射（POST 通道）
		if code, _ := doRequest(t, "POST", addr, token, "/proxy/pool/rotate", nil); code != tc.want {
			t.Fatalf("rotate %v → 应 %d，得 %d", tc.err, tc.want, code)
		}
	}
}

// 方法不允许 → 405（抽查：POST /status、GET /rotate、PATCH /providers）
func TestPoolMethodNotAllowed(t *testing.T) {
	svc := &fakeService{}
	_, addr, token := startTestServer(t, svc)
	if code, _ := doRequest(t, "POST", addr, token, "/proxy/pool/status", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /status 应 405，得 %d", code)
	}
	if code, _ := doRequest(t, "GET", addr, token, "/proxy/pool/rotate", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /rotate 应 405，得 %d", code)
	}
	if code, _ := doRequest(t, "GET", addr, token, "/proxy/pool/onempty", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /onempty 应 405，得 %d", code)
	}
	if code, _ := doRequest(t, "PATCH", addr, token, "/proxy/pool/providers", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("PATCH /providers 应 405，得 %d", code)
	}
}
