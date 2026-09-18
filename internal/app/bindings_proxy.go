package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"reflect"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/mitm"
	"prismproxy/internal/proxy"
	"prismproxy/internal/proxypool"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/sysproxy"
)

// ---------- 代理生命周期 Bindings ----------

// StartProxy 启动代理（同步 listen，端口占用立即报错）；addr 为空用配置里的监听地址。
// 成功/失败均向 SSE status 频道推送（SetSystemProxy 内部连调时不重复推）。
func (a *App) StartProxy(addr string) error {
	if err := a.startProxy(addr); err != nil {
		a.publishStatus()
		return err
	}
	a.publishStatus()
	return nil
}

// startProxy 实际启动逻辑（不发布 status，供 SetSystemProxy 内部连调避免重复推送）
func (a *App) startProxy(addr string) error {
	// 全局环境配置快照（projMu → a.mu 锁序：先取 projMu 快照再进 a.mu，禁止反向）
	a.projMu.Lock()
	if addr == "" {
		addr = a.gcfg.ListenAddr
	}
	up := upstreamDesc{Mode: a.gcfg.UpstreamMode, Manual: a.gcfg.UpstreamProxy, Pool: a.gcfg.ProxyPool}
	a.projMu.Unlock()
	return a.startProxySnap(addr, up)
}

// upstreamDesc 上游配置快照（startProxySnap 入参；代理池设计 §8.1：改为描述结构，
// 不再扩字符串参数）。Pool 仅 Mode==pool 时消费（WithDefaults 在 ensure 内统一做）。
type upstreamDesc struct {
	Mode   string
	Manual string
	Pool   settings.ProxyPoolSettings
}

// startProxySnap 用调用方给定的环境快照启动代理（不发布 status）。拆出独立变体是因
// 为 SaveSettings 热重启在已持 projMu 的调用栈内启动代理——projMu 非重入锁，重取即自
// 死锁（旧代理已停、新代理永不启动、控制 API 全线超时）；持锁方必须经本方法传入快照。
func (a *App) startProxySnap(addr string, up upstreamDesc) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		return fmt.Errorf("proxy already running on %s", a.addr)
	}

	var ca *mitm.CA
	if !a.noMITM {
		var err error
		ca, err = mitm.LoadOrCreateCA(filepath.Join(a.cfgDir, "ca"))
		if err != nil {
			return fmt.Errorf("init root ca: %w", err)
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败：%w（端口可能被占用，可在「设置」中更换监听地址/端口）", addr, err)
	}
	opts := &proxy.Options{Engine: a.eng}
	if up.Mode == settings.UpstreamPool {
		// pool 模式：Manager 构造/复用（首提异步——Start 内部 go 补池，
		// 本持锁路径不执行任何厂商网络 I/O，设计 §8.1 B1）
		mgr, err := a.ensureProxyManagerLocked(up.Pool)
		if err != nil {
			ln.Close()
			return err
		}
		a.poolCur.Store(mgr)
		opts.UpstreamResolver = a.poolResolve
		opts.OnBadEntry = a.poolReportBad
		opts.RotateUpstream = a.poolRotate
	} else {
		opts.UpstreamProxy = resolveUpstream(up.Mode, up.Manual, addr)
	}
	srv, err := proxy.NewServerOpts(addr, a.rec, ca, opts)
	if err != nil {
		ln.Close()
		return err
	}
	go func() {
		if err := srv.Serve(ln); err != nil && a.ctx != nil {
			runtime.LogErrorf(a.ctx, "proxy exited: %v", err)
		}
	}()

	a.srv, a.ca, a.addr = srv, ca, addr
	a.startErr = "" // 任何一次成功启动都清除此前的启动失败标记（含手动重启）
	// 代理代际自增：worker 只会执行最新代际的自动任务（热重启时 stop 的 clear 为旧代际，
	// 直接丢弃，杜绝 clear/set 并发乱序落盘的竞态）。
	gen := a.adbGen.Add(1)
	// 代理已监听：自动设置 AutoSet 设备的 http_proxy。异步入队（goroutine 等 defer 解锁后
	// 才拿快照，避免重入死锁），worker 串行执行且只认最新代际——覆盖 GUI 自启/手动/设置重启。
	go a.enqueueAdbOps(gen, true)
	return nil
}

// StopProxy 停止代理并向 SSE status 频道推送（SetSystemProxy 内部连调时走 stopProxy 不重复推）
func (a *App) StopProxy() error {
	err := a.stopProxy()
	a.publishStatus()
	return err
}

func (a *App) stopProxy() error {
	return a.stopProxyWithOpt(true)
}

// stopProxyNoHooks 停止代理但不触发 AutoSet 清除挂钩：Shutdown 已先做同步清除，
// 避免停止后再起一批注定被进程退出截断的冗余 clear goroutine。
func (a *App) stopProxyNoHooks() error {
	return a.stopProxyWithOpt(false)
}

func (a *App) stopProxyWithOpt(autoClear bool) error {
	a.mu.Lock()
	if a.srv == nil {
		a.mu.Unlock()
		return nil
	}
	err := a.srv.Close()
	a.srv = nil
	// 停代理即停池 Manager（设计 §6.1：简化起见停代理即 Stop，重新启动重新提取）；
	// 锁内仅取出指针置空，Stop 的 cancel+wait 放锁外（守 B1：持锁路径不碰厂商 I/O 等待）。
	poolMgr := a.poolMgr
	a.poolMgr = nil
	a.poolSnap = settings.ProxyPoolSettings{}
	a.poolCur.Store(nil)
	gen := uint64(0)
	if autoClear {
		// 代际自增：热重启时紧接的 start 会再自增，本批 clear 因旧代际被 worker 丢弃；
		// 普通停止后没有 start，clear 保持最新代际正常执行。
		gen = a.adbGen.Add(1)
	}
	a.mu.Unlock()
	if poolMgr != nil {
		poolMgr.Stop()
	}
	// 代理已停：清除 AutoSet 设备的 http_proxy，避免设备仍指向失效代理导致断网。
	// 异步入队（goroutine 等解锁后拿快照），worker 串行执行；退出路径另在 Shutdown
	// 中同步清除（stopProxyNoHooks 不再触发本挂钩）。
	if autoClear {
		go a.enqueueAdbOps(gen, false)
	}
	return err
}

func (a *App) GetProxyStatus() ProxyStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode := "MITM"
	if a.ca == nil {
		mode = "tunnel-only"
	}
	return ProxyStatus{Running: a.srv != nil, Addr: a.addr, Mode: mode, FlowCount: len(a.st.List()), StartError: a.startErr}
}

// ResetProxyBypass 清空运行中代理因 TLS 握手失败而降级为盲隧道的 host 集合，
// 让后续连接重新尝试 MITM。典型用于系统 CA 注入成功后自动调用，或 CLI/HTTP 手动兜底。
// 代理未运行时返回 0（无 bypass 可清）。仅影响新连接。
func (a *App) ResetProxyBypass() int {
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	if srv == nil {
		return 0
	}
	return srv.ResetBypass()
}

// resolveUpstream 按全局配置解析上游代理地址（空=直连）；mode/manual 为调用方快照值
func resolveUpstream(mode, manual, selfAddr string) string {
	switch mode {
	case settings.UpstreamManual:
		return manual
	case settings.UpstreamSystem:
		return sysproxy.UpstreamFromSystem(selfAddr)
	}
	return ""
}

// ---------- 动态代理池装配（代理池设计 §6/§8.1） ----------

// ensureProxyManagerLocked 构造/复用池 Manager（调用方持 a.mu）。
// 配置快照未变则复用（池内 IP 不丢）；变更则新建并停旧（配置变更 rebuild，§6.1）。
// 首提异步：Manager.Start 内部 go 补池，本路径不执行任何厂商网络 I/O（B1 铁律）。
func (a *App) ensureProxyManagerLocked(conf settings.ProxyPoolSettings) (*proxypool.Manager, error) {
	conf = conf.WithDefaults()
	if a.poolMgr != nil && reflect.DeepEqual(a.poolSnap, conf) {
		return a.poolMgr, nil
	}
	mgr, err := proxypool.NewManager(conf)
	if err != nil {
		return nil, err
	}
	mgr.SetOnStatusChange(func(st proxypool.PoolStatus) {
		// 代际过滤（审计 M3）：旧 Manager 的 dispatcher 不被 Stop 等待，其队列里滞留的
		// 停机帧可能在新 Manager 启动后才回调；以原子 poolCur 为代际基准，非当前代一律丢弃，
		// 杜绝 running:false 跨代伪帧覆盖新池 running:true。
		if a.poolCur.Load() != mgr {
			return
		}
		a.onPoolStatus(st)
	})
	// 先切换代际基准，再停旧（此刻新 srv 尚未构造、无连接会 resolve，故提前 Store 安全）：
	// 旧 dispatcher 任何延迟帧执行时 poolCur 已指向 mgr，必被上面的过滤丢弃。
	if a.poolMgr != nil {
		a.poolCur.Store(mgr)
		// Stop 仅 cancel+wait 业务协程（在途提取尊重 ctx 即时返回，无厂商 I/O 等待，B1 不破）
		a.poolMgr.Stop()
	}
	mgr.Start()
	a.poolMgr = mgr
	a.poolSnap = conf
	a.poolCur.Store(mgr) // 与首启路径（startProxySnap）统一：确保返回后 poolCur 指向当前代
	return mgr, nil
}

// poolResolve 引擎 UpstreamResolver 的 App 薄适配（proxypool 不反向依赖 proxy 包，§7）。
// 经 poolCur 原子指针间接取当前代 Manager：池配置 CRUD 由 persistPoolLocked 整代理
// 热重启（stopProxy→startProxySnap→ensureProxyManagerLocked 装配新代），重启后引擎
// 自然解析到新 Manager；原子间接同时保证回调代际过滤与停机窗口的无锁读取。
func (a *App) poolResolve(ctx context.Context) (*proxy.UpstreamEndpoint, error) {
	mgr := a.poolCur.Load()
	if mgr == nil {
		return nil, errors.New("代理池未就绪")
	}
	ep, err := mgr.Resolver()(ctx)
	if ep == nil || err != nil {
		return nil, err
	}
	return &proxy.UpstreamEndpoint{Addr: ep.Addr, Username: ep.Username, Password: ep.Password}, nil
}

// poolReportBad 引擎坏点上报 → Manager.ReportBad（迟报由 Manager 内部按 current 过滤）
func (a *App) poolReportBad(addr string) {
	if mgr := a.poolCur.Load(); mgr != nil {
		mgr.ReportBad(addr)
	}
}

// poolRotate 引擎 407 轮换回调 → Manager.Rotate（错误静默：下次 resolve 会重走补池）
func (a *App) poolRotate(ctx context.Context) {
	if mgr := a.poolCur.Load(); mgr != nil {
		_ = mgr.Rotate(ctx)
	}
}

// onPoolStatus Manager 状态变化回调（Manager 保证锁外执行）：
// 推 SSE proxypool 频道 + Wails proxypool:status 事件（频率为提取/轮换级，无需合帧）。
func (a *App) onPoolStatus(st proxypool.PoolStatus) {
	a.mu.Lock()
	hub := a.ctlHub()
	a.mu.Unlock()
	if hub != nil {
		hub.Publish("proxypool", st)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "proxypool:status", st)
	}
}

// rebuildEngine 按当前项目规则重编译规则引擎并热替换（Holder 原子替换）。
// 调用方须持 projMu（a.proj/a.groups 均受 projMu 保护）。
func (a *App) rebuildEngine() error {
	if a.proj == nil {
		// 无打开项目（M11 欢迎页态）：卸载引擎即可，无需编译
		a.eng.Set(nil)
		return nil
	}
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	e, err := rules.NewEngine(a.proj.FilterGroups, a.proj.DecryptRules, gmap)
	if err != nil {
		return err
	}
	a.eng.Set(e)
	return nil
}

// InstallRootCA 把根证书装入当前用户受信根存储（certutil -user，免管理员）
func (a *App) InstallRootCA() error {
	a.mu.Lock()
	ca := a.ca
	a.mu.Unlock()
	if ca == nil {
		return fmt.Errorf("MITM 未启用，无根证书可安装")
	}
	out, err := exec.Command("certutil", "-user", "-addstore", "Root", ca.CertPEMPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil: %v: %s", err, string(out))
	}
	return nil
}
