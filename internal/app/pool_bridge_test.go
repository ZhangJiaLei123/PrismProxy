package app

import (
	"context"
	"errors"
	"testing"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/proxypool"
	"prismproxy/internal/settings"
)

// testPoolVendor 捕获 Fetch 入参的假厂商（验证测试提取的凭据哨兵合并）
const testPoolVendor = proxypool.VendorID("testpool")

type captureProvider struct {
	got settings.ProxyProviderConf
}

func (p *captureProvider) Vendor() proxypool.VendorID { return testPoolVendor }
func (p *captureProvider) Fetch(_ context.Context, conf settings.ProxyProviderConf, _ int) (*proxypool.FetchResult, error) {
	p.got = conf
	return &proxypool.FetchResult{Entries: []proxypool.ProxyEntry{{Host: "1.2.3.4", Port: 8080}}}, nil
}
func (p *captureProvider) AccountInfo(context.Context, settings.ProxyProviderConf) (*proxypool.AccountInfo, error) {
	return nil, nil
}
func (p *captureProvider) AddWhitelist(context.Context, settings.ProxyProviderConf, string) error {
	return nil
}
func (p *captureProvider) DelWhitelist(context.Context, settings.ProxyProviderConf, string) error {
	return nil
}
func (p *captureProvider) ListWhitelist(context.Context, settings.ProxyProviderConf) (*proxypool.WhitelistInfo, error) {
	return nil, nil
}

func setupTestPoolApp(t *testing.T) (*App, *captureProvider) {
	t.Helper()
	fake := &captureProvider{}
	proxypool.Register(proxypool.VendorMeta{ID: testPoolVendor, Name: "testpool"}, func() proxypool.Provider { return fake })
	a := newTestApp(t)
	a.gcfg.ProxyPool = settings.ProxyPoolSettings{
		ActiveProvider: "p1",
		Providers: []settings.ProxyProviderConf{{
			ID: "p1", Vendor: string(testPoolVendor), Name: "t1",
			TradeNo: "T1", Secret: "S1", Password: "P1", FetchNum: 1,
		}},
	}
	return a, fake
}

// 池耗尽策略：默认 direct；非法值 ErrPoolBadReq；error 落盘并经视图顶层 onEmpty 回显；未运行不重启
func TestProxyPoolSetOnEmpty(t *testing.T) {
	a, _ := setupTestPoolApp(t)

	// 配置态默认值随 WithDefaults 归一为 direct（即使初始结构体为空）
	sv0, err := a.ProxyPoolStatus()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if sv0.OnEmpty != settings.PoolOnEmptyDirect {
		t.Fatalf("默认 onEmpty 应 direct，得 %q", sv0.OnEmpty)
	}
	// 大小写/空白归一
	res, err := a.ProxyPoolSetOnEmpty("  ERROR ")
	if err != nil {
		t.Fatalf("SetOnEmpty: %v", err)
	}
	if res.Restarted {
		t.Fatal("代理未运行不应重启")
	}
	if res.Status.OnEmpty != settings.PoolOnEmptyError {
		t.Fatalf("视图应回填 error，得 %q", res.Status.OnEmpty)
	}
	if a.gcfg.ProxyPool.OnEmpty != settings.PoolOnEmptyError {
		t.Fatalf("配置应落盘 error，得 %q", a.gcfg.ProxyPool.OnEmpty)
	}
	// 非法值 → ErrPoolBadReq（HTTP 映射 400）
	if _, err := a.ProxyPoolSetOnEmpty("block"); !errors.Is(err, ctlapi.ErrPoolBadReq) {
		t.Fatalf("非法值应 wrap ErrPoolBadReq，得 %v", err)
	}
}

// 回归：测试提取表单哨兵空 secret 必须沿用已存凭据（否则厂商报「secret不得为空」）
func TestProxyPoolTestFetchMergesStoredCreds(t *testing.T) {
	a, fake := setupTestPoolApp(t)
	_, err := a.ProxyPoolTestFetch(settings.ProxyProviderConf{
		ID: "p1", Vendor: string(testPoolVendor), FetchNum: 1,
	})
	if err != nil {
		t.Fatalf("TestFetch: %v", err)
	}
	if fake.got.Secret != "S1" || fake.got.TradeNo != "T1" || fake.got.Password != "P1" {
		t.Fatalf("哨兵合并失败: secret=%q tradeNo=%q password=%q", fake.got.Secret, fake.got.TradeNo, fake.got.Password)
	}
	// 显式重填的凭据不被已存值覆盖
	_, err = a.ProxyPoolTestFetch(settings.ProxyProviderConf{
		ID: "p1", Vendor: string(testPoolVendor), Secret: "S2", FetchNum: 1,
	})
	if err != nil {
		t.Fatalf("TestFetch: %v", err)
	}
	if fake.got.Secret != "S2" {
		t.Fatalf("显式凭据被覆盖: secret=%q", fake.got.Secret)
	}
	// __clear__ 按空处理，不透传厂商
	_, err = a.ProxyPoolTestFetch(settings.ProxyProviderConf{
		ID: "p1", Vendor: string(testPoolVendor), Secret: "__clear__", FetchNum: 1,
	})
	if err != nil {
		t.Fatalf("TestFetch: %v", err)
	}
	if fake.got.Secret == "__clear__" {
		t.Fatal("__clear__ 不应透传厂商")
	}
}
