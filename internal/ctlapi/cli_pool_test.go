package ctlapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- CLI 纯函数（P3-8；参照 cli_compose_test 直接调解析函数） ----------

// resolvePoolSecret：--secret 与 --secret-file 互斥（§8.3 G8）
func TestResolvePoolSecret_MutualExclusive(t *testing.T) {
	if _, err := resolvePoolSecret("abc", "some-file"); err == nil {
		t.Fatal("--secret 与 --secret-file 同给应报互斥错误")
	}
}

// resolvePoolSecret：文件通道读取并 TrimSpace（脚本/AI 推荐通道）
func TestResolvePoolSecret_File(t *testing.T) {
	f := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(f, []byte("  s3cr3t-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePoolSecret("", f)
	if err != nil {
		t.Fatalf("读文件不应报错: %v", err)
	}
	if got != "s3cr3t-key" {
		t.Fatalf("应 TrimSpace 读到密钥，得 %q", got)
	}
	// 文件不存在 → 报错
	if _, err := resolvePoolSecret("", filepath.Join(t.TempDir(), "none.txt")); err == nil {
		t.Fatal("密钥文件不存在应报错")
	}
}

// resolvePoolSecret：直传透传；双空 → 空串（保持哨兵，app 层语义）
func TestResolvePoolSecret_Direct(t *testing.T) {
	if got, err := resolvePoolSecret("plain-key", ""); err != nil || got != "plain-key" {
		t.Fatalf("直传应透传: %q %v", got, err)
	}
	if got, err := resolvePoolSecret("", ""); err != nil || got != "" {
		t.Fatalf("双空应返回空串: %q %v", got, err)
	}
}

// checkPoolShapeFlags：isp/auth 值域预检（app 层仍有完整校验）
func TestCheckPoolShapeFlags(t *testing.T) {
	for _, isp := range []string{"", "0", "1", "2", "3"} {
		if err := checkPoolShapeFlags(isp, ""); err != nil {
			t.Fatalf("isp=%q 应合法: %v", isp, err)
		}
	}
	for _, auth := range []string{"", "auto", "userpass", "whitelist"} {
		if err := checkPoolShapeFlags("", auth); err != nil {
			t.Fatalf("auth=%q 应合法: %v", auth, err)
		}
	}
	if err := checkPoolShapeFlags("4", ""); err == nil {
		t.Fatal("isp=4 应报错")
	}
	if err := checkPoolShapeFlags("abc", ""); err == nil {
		t.Fatal("isp=abc 应报错")
	}
	if err := checkPoolShapeFlags("", "foo"); err == nil {
		t.Fatal("auth=foo 应报错")
	}
}

// resolvePoolProvider：id 精确 > 名称唯一 > 重名报错列候选 > 不存在（与 app 层同语义）
func TestResolvePoolProvider(t *testing.T) {
	list := []poolProviderView{
		{ID: "id-a", Name: "同名"},
		{ID: "id-b", Name: "同名"},
		{ID: "id-c", Name: "唯一"},
	}
	// id 精确命中（即使名称重名也优先 id）
	if p, err := resolvePoolProvider(list, "id-a"); err != nil || p.ID != "id-a" {
		t.Fatalf("id 精确寻址失败: %v %#v", err, p)
	}
	// 名称唯一命中
	if p, err := resolvePoolProvider(list, "唯一"); err != nil || p.ID != "id-c" {
		t.Fatalf("名称唯一寻址失败: %v %#v", err, p)
	}
	// 名称重名 → 报错并列候选 id
	_, err := resolvePoolProvider(list, "同名")
	if err == nil || !strings.Contains(err.Error(), "id-a") || !strings.Contains(err.Error(), "id-b") ||
		!strings.Contains(err.Error(), "请改用 id") {
		t.Fatalf("重名应报错列候选 id: %v", err)
	}
	// 不存在
	if _, err := resolvePoolProvider(list, "不存在"); err == nil {
		t.Fatal("不存在应报错")
	}
	// key 首尾空格 TrimSpace
	if p, err := resolvePoolProvider(list, "  id-c  "); err != nil || p.ID != "id-c" {
		t.Fatalf("key 应 TrimSpace: %v %#v", err, p)
	}
}

// whitelist add：无 ip 且无 --yes 须在发请求前被门槛拦下（§8.3：非交互不弹确认）
func TestPoolWhitelistAddAutoRequiresYes(t *testing.T) {
	// 无效 base：若误发请求会连接失败；门槛命中则在网络之前返回文案错误
	c := &client{base: "http://127.0.0.1:1/api/v1", http: &http.Client{Timeout: time.Second}}
	_, err := cliPoolWhitelist(c, []string{"add"})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("无 ip 且无 --yes 应被门槛拦下: %v", err)
	}
}

// whitelist add：多于一个位置参数 → 用法错误
func TestPoolWhitelistAddTooManyArgs(t *testing.T) {
	c := &client{base: "http://127.0.0.1:1/api/v1", http: &http.Client{Timeout: time.Second}}
	if _, err := cliPoolWhitelist(c, []string{"add", "1.2.3.4", "5.6.7.8"}); err == nil ||
		!strings.Contains(err.Error(), "用法") {
		t.Fatalf("两个位置参数应报用法错误: %v", err)
	}
}

// ---------- RunCLI 端到端（fakeService + 真实 HTTP 服务，参照 TestRunCLIEndToEnd） ----------

func TestRunCLIPoolEndToEnd(t *testing.T) {
	svc := &fakeService{}
	srv, _, _ := startTestServer(t, svc)
	dir := filepath.Dir(srv.endpoint)

	run := func(args ...string) int { return RunCLI(dir, args) }

	// proxy pool 无参 = status
	if code := run("proxy", "pool"); code != 0 {
		t.Fatalf("proxy pool 无参=status 应 0，得 %d", code)
	}
	if code := run("proxy", "pool", "status"); code != 0 {
		t.Fatalf("pool status 应 0，得 %d", code)
	}
	if code := run("proxy", "pool", "vendors"); code != 0 {
		t.Fatalf("pool vendors 应 0，得 %d", code)
	}
	if code := run("proxy", "pool", "providers"); code != 0 {
		t.Fatalf("pool providers 应 0，得 %d", code)
	}
	// use：id 直达桩
	if code := run("proxy", "pool", "use", "pid-1"); code != 0 {
		t.Fatalf("pool use 应 0，得 %d", code)
	}
	if svc.poolID != "pid-1" {
		t.Fatalf("use id 应为 pid-1，得 %q", svc.poolID)
	}
	// mode：带参切换（四值校验）→ 桩；无参=查状态
	if code := run("proxy", "pool", "mode", "pool"); code != 0 {
		t.Fatalf("pool mode pool 应 0，得 %d", code)
	}
	if svc.poolMode != "pool" {
		t.Fatalf("mode 应为 pool，得 %q", svc.poolMode)
	}
	if code := run("proxy", "pool", "mode"); code != 0 {
		t.Fatalf("pool mode 无参查询应 0，得 %d", code)
	}
	if code := run("proxy", "pool", "mode", "fixed"); code == 0 {
		t.Fatal("非法模式 fixed 应非零退出（合法值 direct|manual|system|pool）")
	}
	// test：无参 body {}
	if code := run("proxy", "pool", "test"); code != 0 {
		t.Fatalf("pool test 应 0，得 %d", code)
	}
	if strings.TrimSpace(string(svc.poolTestRaw)) != "{}" {
		t.Fatalf("test 无参 body 应为 {{}}，得 %q", svc.poolTestRaw)
	}
	// test 带 id
	if code := run("proxy", "pool", "test", "pid-2"); code != 0 {
		t.Fatalf("pool test id 应 0，得 %d", code)
	}
	if !strings.Contains(string(svc.poolTestRaw), "pid-2") {
		t.Fatalf("test body 应含 id，得 %q", svc.poolTestRaw)
	}
	// rotate
	if code := run("proxy", "pool", "rotate"); code != 0 {
		t.Fatalf("pool rotate 应 0，得 %d", code)
	}
	// delete：id 进路径
	if code := run("proxy", "pool", "delete", "pid-3"); code != 0 {
		t.Fatalf("pool delete 应 0，得 %d", code)
	}
	if svc.poolID != "pid-3" {
		t.Fatalf("delete id 应为 pid-3，得 %q", svc.poolID)
	}
	// whitelist list
	if code := run("proxy", "pool", "whitelist", "list"); code != 0 {
		t.Fatalf("whitelist list 应 0，得 %d", code)
	}
	// whitelist add 指定 ip：无需 --yes
	svc.poolWlIP, svc.poolWlAuto, svc.poolWlYes = "", false, false
	if code := run("proxy", "pool", "whitelist", "add", "1.2.3.4"); code != 0 {
		t.Fatalf("whitelist add ip 应 0，得 %d", code)
	}
	if svc.poolWlIP != "1.2.3.4" || svc.poolWlAuto {
		t.Fatalf("指定 ip 加白应 {ip} 不带 auto: ip=%q auto=%v", svc.poolWlIP, svc.poolWlAuto)
	}
	// whitelist add 无 ip 无 --yes：门槛拦截（非零退出）
	if code := run("proxy", "pool", "whitelist", "add"); code == 0 {
		t.Fatal("无 ip 无 --yes 应非零退出")
	}
	// whitelist add --yes：自动取公网 IP
	svc.poolWlIP, svc.poolWlAuto, svc.poolWlYes = "", false, false
	if code := run("proxy", "pool", "whitelist", "add", "--yes"); code != 0 {
		t.Fatalf("whitelist add --yes 应 0，得 %d", code)
	}
	if !svc.poolWlAuto || !svc.poolWlYes {
		t.Fatalf("--yes 自动加白应 {auto,yes}: auto=%v yes=%v", svc.poolWlAuto, svc.poolWlYes)
	}
	// whitelist delete
	if code := run("proxy", "pool", "whitelist", "delete", "9.9.9.9"); code != 0 {
		t.Fatalf("whitelist delete 应 0，得 %d", code)
	}
	if svc.poolWlIP != "9.9.9.9" {
		t.Fatalf("delete ip 应为 9.9.9.9，得 %q", svc.poolWlIP)
	}
	// 未知子命令
	if code := run("proxy", "pool", "bogus"); code == 0 {
		t.Fatal("未知子命令应非零退出")
	}
}
