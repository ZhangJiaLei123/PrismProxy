package proxypool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"prismproxy/internal/settings"
)

// Manager 代理池运行时（代理池设计 §6）。
// 生命周期：App 持有，pool 模式启动代理时 NewManager+Start，停止代理时 Stop。
// 并发约定：
//   - current 用 atomic.Pointer（Resolver 快路径仅原子读，纳秒级）；
//   - 提取单飞（mu+inflight）：并发建连只发一次厂商请求，等待者有 ResolveWaitSec 上限；
//   - 任何持锁路径不执行厂商网络 I/O（v3 阻断修订 B1）；onChange 回调只在独立
//     dispatcher goroutine 执行（notify 队列），不进任何调用方锁栈。
type Manager struct {
	conf     settings.ProxyPoolSettings // WithDefaults 后快照
	pconf    settings.ProxyProviderConf // 当前生效账号
	provider Provider

	current atomic.Pointer[ProxyEntry] // 粘滞出口

	mu           sync.Mutex
	pool         []ProxyEntry  // 备用池
	fetching     bool          // Resolving 窗口标记（设计 §6.4 Fetching）
	inflight     chan struct{} // 当前单飞完成通道（每轮 kickFetch 新建，仅创建它的那轮可 close）
	lastFetch    time.Time
	lastErr      string
	surplus      int // 套餐余量（-1=未知）
	failCount    int // 连续提取失败次数（退避指数）
	backoffUntil time.Time
	badCount     int  // 当前 entry 连续坏点数
	degraded     bool // 池空已降级直连（设计 §6.4 Degraded）
	stopped      bool
	onChange     func(PoolStatus) // App 注入（推 Wails 事件）；只在独立 dispatcher goroutine 调用，永不进调用方锁栈

	// 状态通知单消费者队列：所有状态变化投递到此，由 dispatcher 串行回调 onChange。
	// 必要性：App 的 onChange（onPoolStatus）要取 App.mu，而启动/状态路径可能正持有
	// App.mu（startProxySnap 持锁调 Start→kickFetch），若同步回调即自死锁。
	notify chan PoolStatus

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // run/fetchOnce 业务协程（Stop 必须等其退出：它们在途持有厂商连接）
	// dispatcher 不纳入 wg/Wait：它执行的 onChange 会反向取 App.mu，而 Stop 可能正被
	// 持 App.mu 的调用方（ensureProxyManagerLocked 重建旧 Manager）调用，Wait 回调=自死锁。
	// dispatcher 随 ctx.Done 自行退出（见 dispatchStatus），Stop 只保证业务协程收敛。
}

// EndpointView 当前出口视图（不含密码/Raw，设计 §6.4）
type EndpointView struct {
	Addr   string `json:"addr"`
	Region string `json:"region"`
}

// PoolStatus 池状态（设计 §6.4；CLI/HTTP/事件同构，不含任何 secret/password/Raw）
type PoolStatus struct {
	Running      bool          `json:"running"`
	Vendor       string        `json:"vendor"`
	ProviderName string        `json:"providerName"`
	Current      *EndpointView `json:"current"`
	ExpireAt     int64         `json:"expireAt"` // unix 秒，0=未知
	RemainSec    int           `json:"remainSec"`
	PoolSize     int           `json:"poolSize"`
	SurplusTotal int           `json:"surplusTotal"` // 套餐余量（-1=未知）
	LastFetchAt  int64         `json:"lastFetchAt"`
	LastError    string        `json:"lastError"`
	OnEmpty      string        `json:"onEmpty"`
	Fetching     bool          `json:"fetching"`
	Degraded     bool          `json:"degraded"`
}

// NewManager 构造 Manager（校验生效账号与厂商注册；配置快照已 WithDefaults）
func NewManager(conf settings.ProxyPoolSettings) (*Manager, error) {
	conf = conf.WithDefaults()
	pconf := conf.ActiveConf()
	if pconf == nil {
		return nil, errors.New("代理池未选择生效账号")
	}
	prov, ok := Lookup(pconf.Vendor)
	if !ok {
		return nil, fmt.Errorf("厂商 %q 未注册", pconf.Vendor)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		conf:     conf,
		pconf:    *pconf,
		provider: prov,
		surplus:  -1,
		notify:   make(chan PoolStatus, 64),
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

// dispatchStatus 状态回调单消费者：保证 onChange 串行且绝不运行在任何持锁调用栈
// （kickFetch/fetchOnce/Rotate/resolve 均可在 App 持 App.mu 的链路上投递通知）。
// 缓冲满（64）时新帧丢弃（notifyLocked 非阻塞）——UI 轮询/事件重锚以最新值为准。
// 退出走 ctx.Done 而非 close(notify)：Stop 不能等本协程退出（回调要取 App.mu，
// 而 Stop 可能正持 App.mu 被调用）；停止后 notify 不再有人投递，随 Manager 被 GC 回收。
func (m *Manager) dispatchStatus() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case st := <-m.notify:
			if cb := m.onChange; cb != nil {
				cb(st)
			}
		}
	}
}

// notifyLocked 投递一次状态（调用方持 m.mu）；非阻塞，停机或队列满直接放弃。
func (m *Manager) notifyLocked(st PoolStatus) {
	if m.stopped {
		return
	}
	// 不阻塞调用方（其可能正持 App.mu）：队列满（回调慢）即丢中间帧
	select {
	case m.notify <- st:
	default:
	}
}

// notifyStoppedLocked 停机末帧投递（已置 stopped，故不走 notifyLocked）。
// ctx 已取消后 dispatcher 可能已退出，必须与 ctx.Done 二选一，禁止裸发（防 goroutine 永久阻塞）。
func (m *Manager) notifyStoppedLocked(st PoolStatus) {
	select {
	case m.notify <- st:
	case <-m.ctx.Done():
	default:
	}
}

// SetOnStatusChange 注入状态变化回调（App 推 Wails 事件用；回调在锁外执行）
func (m *Manager) SetOnStatusChange(cb func(PoolStatus)) {
	m.mu.Lock()
	m.onChange = cb
	m.mu.Unlock()
}

// Start 启动后台续期循环 + 异步首次提取（B1：构造路径不做厂商 I/O）。
// 回调协程随 Start 启动；即使本函数被 App 持 App.mu 调用，onChange 也在独立协程执行，无自死锁。
// Start 只调一次（Manager 一次性生命周期，重建走 NewManager），故无需/未做重复启动防护。
func (m *Manager) Start() {
	go m.dispatchStatus()
	m.wg.Add(1)
	go m.run()
	m.kickFetch()
}

// Stop 收敛业务协程（在途提取随 ctx 取消返回）；幂等，可被持 App.mu 的调用方安全调用。
// 不等待 dispatcher（其回调要取 App.mu，等待即自死锁）：末帧尽力投递后取消 ctx，
// dispatcher 自行退出。重建路径（ensureProxyManagerLocked 持 a.mu 停旧 Manager）由此安全。
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true // fetching/inflight 由在途 fetchOnce 自行收尾（wg.Wait 会等其退出），这里不越权复位
	m.notifyStoppedLocked(m.statusLocked())
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
}

// Resolver 取当前上游（设计 §6.2）：返回闭包供引擎每连接调用。
// 有货时仅原子指针读；(nil,nil)=直连（OnEmpty=direct 降级）；err=拒绝出站。
func (m *Manager) Resolver() func(ctx context.Context) (*Endpoint, error) {
	return m.resolve
}

func (m *Manager) resolve(ctx context.Context) (*Endpoint, error) {
	if e := m.usableCurrent(); e != nil {
		return endpointOf(e), nil
	}
	// current 空缺（被 ReportBad 剔除/刚过期）先消费备用池，与 Rotate 对齐：
	// 手中有未过期 IP 必须立即顶上，不能跳过备用池直接补池/误判降级（审计 M2）。
	m.mu.Lock()
	m.promoteLocked()
	if e := m.usableCurrent(); e != nil {
		m.notifyLocked(m.statusLocked())
		m.mu.Unlock()
		return endpointOf(e), nil
	}
	m.mu.Unlock()
	// 备用池也空：触发补池（单飞）并等待窗口期完成，上限 ResolveWaitSec，随 ctx 取消
	m.kickFetch()
	m.mu.Lock()
	ch := m.inflight
	fetching := m.fetching
	m.mu.Unlock()
	if fetching && ch != nil {
		wait := time.Duration(m.conf.Rotation.ResolveWaitSec) * time.Second
		timer := time.NewTimer(wait)
		select {
		case <-ch:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if e := m.usableCurrent(); e != nil {
		return endpointOf(e), nil
	}
	// 新提取合池后可能只入了备用池而 current 仍空（并发轮换等），再尝试提升一次
	m.mu.Lock()
	m.promoteLocked()
	if e := m.usableCurrent(); e != nil {
		m.notifyLocked(m.statusLocked())
		m.mu.Unlock()
		return endpointOf(e), nil
	}
	m.mu.Unlock() // 未命中也必须释放：下面 OnEmpty 块重新取锁（漏释放=不可重入自锁）
	// 彻底没货 → OnEmpty
	m.mu.Lock()
	cooldown := time.Until(m.backoffUntil)
	lastErr := m.lastErr
	onEmpty := m.conf.OnEmpty
	if onEmpty == settings.PoolOnEmptyDirect {
		m.degraded = true // 裸奔窗口标黄（设计 §6.2）
	}
	m.notifyLocked(m.statusLocked())
	m.mu.Unlock()
	if onEmpty == settings.PoolOnEmptyDirect {
		return nil, nil
	}
	if cooldown > 0 {
		return nil, fmt.Errorf("代理池提取冷却中（剩余约 %d 秒）: %s", int(cooldown.Seconds())+1, lastErr)
	}
	if lastErr != "" {
		return nil, fmt.Errorf("代理池无可用 IP: %s", lastErr)
	}
	return nil, errors.New("代理池无可用 IP")
}

// Ensure 主动确保有货（CLI/测试用）：触发提取并等待本次单飞完成
func (m *Manager) Ensure(ctx context.Context) error {
	if m.usableCurrent() != nil {
		return nil
	}
	m.kickFetch()
	m.mu.Lock()
	ch := m.inflight
	fetching := m.fetching
	m.mu.Unlock()
	if fetching && ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.usableCurrent() != nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastErr != "" {
		return errors.New(m.lastErr)
	}
	return errors.New("代理池无可用 IP")
}

// Rotate 手动换 IP（设计 §6.3）：丢弃当前 entry，备用池有货立即切换，否则补池等待
func (m *Manager) Rotate(ctx context.Context) error {
	m.mu.Lock()
	m.current.Store(nil)
	m.badCount = 0
	m.promoteLocked()
	has := m.current.Load() != nil
	m.notifyLocked(m.statusLocked())
	m.mu.Unlock()
	if has {
		return nil
	}
	return m.Ensure(ctx)
}

// ReportBad 坏点上报（设计 §6.3 三通道归因：dial 失败/407/防环命中）。
// 仅当 addr 仍是当前 entry 时计数（已轮换的迟报不计）；达 MaxRetry 剔除并换下一个。
func (m *Manager) ReportBad(addr string) {
	m.mu.Lock()
	cur := m.current.Load()
	if cur == nil || cur.Addr() != addr {
		m.mu.Unlock()
		return
	}
	m.badCount++
	if m.badCount < max(m.conf.Rotation.MaxRetry, 1) {
		m.mu.Unlock()
		return
	}
	m.current.Store(nil)
	m.badCount = 0
	m.promoteLocked()
	empty := m.current.Load() == nil
	m.notifyLocked(m.statusLocked())
	m.mu.Unlock()
	if empty {
		m.kickFetch()
	}
}

// Status 当前池状态快照
func (m *Manager) Status() PoolStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

// ---------- 内部 ----------

// run 后台续期循环：到期前 RefreshAheadSec/池量不足时触发补池
func (m *Manager) run() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		var wait time.Duration
		cur := m.current.Load()
		switch {
		case cur == nil:
			wait = time.Second // 无货轻轮询（kickFetch 有单飞+退避保护，不紧密打厂商）
		case cur.ExpireAt.IsZero():
			wait = time.Minute // 无过期信息：低频检查池量
		default:
			wait = time.Until(cur.ExpireAt.Add(-m.refreshAhead()))
			if wait < time.Second {
				wait = time.Second
			}
		}
		m.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-m.ctx.Done():
			t.Stop()
			return
		case <-t.C:
			m.maybeRefresh()
		}
	}

}

func (m *Manager) refreshAhead() time.Duration {
	return time.Duration(m.conf.Rotation.RefreshAheadSec) * time.Second
}

// maybeRefresh 续期/补池判定
func (m *Manager) maybeRefresh() {
	m.mu.Lock()
	cur := m.current.Load()
	need := cur == nil ||
		(!cur.ExpireAt.IsZero() && time.Until(cur.ExpireAt) <= m.refreshAhead()) ||
		len(m.pool) < m.conf.Rotation.MinPoolSize
	m.mu.Unlock()
	if need {
		m.kickFetch()
	}
}

// kickFetch 触发一次后台提取；已在提取/冷却中/已停止则不动作
func (m *Manager) kickFetch() bool {
	m.mu.Lock()
	if m.stopped || m.fetching || time.Now().Before(m.backoffUntil) {
		m.mu.Unlock()
		return false
	}
	m.fetching = true
	ch := make(chan struct{})
	m.inflight = ch
	m.notifyLocked(m.statusLocked())
	m.wg.Add(1)
	go m.fetchOnce(ch)
	m.mu.Unlock()
	return true
}

// fetchOnce 执行一次厂商提取并合池（单飞执行体；网络 I/O 在锁外）。
// inflight 由 kickFetch 创建并传入：只有创建它的这一轮才能 close，避免与 run 循环
// 在停机竞态下 kick 出的新一轮通道串用导致 close of closed channel。
func (m *Manager) fetchOnce(inflight chan struct{}) {
	defer m.wg.Done()
	res, err := m.provider.Fetch(m.ctx, m.pconf, m.pconf.FetchNum)
	m.mu.Lock()
	if err != nil {
		if m.ctx.Err() == nil { // Stop 取消不算失败
			m.failCount++
			m.lastErr = err.Error()
			m.backoffUntil = time.Now().Add(m.backoffLocked())
		}
	} else {
		m.failCount = 0
		m.lastErr = ""
		m.backoffUntil = time.Time{}
		m.lastFetch = res.FetchedAt
		m.surplus = res.SurplusTotal
		m.mergePoolLocked(res.Entries)
		// current 空缺/已进入续期窗口 → 切换（旧连接不断，新连接走新 IP）
		cur := m.current.Load()
		if cur == nil || (!cur.ExpireAt.IsZero() && time.Until(cur.ExpireAt) <= m.refreshAhead()) {
			m.promoteLocked()
		}
		if m.current.Load() != nil {
			m.degraded = false
		}
	}
	// 只有当前轮仍是我创建的通道时才复位 fetching 并关闭；已被新一轮顶替则原样保留
	if m.inflight == inflight {
		m.fetching = false
		close(inflight)
	}
	m.notifyLocked(m.statusLocked())
	m.mu.Unlock()
}

// mergePoolLocked 新条目合池：与 current/池内同 addr 的去重（新 ExpireAt 更晚则续用），不同则入池
func (m *Manager) mergePoolLocked(entries []ProxyEntry) {
	cur := m.current.Load()
	for _, e := range entries {
		if cur != nil && e.Addr() == cur.Addr() {
			if e.ExpireAt.After(cur.ExpireAt) {
				cp := *cur
				cp.ExpireAt = e.ExpireAt
				m.current.Store(&cp)
			}
			continue
		}
		dup := false
		for i, pe := range m.pool {
			if pe.Addr() == e.Addr() {
				if e.ExpireAt.After(pe.ExpireAt) {
					m.pool[i] = e
				}
				dup = true
				break
			}
		}
		if !dup {
			m.pool = append(m.pool, e)
		}
	}
}

// promoteLocked 从备用池提一个有效条目为 current：优先与现 current 不同 addr（换 IP），
// 其次同 addr 更新 ExpireAt（续用）；无有效条目不动作
func (m *Manager) promoteLocked() {
	now := time.Now()
	cur := m.current.Load()
	curAddr := ""
	if cur != nil {
		curAddr = cur.Addr()
	}
	for i, e := range m.pool {
		if e.Addr() == curAddr {
			continue
		}
		if e.ExpireAt.IsZero() || e.ExpireAt.After(now) {
			cp := e
			m.current.Store(&cp)
			m.pool = append(m.pool[:i], m.pool[i+1:]...)
			return
		}
	}
	for i, e := range m.pool {
		if e.ExpireAt.IsZero() || e.ExpireAt.After(now) {
			cp := e
			m.current.Store(&cp)
			m.pool = append(m.pool[:i], m.pool[i+1:]...)
			return
		}
	}
}

// backoffLocked 提取失败指数退避：2/5/10s 起翻倍，封顶 60s（设计 §6.3）
func (m *Manager) backoffLocked() time.Duration {
	switch m.failCount {
	case 1:
		return 2 * time.Second
	case 2:
		return 5 * time.Second
	default:
		d := 10 * time.Second << (m.failCount - 3)
		if d > 60*time.Second || d <= 0 { // 溢出防护
			d = 60 * time.Second
		}
		return d
	}
}

// usableCurrent 当前可用 entry（未过期）；过期返回 nil
func (m *Manager) usableCurrent() *ProxyEntry {
	e := m.current.Load()
	if e == nil {
		return nil
	}
	if !e.ExpireAt.IsZero() && !e.ExpireAt.After(time.Now()) {
		return nil
	}
	return e
}

// statusLocked 构造状态快照（持锁内调用；不含 Raw/密码，设计 §6.1 G3）
func (m *Manager) statusLocked() PoolStatus {
	st := PoolStatus{
		Running:      !m.stopped,
		Vendor:       m.pconf.Vendor,
		ProviderName: m.pconf.Name,
		SurplusTotal: m.surplus,
		LastFetchAt:  m.lastFetch.Unix(),
		LastError:    m.lastErr,
		OnEmpty:      m.conf.OnEmpty,
		Fetching:     m.fetching,
		Degraded:     m.degraded,
	}
	if cur := m.current.Load(); cur != nil {
		st.Current = &EndpointView{Addr: cur.Addr(), Region: cur.Region}
		if !cur.ExpireAt.IsZero() {
			st.ExpireAt = cur.ExpireAt.Unix()
			st.RemainSec = max(int(time.Until(cur.ExpireAt).Seconds()), 0)
		}
		st.PoolSize = len(m.pool) + 1
	} else {
		st.PoolSize = len(m.pool)
	}
	return st
}

// endpointOf 条目转 Resolver 出参
func endpointOf(e *ProxyEntry) *Endpoint {
	return &Endpoint{Addr: e.Addr(), Username: e.Username, Password: e.Password}
}
