package proxypool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"prismproxy/internal/settings"
)

// ---------- 测试辅助 ----------

// poolConf 合法测试配置（ResolveWaitSec=2 缩短等待窗口）
func poolConf(onEmpty string) settings.ProxyPoolSettings {
	return settings.ProxyPoolSettings{
		ActiveProvider: "p1",
		Providers: []settings.ProxyProviderConf{{
			ID: "p1", Name: "默认账号", Vendor: Vendor91HTTP,
			TradeNo: "t", Secret: "s", FetchNum: 2,
		}},
		Rotation: settings.RotationConfig{
			Mode:            settings.RotationSticky,
			RefreshAheadSec: 30,
			MinPoolSize:     1,
			MaxRetry:        2,
			ResolveWaitSec:  2,
		},
		OnEmpty: onEmpty,
	}
}

// vendorServer 可编程厂商 API：handler 按命中序号返回 (httpCode, body)
type vendorServer struct {
	srv   *httptest.Server
	hits  atomic.Int32
	delay time.Duration
}

func newVendorServer(t *testing.T, handler func(hit int) (int, string)) *vendorServer {
	v := &vendorServer{}
	v.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := int(v.hits.Add(1))
		if v.delay > 0 {
			time.Sleep(v.delay)
		}
		code, body := handler(hit)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(v.srv.Close)
	return v
}

// entryJSON 构造未来 1 小时过期的条目
func entryJSON(host string, port int) string {
	return fmt.Sprintf(`{"ip":%q,"port":%d,"expire_time":%d,"username":"u","password":"p","region":"江苏 南京"}`,
		host, port, time.Now().Add(time.Hour).Unix())
}

// newTestManager NewManager 后注入测试 server 基址的 91http adapter
func newTestManager(t *testing.T, conf settings.ProxyPoolSettings, v *vendorServer) *Manager {
	t.Helper()
	m, err := NewManager(conf)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.provider = new91HTTP(v.srv.URL)
	t.Cleanup(m.Stop)
	return m
}

// ---------- NewManager 构造路径 ----------

func TestNewManagerErrors(t *testing.T) {
	// 未选生效账号
	conf := poolConf(settings.PoolOnEmptyDirect)
	conf.ActiveProvider = ""
	if _, err := NewManager(conf); err == nil {
		t.Fatalf("空生效账号应报错")
	}
	// 厂商未注册
	conf = poolConf(settings.PoolOnEmptyDirect)
	conf.Providers[0].Vendor = "no-such"
	if _, err := NewManager(conf); err == nil || !strings.Contains(err.Error(), "未注册") {
		t.Fatalf("未注册厂商应报错: %v", err)
	}
}

// ---------- 首提与解析 ----------

func TestManagerFetchAndResolve(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"surplus_ip_quantity":9850,"list":[` +
			entryJSON("1.1.1.1", 8001) + `,` + entryJSON("2.2.2.2", 8002) + `]}}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	ep, err := m.Resolver()(context.Background())
	if err != nil || ep == nil {
		t.Fatalf("Resolver: ep=%v err=%v", ep, err)
	}
	if ep.Addr != "1.1.1.1:8001" || ep.Username != "u" || ep.Password != "p" {
		t.Fatalf("endpoint 错误: %+v", ep)
	}
	st := m.Status()
	if !st.Running || st.Fetching || st.Degraded {
		t.Fatalf("状态异常: %+v", st)
	}
	if st.Current == nil || st.Current.Addr != "1.1.1.1:8001" || st.Current.Region != "江苏 南京" {
		t.Fatalf("current 异常: %+v", st.Current)
	}
	if st.PoolSize != 2 || st.SurplusTotal != 9850 || st.LastFetchAt == 0 || st.ExpireAt == 0 {
		t.Fatalf("状态字段异常: %+v", st)
	}
}

// TestManagerResolverWaitsInflight 窗口期内 Resolver 等待单飞完成而非瞬时 OnEmpty（设计 §6.2 B3）
func TestManagerResolverWaitsInflight(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("3.3.3.3", 8003) + `]}}`
	})
	v.delay = 300 * time.Millisecond
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	m.Start() // 首提 in-flight
	start := time.Now()
	ep, err := m.Resolver()(context.Background())
	if err != nil || ep == nil || ep.Addr != "3.3.3.3:8003" {
		t.Fatalf("应等待窗口期拿到 endpoint: ep=%v err=%v", ep, err)
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Fatalf("未等待单飞完成（%v），违反 Resolving 语义", d)
	}
}

// ---------- OnEmpty ----------

func TestManagerOnEmptyError(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":1001,"msg":"余量不足"}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	m.Start()
	_, err := m.Resolver()(context.Background())
	if err == nil || !strings.Contains(err.Error(), "余量不足") {
		t.Fatalf("OnEmpty=error 应带厂商错误: %v", err)
	}
	// 冷却期再取：错误带冷却提示且不再发起请求（单飞+退避）
	hitsBefore := v.hits.Load()
	_, err = m.Resolver()(context.Background())
	if err == nil || !strings.Contains(err.Error(), "冷却") {
		t.Fatalf("冷却期错误应带提示: %v", err)
	}
	if v.hits.Load() != hitsBefore {
		t.Fatalf("冷却期不应发起新提取: %d→%d", hitsBefore, v.hits.Load())
	}
	st := m.Status()
	if st.LastError == "" || st.Fetching {
		t.Fatalf("状态异常: %+v", st)
	}
}

func TestManagerOnEmptyDirectDegraded(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":1001,"msg":"提取频繁"}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyDirect), v)
	m.Start()
	ep, err := m.Resolver()(context.Background())
	if ep != nil || err != nil {
		t.Fatalf("OnEmpty=direct 应返回 (nil,nil): ep=%v err=%v", ep, err)
	}
	if !m.Status().Degraded {
		t.Fatalf("降级直连应标 Degraded")
	}
}

// ---------- 轮换与坏点 ----------

func TestManagerRotate(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		if hit == 1 {
			return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `,` + entryJSON("2.2.2.2", 8002) + `]}}`
		}
		return 200, `{"code":0,"data":{"list":[` + entryJSON("9.9.9.9", 8009) + `]}}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := m.Status().Current.Addr; got != "1.1.1.1:8001" {
		t.Fatalf("初始 current=%s", got)
	}
	// 备用池有货：立即切换不同 addr，不发新请求
	if err := m.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if got := m.Status().Current.Addr; got != "2.2.2.2:8002" {
		t.Fatalf("轮换后 current=%s", got)
	}
	if v.hits.Load() != 1 {
		t.Fatalf("备用池切换不应发请求: hits=%d", v.hits.Load())
	}
	// 池空：Rotate 触发补池等待
	if err := m.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate 补池: %v", err)
	}
	if got := m.Status().Current.Addr; got != "9.9.9.9:8009" {
		t.Fatalf("补池后 current=%s", got)
	}
}

func TestManagerReportBad(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `,` + entryJSON("2.2.2.2", 8002) + `]}}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// MaxRetry=2：第 1 次不剔除
	m.ReportBad("1.1.1.1:8001")
	if got := m.Status().Current.Addr; got != "1.1.1.1:8001" {
		t.Fatalf("未达阈值不应剔除: %s", got)
	}
	// 迟报旧 addr 不计（场景预演：轮换后迟报）
	m.ReportBad("9.9.9.9:9999")
	// 第 2 次达阈值：剔除并切下一个
	m.ReportBad("1.1.1.1:8001")
	if got := m.Status().Current.Addr; got != "2.2.2.2:8002" {
		t.Fatalf("达阈值应切换: %s", got)
	}
	// 迟报已轮换 addr：不重复计数
	m.ReportBad("1.1.1.1:8001")
	if got := m.Status().Current.Addr; got != "2.2.2.2:8002" {
		t.Fatalf("迟报不应影响: %s", got)
	}
}

// TestManagerSingleflight 并发建连只发一次厂商请求（设计 §6.1 单飞）
func TestManagerSingleflight(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `]}}`
	})
	v.delay = 200 * time.Millisecond
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ep, err := m.Resolver()(context.Background())
			if err != nil || ep == nil {
				t.Errorf("resolve: ep=%v err=%v", ep, err)
			}
		}()
	}
	wg.Wait()
	if hits := v.hits.Load(); hits != 1 {
		t.Fatalf("单飞违反：hits=%d, 期望 1", hits)
	}
}

// TestManagerExpireRenewal 到期前 RefreshAheadSec 自动续期切换（设计 §6.3 sticky）
func TestManagerExpireRenewal(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		if hit == 1 {
			// 首条 2 秒后过期
			return 200, fmt.Sprintf(`{"code":0,"data":{"list":[{"ip":"1.1.1.1","port":8001,"expire_time":%d}]}}`,
				time.Now().Add(2*time.Second).Unix())
		}
		return 200, `{"code":0,"data":{"list":[` + entryJSON("2.2.2.2", 8002) + `]}}`
	})
	conf := poolConf(settings.PoolOnEmptyError)
	conf.Rotation.RefreshAheadSec = 1
	m := newTestManager(t, conf, v)
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := m.Status().Current.Addr; got != "1.1.1.1:8001" {
		t.Fatalf("初始 current=%s", got)
	}
	// 等 run 循环在 expire-refreshAhead=1s 处触发续期
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if got := m.Status().Current.Addr; got == "2.2.2.2:8002" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("续期未切换: current=%s hits=%d", m.Status().Current.Addr, v.hits.Load())
}

// TestManagerStop 停止后 Running=false，状态保留可查
func TestManagerStop(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `]}}`
	})
	conf := poolConf(settings.PoolOnEmptyError)
	m, err := NewManager(conf)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.provider = new91HTTP(v.srv.URL)
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	m.Stop()
	st := m.Status()
	if st.Running {
		t.Fatalf("Stop 后 Running 应 false")
	}
	if st.Current == nil || st.Current.Addr != "1.1.1.1:8001" {
		t.Fatalf("Stop 应保留最近状态: %+v", st.Current)
	}
}

// TestManagerOnChange 状态变化回调（App 推 Wails 事件用）
func TestManagerOnChange(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `]}}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	var last atomic.Value
	m.SetOnStatusChange(func(st PoolStatus) { last.Store(st) })
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	st, ok := last.Load().(PoolStatus)
	if !ok || st.Current == nil || st.Current.Addr != "1.1.1.1:8001" {
		t.Fatalf("回调未收到最终状态: %+v", st)
	}
}

// TestManagerCallbackNoCallerLockReentry 回归：回调在独立 dispatcher 协程执行，
// 即使调用方持有回调也要获取的外部锁，Start 与 Stop（重建旧 Manager 路径）都不得自死锁。
// 关键场景=ensureProxyManagerLocked 持 App.mu 调旧 Manager.Stop()，而 onChange 要取同一把锁。
func TestManagerCallbackNoCallerLockReentry(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `]}}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	var appMu sync.Mutex // 模拟 App.mu：回调需要获取、Start/Stop 调用方也持有着
	m.SetOnStatusChange(func(st PoolStatus) {
		appMu.Lock()
		defer appMu.Unlock()
	})
	// 持外部锁 Start：startProxySnap 持 App.mu 装配 Manager（B1 异步首提，必须立即返回）
	startDone := make(chan struct{})
	go func() {
		appMu.Lock()
		m.Start()
		appMu.Unlock()
		close(startDone)
	}()
	select {
	case <-startDone:
	case <-time.After(5 * time.Second):
		t.Fatal("持外部锁 Start 死锁（回调在调用方锁栈同步执行）")
	}
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// 持外部锁 Stop：还原 ensureProxyManagerLocked 重建 Manager 时停旧 Manager 的死锁链
	stopDone := make(chan struct{})
	go func() {
		appMu.Lock()
		m.Stop()
		appMu.Unlock()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("持外部锁 Stop 死锁（Stop 等待了需要 App.mu 的回调排空）")
	}
}

// TestManagerResolvePromotesBackup 回归审计 M2：current 空缺（被 ReportBad 剔除/刚轮换）
// 但备用池有未过期 entry 时，resolve 必须先 promote 手中 IP 立即顶上，
// 不得越过备用池直接补池甚至误判 degraded 直连（"手里有 IP 不裸奔"）。
func TestManagerResolvePromotesBackup(t *testing.T) {
	conf := poolConf(settings.PoolOnEmptyDirect)
	m, err := NewManager(conf)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(m.Stop)
	// 预置备用池一条未过期条目（不经 Start/厂商提取）
	m.mu.Lock()
	m.pool = []ProxyEntry{{
		Host: "5.5.5.5", Port: 8005, Username: "u", Password: "p",
		Region: "内蒙古 赤峰", ExpireAt: time.Now().Add(time.Hour), Source: Vendor91HTTP,
	}}
	// 退避到未来：若代码错误地越过备用池调 kickFetch，也不会产生厂商请求，
	// 最终会落到 degraded；正确路径在 promote 成功后直接返回，degraded 保持 false。
	m.backoffUntil = time.Now().Add(time.Hour)
	m.mu.Unlock()

	ep, err := m.Resolver()(context.Background())
	if err != nil || ep == nil {
		t.Fatalf("备用池有货应直接 promote: ep=%v err=%v", ep, err)
	}
	if ep.Addr != "5.5.5.5:8005" {
		t.Fatalf("应使用备用池条目: %s", ep.Addr)
	}
	st := m.Status()
	if st.Degraded {
		t.Fatalf("有货 promote 不得标 degraded（误降级直连=裸奔）")
	}
	if st.PoolSize != 1 {
		t.Fatalf("promote 后 PoolSize 应为 1（current），得 %d", st.PoolSize)
	}
	if st.Fetching {
		t.Fatalf("备用池命中不应进入 fetching")
	}
}

// TestManagerBackoff 退避序列 2/5/10→翻倍封顶 60s（设计 §6.3）
func TestManagerBackoff(t *testing.T) {
	m := &Manager{}
	want := []time.Duration{2, 5, 10, 20, 40, 60, 60, 60, 60, 60}
	for i, w := range want {
		m.failCount = i + 1
		if got := m.backoffLocked(); got != w*time.Second {
			t.Fatalf("failCount=%d 退避=%v, 期望 %v", m.failCount, got, w*time.Second)
		}
	}
}

// TestPoolStatusNoSecret 状态 DTO 不含密码/Raw（v3 G3 红线）
func TestPoolStatusNoSecret(t *testing.T) {
	v := newVendorServer(t, func(hit int) (int, string) {
		return 200, `{"code":0,"data":{"list":[` + entryJSON("1.1.1.1", 8001) + `]}}`
	})
	m := newTestManager(t, poolConf(settings.PoolOnEmptyError), v)
	m.Start()
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	st := m.Status()
	// 条目含账密（entryJSON u/p），DTO JSON 化后不得出现
	b := fmt.Sprintf("%+v", st)
	if strings.Contains(b, "\"p\"") || strings.Contains(b, "password") || strings.Contains(b, "Raw") {
		t.Fatalf("DTO 泄漏敏感字段: %s", b)
	}
}
