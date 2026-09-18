package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 旧 settings.json 无 proxyPool 字段：加载零值兼容，行为不变（UpstreamMode 恒不为 pool）
func TestLoadGlobalLegacyNoProxyPool(t *testing.T) {
	dir := t.TempDir()
	minJSON := []byte(`{"listenAddr":"127.0.0.1:9999","upstreamMode":"manual","upstreamProxy":"127.0.0.1:7890"}`)
	if err := os.WriteFile(filepath.Join(dir, fileName), minJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.UpstreamMode != UpstreamManual {
		t.Fatalf("旧模式应保持: %q", g.UpstreamMode)
	}
	// DefaultGlobal 兜底：策略默认值就位，账号清单为空
	if g.ProxyPool.OnEmpty != PoolOnEmptyDirect || g.ProxyPool.Rotation.Mode != RotationSticky ||
		g.ProxyPool.Rotation.RefreshAheadSec != 30 || g.ProxyPool.Rotation.ResolveWaitSec != 5 {
		t.Fatalf("proxyPool 默认值不对: %+v", g.ProxyPool)
	}
	if g.ProxyPool.Providers == nil {
		t.Fatal("Providers 应初始化为空切片（不序列化出 null）")
	}
	if err := g.ValidateEnv(); err != nil {
		t.Fatalf("旧配置 manual 模式校验应通过: %v", err)
	}
}

func TestProxyPoolWithDefaults(t *testing.T) {
	var s ProxyPoolSettings // 全零值（旧 JSON 无字段反序列化形态）
	d := s.WithDefaults()
	if d.OnEmpty != PoolOnEmptyDirect {
		t.Fatalf("OnEmpty 兜底: %q", d.OnEmpty)
	}
	r := d.Rotation
	if r.Mode != RotationSticky || r.RefreshAheadSec != 30 || r.MinPoolSize != 1 || r.MaxRetry != 2 || r.ResolveWaitSec != 5 {
		t.Fatalf("Rotation 兜底: %+v", r)
	}
	// 显式值不动
	s2 := ProxyPoolSettings{
		OnEmpty:  PoolOnEmptyError,
		Rotation: RotationConfig{Mode: RotationPerRequest, RefreshAheadSec: 10, MinPoolSize: 3, MaxRetry: 5, ResolveWaitSec: 12},
	}
	d2 := s2.WithDefaults()
	if d2.OnEmpty != PoolOnEmptyError || d2.Rotation.RefreshAheadSec != 10 || d2.Rotation.ResolveWaitSec != 12 {
		t.Fatalf("显式值不应被覆盖: %+v", d2)
	}
	// 账号兜底
	p := ProxyProviderConf{}.WithDefaults()
	if p.FetchNum != 1 || p.AuthMode != PoolAuthAuto {
		t.Fatalf("账号兜底: %+v", p)
	}
}

func validPool() ProxyPoolSettings {
	return ProxyPoolSettings{
		ActiveProvider: "p1",
		Providers: []ProxyProviderConf{{
			ID: "p1", Name: "默认账号", Vendor: "91http",
			TradeNo: "t123", Secret: "s456",
		}},
	}.WithDefaults()
}

func TestProxyPoolValidateOK(t *testing.T) {
	if err := validPool().Validate(); err != nil {
		t.Fatalf("合法配置应通过: %v", err)
	}
	// 手填账密账号（凭据仍须 tradeNo+secret，账密为可选覆盖）
	s := validPool()
	s.Providers[0].AuthMode = PoolAuthUserPass
	s.Providers[0].Username = "u"
	s.Providers[0].Password = "p"
	if err := s.Validate(); err != nil {
		t.Fatalf("账密形态应通过: %v", err)
	}
}

func TestProxyPoolValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ProxyPoolSettings)
		want   string
	}{
		{"onEmpty 非法", func(s *ProxyPoolSettings) { s.OnEmpty = "xxx" }, "onEmpty"},
		{"轮换模式非法", func(s *ProxyPoolSettings) { s.Rotation.Mode = "xxx" }, "轮换模式"},
		{"ResolveWaitSec 超上限", func(s *ProxyPoolSettings) { s.Rotation.ResolveWaitSec = 16 }, "1-15"},
		{"缺生效账号", func(s *ProxyPoolSettings) { s.ActiveProvider = "" }, "生效账号"},
		{"生效账号不存在", func(s *ProxyPoolSettings) { s.ActiveProvider = "nope" }, "不存在"},
		{"缺密钥", func(s *ProxyPoolSettings) { s.Providers[0].Secret = "" }, "业务编号/业务密钥"},
		{"缺业务编号", func(s *ProxyPoolSettings) { s.Providers[0].TradeNo = " " }, "业务编号/业务密钥"},
		{"缺厂商", func(s *ProxyPoolSettings) { s.Providers[0].Vendor = "" }, "缺少厂商"},
		{"提取数量越界", func(s *ProxyPoolSettings) { s.Providers[0].FetchNum = 101 }, "1-100"},
		{"鉴权方式非法", func(s *ProxyPoolSettings) { s.Providers[0].AuthMode = "xxx" }, "鉴权方式"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validPool()
			tc.mutate(&s)
			err := s.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("期望错误含 %q，实际: %v", tc.want, err)
			}
		})
	}
}

// 非生效账号允许凭据未补全（草稿态），只校验形态
func TestProxyPoolValidateDraftProvider(t *testing.T) {
	s := validPool()
	s.Providers = append(s.Providers, ProxyProviderConf{ID: "p2", Name: "草稿", Vendor: "91http"})
	s = s.WithDefaults()
	if err := s.Validate(); err != nil {
		t.Fatalf("草稿账号不应强制凭据: %v", err)
	}
}

// ValidateEnv 的 pool 分支：非法模式与非法池配置都要报错
func TestValidateEnvPool(t *testing.T) {
	g := DefaultGlobal()
	g.UpstreamMode = UpstreamPool
	// 默认无账号 → 缺生效账号报错
	if err := g.ValidateEnv(); err == nil || !strings.Contains(err.Error(), "生效账号") {
		t.Fatalf("空池应报生效账号错误: %v", err)
	}
	g.ProxyPool = validPool()
	if err := g.ValidateEnv(); err != nil {
		t.Fatalf("合法池配置应通过: %v", err)
	}
}

// pool 配置保存/加载往返（secret 原值落盘 0600；读取侧字段完整）
func TestProxyPoolSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGlobal()
	g.UpstreamMode = UpstreamPool
	g.ProxyPool = validPool()
	g.ProxyPool.Providers[0].Username = "au"
	g.ProxyPool.Providers[0].Password = "ap"
	if err := g.SaveGlobal(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadGlobal(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := got.ProxyPool.ActiveConf()
	if p == nil || p.TradeNo != "t123" || p.Secret != "s456" || p.Username != "au" || p.Password != "ap" {
		t.Fatalf("往返后账号字段丢失: %+v", p)
	}
	if got.ProxyPool.Rotation.ResolveWaitSec != 5 {
		t.Fatalf("往返后 Rotation 丢失: %+v", got.ProxyPool.Rotation)
	}
}
