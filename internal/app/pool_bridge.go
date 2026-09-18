package app

// 代理池 Wails 绑定 + CRUD 独立落盘（代理池设计 v3 §8.2；P3-4/P3-5）。
//
// 范式对齐 ai_bridge.go SaveAIConfigPatch：
//   - projMu 读浅拷贝快照 → 锁外合并候选 → 校验 → projMu.Lock 覆盖 → SaveGlobal → Unlock
//     （单用户桌面场景接受锁外合并窗口，与 AI 半边同范式）
//   - secret/password 哨兵：空串=保持原值、"__clear__"=清空、其他=采用
//   - 掩码投影 ProviderView：hasSecret/hasPassword 布尔，原文永不回显
//   - 错误以 ctlapi.ErrPoolXxx sentinel 包装（fmt.Errorf %w），文案前缀随错误透传 CLI/前端
//
// 热应用：写后统一走 persistPoolLocked（落盘 → 代理运行中且涉池时热重启 → 推状态）；
// 热重启整程持 projMu，启动走免 projMu 的 startProxySnap（M10 踩坑：重取 projMu 自死锁）。
// B1 铁律：持锁路径不碰厂商网络 I/O——Fetch/白名单/GetMyIP 均为锁外纯调用（conf 值拷贝）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/proxypool"
	"prismproxy/internal/settings"
)

// ---------- DTO（红线：不含 secret/password/Raw 明文） ----------

// ProviderView 账号掩码投影（§8.2 providers 出参；secret/password 永不回原值）
type ProviderView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Vendor      string `json:"vendor"`
	TradeNo     string `json:"tradeNo"`
	Protocol    string `json:"protocol,omitempty"`
	FetchNum    int    `json:"fetchNum"`
	Province    string `json:"province,omitempty"`
	City        string `json:"city,omitempty"`
	ISP         string `json:"isp,omitempty"`
	FilterUsed  bool   `json:"filterUsed,omitempty"`
	AuthMode    string `json:"authMode"`
	Username    string `json:"username,omitempty"`
	HasSecret   bool   `json:"hasSecret"`
	HasPassword bool   `json:"hasPassword"`
	Active      bool   `json:"active"` // 是否当前生效账号
}

// ProxyTestResult 测试提取结果（§8.2 test 出参；不含 Raw/Password 明文）
type ProxyTestResult struct {
	Addr     string `json:"addr"`               // 首条 host:port
	Region   string `json:"region,omitempty"`   // 首条归属地
	ExpireAt int64  `json:"expireAt,omitempty"` // 首条到期 unix 秒（0=未知）
	WithAuth bool   `json:"withAuth"`           // 首条是否带账密
	Count    int    `json:"count"`              // 本次提取条数
}

// PoolStatusView 池状态查询出参（代理未运行/非池模式也能展示配置态，CLI status 用）
type PoolStatusView struct {
	Mode           string                `json:"mode"`           // 当前上游模式
	ActiveProvider string                `json:"activeProvider"` // 生效账号 id（空=未选择）
	OnEmpty        string                `json:"onEmpty"`        // 池耗尽策略配置态（direct|error；未运行时无 Status 也可回显）
	Running        bool                  `json:"running"`        // 池 Manager 运行中
	Status         *proxypool.PoolStatus `json:"status,omitempty"`
}

// PoolWriteResult 删除账号等写操作统一出参（restarted/warnings 热应用反馈，对齐 SaveSettingsResult）
type PoolWriteResult struct {
	Restarted bool     `json:"restarted"`
	Warnings  []string `json:"warnings,omitempty"`
}

// PoolProviderResult 保存账号出参
type PoolProviderResult struct {
	Provider  ProviderView `json:"provider"`
	Restarted bool         `json:"restarted"`
	Warnings  []string     `json:"warnings,omitempty"`
}

// PoolUseResult use/setMode 出参（带操作后状态，CLI 直接展示）
type PoolUseResult struct {
	Status    PoolStatusView `json:"status"`
	Restarted bool           `json:"restarted"`
	Warnings  []string       `json:"warnings,omitempty"`
}

// ---------- 内部 helper ----------

// providerView 单条账号 → 掩码投影
func providerView(c settings.ProxyProviderConf, activeID string) ProviderView {
	return ProviderView{
		ID:          c.ID,
		Name:        c.Name,
		Vendor:      c.Vendor,
		TradeNo:     c.TradeNo,
		Protocol:    c.Protocol,
		FetchNum:    c.FetchNum,
		Province:    c.Province,
		City:        c.City,
		ISP:         c.ISP,
		FilterUsed:  c.FilterUsed,
		AuthMode:    c.AuthMode,
		Username:    c.Username,
		HasSecret:   c.Secret != "",
		HasPassword: c.Password != "",
		Active:      c.ID == activeID && activeID != "",
	}
}

// resolveProviderIdx id|名称寻址（仿 resolveProjectIDLocked）：
// id 精确命中 > 名称唯一命中 > 名称重名列候选强制用 id；零命中报 ErrPoolNotFound。
func resolveProviderIdx(list []settings.ProxyProviderConf, idOrName string) (int, error) {
	key := strings.TrimSpace(idOrName)
	if key == "" {
		return -1, fmt.Errorf("%w: 账号 id/名称不能为空", ctlapi.ErrPoolBadReq)
	}
	for i := range list {
		if list[i].ID == key {
			return i, nil
		}
	}
	idx, cnt := -1, 0
	var ids []string
	for i := range list {
		if list[i].Name == key {
			cnt++
			if idx < 0 {
				idx = i
			}
			ids = append(ids, list[i].ID)
		}
	}
	switch cnt {
	case 0:
		return -1, fmt.Errorf("%w: %q", ctlapi.ErrPoolNotFound, key)
	case 1:
		return idx, nil
	default:
		return -1, fmt.Errorf("%w: 名称 %q 命中 %d 个账号（id: %s），请改用 id",
			ctlapi.ErrPoolNotFound, key, cnt, strings.Join(ids, ", "))
	}
}

// validateProviderShape CRUD 单条形态校验：settings.validateShape 包外不可见，
// app 层对齐同款规则（vendor 非空/FetchNum 1-100/AuthMode 三值）并补厂商存在性
// （registry 查询由 app 层把关，settings 不反向依赖 proxypool，设计 §5.5）。
func validateProviderShape(c settings.ProxyProviderConf) error {
	if strings.TrimSpace(c.Vendor) == "" {
		return fmt.Errorf("%w: 厂商不能为空", ctlapi.ErrPoolBadReq)
	}
	if !proxypool.VendorRegistered(c.Vendor) {
		return fmt.Errorf("%w: 未注册的厂商 %q", ctlapi.ErrPoolBadReq, c.Vendor)
	}
	if c.FetchNum < 1 || c.FetchNum > 100 {
		return fmt.Errorf("%w: 单次提取数量须在 1-100 之间（当前 %d）", ctlapi.ErrPoolBadReq, c.FetchNum)
	}
	switch c.AuthMode {
	case settings.PoolAuthAuto, settings.PoolAuthUserPass, settings.PoolAuthWhitelist:
	default:
		return fmt.Errorf("%w: 非法鉴权方式 %q", ctlapi.ErrPoolBadReq, c.AuthMode)
	}
	return nil
}

// requireActiveCreds 生效账号凭据强制校验（仅成为/正是生效账号时调用；
// 非生效账号允许凭据未补全的草稿，设计 §5.5）。文案对齐 settings.Validate。
func requireActiveCreds(c settings.ProxyProviderConf) error {
	if strings.TrimSpace(c.TradeNo) == "" || c.Secret == "" {
		return fmt.Errorf("%w: 代理池生效账号 %q 缺少业务编号/业务密钥", ctlapi.ErrPoolBadReq, c.Name)
	}
	return nil
}

// defaultProviderName 新增账号缺省别名："厂商-tradeNo尾4位"（settings 注释约定 app 层补）
func defaultProviderName(c settings.ProxyProviderConf) string {
	tail := c.TradeNo
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	if tail == "" {
		tail = "未命名"
	}
	return c.Vendor + "-" + tail
}

// persistPoolLocked 代理池配置变更统一收尾（调用方持 projMu）：
// 落盘 → needRestart 且代理运行中时热重启（stopProxy→startProxySnap 免锁变体）→ 推一次状态。
// 落盘后热应用失败一律降级 warning 不返 error（配置已持久化、重启后必然生效——
// 否则磁盘已写而调用方以为全部失败，造成磁盘/内存认知不一致，对齐 SaveSettings M9 审计）。
func persistPoolLocked(a *App, needRestart bool) (bool, []string, error) {
	if err := a.gcfg.SaveGlobal(a.cfgDir); err != nil {
		return false, nil, fmt.Errorf("代理池配置保存失败: %w", err)
	}
	if !needRestart {
		return false, nil, nil
	}
	a.mu.Lock()
	running := a.srv != nil
	a.mu.Unlock()
	if !running {
		return false, nil, nil
	}
	g := *a.gcfg
	up := upstreamDesc{Mode: g.UpstreamMode, Manual: g.UpstreamProxy, Pool: g.ProxyPool}
	if err := a.stopProxy(); err != nil {
		a.publishStatusHoldingProj()
		return true, []string{fmt.Sprintf("代理池配置已保存，但停止旧代理失败: %v", err)}, nil
	}
	if err := a.startProxySnap(g.ListenAddr, up); err != nil {
		a.publishStatusHoldingProj()
		return true, []string{fmt.Sprintf("代理池配置已保存，但代理重启失败（可在顶栏手动启动）: %v", err)}, nil
	}
	a.publishStatusHoldingProj()
	return true, nil, nil
}

// poolStatusView 当前池状态视图（mgr 可为 nil=未运行）
func poolStatusView(a *App) PoolStatusView {
	a.projMu.Lock()
	mode := a.gcfg.UpstreamMode
	active := a.gcfg.ProxyPool.ActiveProvider
	onEmpty := a.gcfg.ProxyPool.WithDefaults().OnEmpty
	a.projMu.Unlock()
	v := PoolStatusView{Mode: mode, ActiveProvider: active, OnEmpty: onEmpty}
	if mgr := a.poolCur.Load(); mgr != nil {
		st := mgr.Status()
		v.Running = st.Running
		v.Status = &st
	}
	return v
}

// resolveProviderConf 按 id|名称取出账号配置（锁外快照寻址；白名单/测试用，conf 为值拷贝）
func (a *App) resolveProviderConf(idOrName string) (settings.ProxyProviderConf, error) {
	a.projMu.Lock()
	pool := a.gcfg.ProxyPool.WithDefaults()
	a.projMu.Unlock()
	idx, err := resolveProviderIdx(pool.Providers, idOrName)
	if err != nil {
		return settings.ProxyProviderConf{}, err
	}
	return pool.Providers[idx], nil
}

// lookupProvider 厂商存在性把关 + registry 取实现（validateProviderShape 已查时必中）
func lookupProvider(vendor string) (proxypool.Provider, error) {
	p, ok := proxypool.Lookup(vendor)
	if !ok {
		return nil, fmt.Errorf("%w: 未注册的厂商 %q", ctlapi.ErrPoolBadReq, vendor)
	}
	return p, nil
}

// ---------- §8.2 绑定（12 个） ----------

// ListProxyVendors 已注册厂商清单（前端厂商下拉不硬编码；纯 registry 读，无锁无网络）
func (a *App) ListProxyVendors() []proxypool.VendorMeta {
	return proxypool.Vendors()
}

// ProxyPoolProviders 账号清单（掩码投影）
func (a *App) ProxyPoolProviders() ([]ProviderView, error) {
	a.projMu.Lock()
	pool := a.gcfg.ProxyPool.WithDefaults()
	a.projMu.Unlock()
	views := make([]ProviderView, 0, len(pool.Providers))
	for _, c := range pool.Providers {
		views = append(views, providerView(c, pool.ActiveProvider))
	}
	return views, nil
}

// ProxyPoolSaveProvider 新增/更新账号（id 空=新增生成；更新为整体替换语义，
// secret/password 哨兵：空串=保持、"__clear__"=清空）。改动生效账号且代理池运行中时热重启。
func (a *App) ProxyPoolSaveProvider(conf settings.ProxyProviderConf) (*PoolProviderResult, error) {
	conf.Name = strings.TrimSpace(conf.Name)
	conf.Vendor = strings.TrimSpace(strings.ToLower(conf.Vendor))
	conf.TradeNo = strings.TrimSpace(conf.TradeNo)

	a.projMu.Lock()
	defer a.projMu.Unlock()
	pool := a.gcfg.ProxyPool.WithDefaults()

	isNew := strings.TrimSpace(conf.ID) == ""
	if isNew {
		conf.ID = fmt.Sprintf("%d-%d", time.Now().UnixMilli(), len(pool.Providers)+1)
		if conf.Name == "" {
			conf.Name = defaultProviderName(conf)
		}
		// 新建无原值可保持：__clear__ 哨兵直接归一化为空（审计 L1，与更新/TestFetch 三路径对齐，
		// 防 CLI 程序化传入字面哨兵被当成真实密钥落盘，骗过非空校验后带哨兵请求厂商）
		if conf.Secret == "__clear__" {
			conf.Secret = ""
		}
		if conf.Password == "__clear__" {
			conf.Password = ""
		}
		conf = conf.WithDefaults()
		if err := validateProviderShape(conf); err != nil {
			return nil, err
		}
		pool.Providers = append(pool.Providers, conf)
	} else {
		idx, err := resolveProviderIdx(pool.Providers, conf.ID)
		if err != nil {
			return nil, err
		}
		old := pool.Providers[idx]
		conf.ID = old.ID // 更新以 id 寻址，id 不可改
		if conf.Name == "" {
			conf.Name = old.Name // 别名空=保持（同 secret 哨兵，防空表单误清）
		}
		// 敏感字段哨兵：空串=保持原值
		if conf.Secret == "" {
			conf.Secret = old.Secret
		} else if conf.Secret == "__clear__" {
			conf.Secret = ""
		}
		if conf.Password == "" {
			conf.Password = old.Password
		} else if conf.Password == "__clear__" {
			conf.Password = ""
		}
		conf = conf.WithDefaults()
		if err := validateProviderShape(conf); err != nil {
			return nil, err
		}
		// 正是生效账号：凭据必须完整（草稿态仅允许非生效账号）
		if conf.ID == pool.ActiveProvider {
			if err := requireActiveCreds(conf); err != nil {
				return nil, err
			}
		}
		pool.Providers[idx] = conf
	}

	a.gcfg.ProxyPool = pool
	// 热重启仅当改的是生效账号且当前 pool 模式（非生效草稿不影响运行中代理）
	touchActive := conf.ID == pool.ActiveProvider
	restarted, warns, err := persistPoolLocked(a, touchActive && pool.ActiveProvider != "" && a.gcfg.UpstreamMode == settings.UpstreamPool)
	if err != nil {
		return nil, err
	}
	return &PoolProviderResult{
		Provider:  providerView(conf, pool.ActiveProvider),
		Restarted: restarted,
		Warnings:  warns,
	}, nil
}

// ProxyPoolDeleteProvider 删除账号；删当前生效账号时 ActiveProvider 清空且
// 上游模式回退 direct 收敛（避免悬挂 pool 模式无生效账号），代理运行中时热重启。
func (a *App) ProxyPoolDeleteProvider(idOrName string) (*PoolWriteResult, error) {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	pool := a.gcfg.ProxyPool.WithDefaults()
	idx, err := resolveProviderIdx(pool.Providers, idOrName)
	if err != nil {
		return nil, err
	}
	delID := pool.Providers[idx].ID
	pool.Providers = append(pool.Providers[:idx], pool.Providers[idx+1:]...)

	wasActive := delID == pool.ActiveProvider
	modeChanged := false
	if wasActive {
		pool.ActiveProvider = ""
		if a.gcfg.UpstreamMode == settings.UpstreamPool {
			a.gcfg.UpstreamMode = settings.UpstreamDirect // 删当前→模式回退 direct 收敛（§8.2）
			modeChanged = true
		}
	}
	a.gcfg.ProxyPool = pool

	restarted, warns, err := persistPoolLocked(a, wasActive || modeChanged)
	if err != nil {
		return nil, err
	}
	return &PoolWriteResult{Restarted: restarted, Warnings: warns}, nil
}

// ProxyPoolUseProvider 选择生效账号（凭据必须完整）；pool 模式且代理运行中时热重启。
func (a *App) ProxyPoolUseProvider(idOrName string) (*PoolUseResult, error) {
	a.projMu.Lock()
	pool := a.gcfg.ProxyPool.WithDefaults()
	idx, err := resolveProviderIdx(pool.Providers, idOrName)
	if err != nil {
		a.projMu.Unlock()
		return nil, err
	}
	conf := pool.Providers[idx]
	if err := requireActiveCreds(conf); err != nil {
		a.projMu.Unlock()
		return nil, err
	}
	pool.ActiveProvider = conf.ID
	a.gcfg.ProxyPool = pool
	restarted, warns, err := persistPoolLocked(a, a.gcfg.UpstreamMode == settings.UpstreamPool)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	return &PoolUseResult{Status: poolStatusView(a), Restarted: restarted, Warnings: warns}, nil
}

// ProxyPoolSetMode 切上游模式（direct/manual/system/pool；等价 settings set upstreamMode
// 但带池语义校验：切 pool 前必须已有凭据完整的生效账号）。模式变化且代理运行中时热重启。
func (a *App) ProxyPoolSetMode(mode string) (*PoolUseResult, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	switch mode {
	case settings.UpstreamDirect, settings.UpstreamManual, settings.UpstreamSystem:
		// 非池模式直接放行
	case settings.UpstreamPool:
		a.projMu.Lock()
		pool := a.gcfg.ProxyPool.WithDefaults()
		idx, err := resolveProviderIdx(pool.Providers, pool.ActiveProvider)
		if err != nil {
			a.projMu.Unlock()
			return nil, fmt.Errorf("%w: 使用代理池前须先选择生效账号（proxy pool use）", ctlapi.ErrPoolBadReq)
		}
		if err := requireActiveCreds(pool.Providers[idx]); err != nil {
			a.projMu.Unlock()
			return nil, err
		}
		a.projMu.Unlock()
	default:
		return nil, fmt.Errorf("%w: 非法上游模式 %q（direct|manual|system|pool）", ctlapi.ErrPoolBadReq, mode)
	}

	a.projMu.Lock()
	modeChanged := a.gcfg.UpstreamMode != mode
	a.gcfg.UpstreamMode = mode
	restarted, warns, err := persistPoolLocked(a, modeChanged)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	return &PoolUseResult{Status: poolStatusView(a), Restarted: restarted, Warnings: warns}, nil
}

// ProxyPoolSetOnEmpty 设置池耗尽/提取失败策略（direct=本地直连 | error=断开服务拒绝出站）。
// 策略变化且代理以 pool 模式运行中时热重启（Manager 持配置快照，需重建才生效）。
func (a *App) ProxyPoolSetOnEmpty(onEmpty string) (*PoolUseResult, error) {
	onEmpty = strings.TrimSpace(strings.ToLower(onEmpty))
	switch onEmpty {
	case settings.PoolOnEmptyDirect, settings.PoolOnEmptyError:
	default:
		return nil, fmt.Errorf("%w: 非法池耗尽策略 %q（direct=本地直连 | error=断开服务）", ctlapi.ErrPoolBadReq, onEmpty)
	}
	a.projMu.Lock()
	pool := a.gcfg.ProxyPool.WithDefaults()
	changed := pool.OnEmpty != onEmpty
	pool.OnEmpty = onEmpty
	a.gcfg.ProxyPool = pool
	restarted, warns, err := persistPoolLocked(a, changed && a.gcfg.UpstreamMode == settings.UpstreamPool)
	a.projMu.Unlock()
	if err != nil {
		return nil, err
	}
	return &PoolUseResult{Status: poolStatusView(a), Restarted: restarted, Warnings: warns}, nil
}

// ProxyPoolTestFetch 测试提取（不保存：id 寻址到已存账号时 secret/password/tradeNo
// 空串=沿用已存值，与保存同哨兵语义——否则编辑表单哨兵空值会直传厂商报「不得为空」）。
// 锁外网络 I/O（B1 铁律）；厂商错误 wrap ErrPoolUpstream（msg 透传）。
func (a *App) ProxyPoolTestFetch(conf settings.ProxyProviderConf) (*ProxyTestResult, error) {
	conf.Vendor = strings.TrimSpace(strings.ToLower(conf.Vendor))
	conf = conf.WithDefaults()
	if strings.TrimSpace(conf.ID) != "" {
		a.projMu.Lock()
		pool := a.gcfg.ProxyPool.WithDefaults()
		if idx, err := resolveProviderIdx(pool.Providers, conf.ID); err == nil {
			old := pool.Providers[idx]
			if conf.Secret == "" {
				conf.Secret = old.Secret
			}
			if conf.Password == "" {
				conf.Password = old.Password
			}
			if strings.TrimSpace(conf.TradeNo) == "" {
				conf.TradeNo = old.TradeNo
			}
		}
		a.projMu.Unlock()
	}
	// 「__clear__」清空标记直传厂商无意义，按空处理（厂商会报缺凭据，语义一致）
	if conf.Secret == "__clear__" {
		conf.Secret = ""
	}
	if conf.Password == "__clear__" {
		conf.Password = ""
	}
	if err := validateProviderShape(conf); err != nil {
		return nil, err
	}
	p, err := lookupProvider(conf.Vendor)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fr, err := p.Fetch(ctx, conf, conf.FetchNum)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	if len(fr.Entries) == 0 {
		return nil, fmt.Errorf("%w: 厂商返回 0 条代理", ctlapi.ErrPoolUpstream)
	}
	e := fr.Entries[0]
	res := &ProxyTestResult{
		Addr:     e.Addr(),
		Region:   e.Region,
		WithAuth: e.Username != "",
		Count:    len(fr.Entries),
	}
	if !e.ExpireAt.IsZero() {
		res.ExpireAt = e.ExpireAt.Unix()
	}
	return res, nil
}

// ProxyPoolStatus 池状态（代理未运行/非池模式返回配置态视图，status 为空）
func (a *App) ProxyPoolStatus() (*PoolStatusView, error) {
	v := poolStatusView(a)
	return &v, nil
}

// ProxyPoolRotate 手动轮换（仅 pool 模式且代理运行中；否则 ErrPoolConflict）。
// 锁外网络 I/O（Rotate 内部按需提取）；提取失败 wrap ErrPoolUpstream。
func (a *App) ProxyPoolRotate() (*proxypool.PoolStatus, error) {
	mgr := a.poolCur.Load()
	if mgr == nil {
		return nil, fmt.Errorf("%w: 代理未运行或当前非代理池模式", ctlapi.ErrPoolConflict)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mgr.Rotate(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	st := mgr.Status()
	return &st, nil
}

// ProxyPoolWhitelist 查询终端白名单（厂商 API；ip 无需参数）
func (a *App) ProxyPoolWhitelist(idOrName string) ([]string, error) {
	conf, err := a.resolveProviderConf(idOrName)
	if err != nil {
		return nil, err
	}
	p, err := lookupProvider(conf.Vendor)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wl, err := p.ListWhitelist(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	return wl.IPs, nil
}

// ProxyPoolAddWhitelist 加白名单；ip 空=自动查询当前公网出口 IP（厂商须实现 MyIPer，
// 否则提示手填）。返回操作后最新白名单。
func (a *App) ProxyPoolAddWhitelist(idOrName, ip string) ([]string, error) {
	conf, err := a.resolveProviderConf(idOrName)
	if err != nil {
		return nil, err
	}
	p, err := lookupProvider(conf.Vendor)
	if err != nil {
		return nil, err
	}
	ip = strings.TrimSpace(ip)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if ip == "" {
		m, ok := p.(proxypool.MyIPer)
		if !ok {
			return nil, fmt.Errorf("%w: 厂商 %q 不支持自动查询出口 IP，请手动指定 ip", ctlapi.ErrPoolBadReq, conf.Vendor)
		}
		if ip, err = m.GetMyIP(ctx); err != nil {
			return nil, fmt.Errorf("%w: 查询出口 IP 失败（可手动指定 ip）: %v", ctlapi.ErrPoolUpstream, err)
		}
	}
	if net.ParseIP(ip) == nil {
		return nil, fmt.Errorf("%w: 非法 IP %q", ctlapi.ErrPoolBadReq, ip)
	}
	if err := p.AddWhitelist(ctx, conf, ip); err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	wl, err := p.ListWhitelist(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	return wl.IPs, nil
}

// ProxyPoolDelWhitelist 移出白名单（ip 必填）；返回操作后最新白名单。
func (a *App) ProxyPoolDelWhitelist(idOrName, ip string) ([]string, error) {
	conf, err := a.resolveProviderConf(idOrName)
	if err != nil {
		return nil, err
	}
	p, err := lookupProvider(conf.Vendor)
	if err != nil {
		return nil, err
	}
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return nil, fmt.Errorf("%w: 非法 IP %q", ctlapi.ErrPoolBadReq, ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.DelWhitelist(ctx, conf, ip); err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	wl, err := p.ListWhitelist(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ctlapi.ErrPoolUpstream, err)
	}
	return wl.IPs, nil
}

// ---------- ctlapi.Service 适配（§8.4 HTTP/CLI 入口；与 Wails 绑定共用同一服务层） ----------

// activeProviderID 当前生效账号 id；空（未选择）→ ErrPoolBadReq
func (a *App) activeProviderID() (string, error) {
	a.projMu.Lock()
	id := strings.TrimSpace(a.gcfg.ProxyPool.ActiveProvider)
	a.projMu.Unlock()
	if id == "" {
		return "", fmt.Errorf("%w: 尚未选择生效账号（proxy pool use）", ctlapi.ErrPoolBadReq)
	}
	return id, nil
}

func (s *ctlService) PoolStatus() (any, error)    { return s.app.ProxyPoolStatus() }
func (s *ctlService) PoolVendors() any            { return s.app.ListProxyVendors() }
func (s *ctlService) PoolProviders() (any, error) { return s.app.ProxyPoolProviders() }

// PoolSaveProvider POST 新增 / PUT 整体替换（PUT 路径 id 已由 handler injectJSONID 注入 body）
func (s *ctlService) PoolSaveProvider(raw json.RawMessage) (any, error) {
	var conf settings.ProxyProviderConf
	if err := json.Unmarshal(raw, &conf); err != nil {
		return nil, fmt.Errorf("%w: 请求体解析失败: %v", ctlapi.ErrPoolBadReq, err)
	}
	return s.app.ProxyPoolSaveProvider(conf)
}

func (s *ctlService) PoolDeleteProvider(id string) (any, error) {
	return s.app.ProxyPoolDeleteProvider(id)
}

func (s *ctlService) PoolUseProvider(id string) (any, error) {
	return s.app.ProxyPoolUseProvider(id)
}

func (s *ctlService) PoolSetMode(mode string) (any, error) {
	return s.app.ProxyPoolSetMode(mode)
}

func (s *ctlService) PoolSetOnEmpty(onEmpty string) (any, error) {
	return s.app.ProxyPoolSetOnEmpty(onEmpty)
}

// PoolTestProvider 试提取三形态归一（§8.4）：body 空/{}=当前生效账号、仅 {id}=寻址取凭据、
// 完整 conf（vendor 非空）=直接试提取；统一落 ProxyPoolTestFetch（不落盘）。
func (s *ctlService) PoolTestProvider(raw json.RawMessage) (any, error) {
	a := s.app
	if strings.TrimSpace(string(raw)) == "" {
		raw = []byte("{}")
	}
	var conf settings.ProxyProviderConf
	if err := json.Unmarshal(raw, &conf); err != nil {
		return nil, fmt.Errorf("%w: 请求体解析失败: %v", ctlapi.ErrPoolBadReq, err)
	}
	if strings.TrimSpace(conf.Vendor) != "" {
		return a.ProxyPoolTestFetch(conf)
	}
	id := strings.TrimSpace(conf.ID)
	if id == "" {
		var err error
		if id, err = a.activeProviderID(); err != nil {
			return nil, err
		}
	}
	c2, err := a.resolveProviderConf(id)
	if err != nil {
		return nil, err
	}
	return a.ProxyPoolTestFetch(c2)
}

func (s *ctlService) PoolRotate() (any, error) { return s.app.ProxyPoolRotate() }

// 白名单路由无 {id} 段（§8.4）：一律操作当前生效账号。

func (s *ctlService) PoolWhitelist() (any, error) {
	id, err := s.app.activeProviderID()
	if err != nil {
		return nil, err
	}
	return s.app.ProxyPoolWhitelist(id)
}

// PoolAddWhitelist ip 空=自动获取本机出口 IP，须 auto 且 yes 双重确认（防误加白）
func (s *ctlService) PoolAddWhitelist(ip string, auto, yes bool) (any, error) {
	if strings.TrimSpace(ip) == "" && !(auto && yes) {
		return nil, fmt.Errorf("%w: 自动获取本机出口 IP 须显式确认（auto+yes）", ctlapi.ErrPoolBadReq)
	}
	id, err := s.app.activeProviderID()
	if err != nil {
		return nil, err
	}
	return s.app.ProxyPoolAddWhitelist(id, ip)
}

func (s *ctlService) PoolDelWhitelist(ip string) (any, error) {
	id, err := s.app.activeProviderID()
	if err != nil {
		return nil, err
	}
	return s.app.ProxyPoolDelWhitelist(id, ip)
}
