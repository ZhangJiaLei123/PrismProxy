package app

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/capture"
	"prismproxy/internal/ctlapi"
	"prismproxy/internal/domains"
	"prismproxy/internal/mitm"
	"prismproxy/internal/persist"
	"prismproxy/internal/proxy"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/store"
	"prismproxy/internal/sysproxy"
)

// ---------- App ----------

type App struct {
	ctx context.Context // wails 运行时（startup 后可用）

	st  *store.Store
	rec *capture.Recorder

	cfg    *settings.Settings
	cfgDir string
	eng    *rules.Holder   // 规则引擎热更新容器（proxy 与 Recorder.Filter 共享）
	groups *domains.Groups // 域名组（用户导入，@组名 引用源）

	mu       sync.Mutex
	srv      *proxy.Server
	ca       *mitm.CA
	addr     string
	noMITM   bool
	startErr string // 启动自动抓包失败原因（GetProxyStatus 暴露给前端，事件竞态兜底）
	ctl      *ctlapi.Server // M8 本地控制 API（cli 子命令连接目标；GUI/headless 均启动）

	// M7 SQLite 持久化（方案 §4.11）：pmu 保护 writer 生命周期；落盘为旁路异步队列
	pmu          sync.Mutex
	persist      *persist.Writer
	persistSubbed bool // store 持久化订阅是否已挂（订阅一次，靠 writer 启停控制写入）

	// 事件合帧缓冲（~50ms 窗口，方案 §4.4）
	pendMu   sync.Mutex
	pendUp   map[string]FlowMeta // 同 ID new/update 合并，取最新快照
	pendEv   []string
	flushDue bool
}

// NewApp addr 为空时使用持久化配置里的监听地址
func NewApp(addr string, noMITM bool) *App {
	cfgDir := settings.DefaultConfigDir()

	cfg, err := settings.Load(cfgDir)
	if err != nil {
		cfg = settings.Default()
	}
	// 旧 captureRules/processRules → filterGroups 迁移（规则设计 §六）：迁移即落盘一次
	if cfg.Migrate() {
		if serr := cfg.Save(cfgDir); serr != nil {
			log.Printf("迁移配置落盘失败（内存态已迁移）: %v", serr)
		}
	}

	groups, gerr := domains.LoadUser(filepath.Join(cfgDir, "domains"))
	if gerr != nil {
		groups = nil // 域名组缺失不致命：@组名 引用将不匹配
	}

	eng := &rules.Holder{}
	var gmap map[string][]string
	if groups != nil {
		gmap = groups.Domains
	}
	// 编译失败兜底：log warn + 空引擎全放行（规则设计 §5.3）
	if e, err := rules.NewEngine(cfg.FilterGroups, cfg.DecryptRules, gmap); err == nil {
		eng.Set(e)
	} else {
		log.Printf("过滤规则编译失败，当前过滤未生效（全量显示）: %v", err)
	}

	if addr == "" {
		addr = cfg.ListenAddr
	}
	a := &App{
		cfg:    cfg,
		cfgDir: cfgDir,
		eng:    eng,
		groups: groups,
		addr:   addr,
		noMITM: noMITM,
		pendUp: make(map[string]FlowMeta),
	}
	a.st = store.New(cfg.MaxFlows)
	a.st.SetLimits(cfg.MaxFlows, int64(cfg.MaxBodyMB)<<20)
	a.rec = capture.NewRecorder(a.st)
	proxy.ApplyRulesFilter(a.rec, a.eng) // 捕获/进程规则 exclude → 不记录
	a.st.Subscribe(a.onStoreEvent)
	a.initPersist() // M7：按配置开启 SQLite 持久化（默认关）并加载历史
	return a
}

// Startup wails OnStartup 钩子：启动本地控制 API、会话结束监听、崩溃自愈并按配置自动抓包
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	// 本地控制 API（M8 cli 子命令）：GUI 在线，ui:* 界面事件可用
	a.startCtlAPI()
	a.mu.Lock()
	if a.ctl != nil {
		a.ctl.SetUI(true)
	}
	a.mu.Unlock()
	// 系统关机/注销/重启：隐藏窗口接收 WM_ENDSESSION，同步还原系统代理
	// （Wails 主窗口不处理该消息，OnShutdown 在关机时不触发；见 session_windows.go）
	go watchSessionEnd(a.restoreSystemProxy)
	// 崩溃自愈（验收 #13）：上次接管系统代理期间被强杀 → 按备份还原
	if healed, err := sysproxy.SelfHeal(a.backupFile()); err != nil {
		runtime.LogErrorf(ctx, "系统代理自愈失败: %v", err)
	} else if healed {
		runtime.LogWarning(ctx, "检测到上次异常退出，已恢复原系统代理设置")
	}
	// GUI 启动即抓包；失败（如端口占用）记录状态并通知前端弹提示
	if err := a.StartProxy(""); err != nil {
		runtime.LogErrorf(ctx, "auto start proxy: %v", err)
		a.mu.Lock()
		a.startErr = err.Error()
		a.mu.Unlock()
		runtime.EventsEmit(ctx, "proxy:start-error", err.Error())
		return
	}
	// 配置要求时自动接管系统代理（代理已监听；失败仅记录，不影响使用）
	a.mu.Lock()
	autoSys := a.cfg.AutoSysProxy
	a.mu.Unlock()
	if autoSys {
		if err := a.SetSystemProxy(true); err != nil {
			runtime.LogErrorf(ctx, "自动接管系统代理失败: %v", err)
		}
	}
	// 自动启动 + 自动接管均完成后通知前端刷新顶栏状态。
	// 前端 onMounted 的首次刷新可能早于本函数（startCtlAPI/自愈/注册表操作耗时），
	// 成功路径此前无事件（仅失败发 proxy:start-error），会导致开关恒显"已停止"。
	runtime.EventsEmit(ctx, "proxy:ready")
	a.publishStatus() // SSE status 频道：startup 自动启动/接管完成后（seed 之后的首帧变化）
}

// Shutdown 退出清理：若系统代理正指向本工具则按备份恢复（OnShutdown 钩子，关窗口/退出时触发）
func (a *App) Shutdown(ctx context.Context) {
	a.restoreSystemProxy()
	// 同步清除 AutoSet 设备的 http_proxy（stopProxy 的异步清除不保证在进程退出前完成）。
	a.clearAdbProxiesSync()
	_ = a.StopProxy()
	a.stopCtlAPI()
	a.stopPersist() // M7：刷盘剩余队列并关闭数据库
}

// restoreSystemProxy 若系统代理正指向本工具则按备份恢复（幂等，干净退出与系统关机清理共用）
func (a *App) restoreSystemProxy() {
	a.mu.Lock()
	addr := a.addr
	a.mu.Unlock()
	if st, _, err := sysproxy.Status(addr); err == nil && st == sysproxy.StateOn {
		if err := sysproxy.Disable(addr, a.backupFile()); err != nil && a.ctx != nil {
			runtime.LogErrorf(a.ctx, "恢复系统代理失败: %v", err)
		}
	}
}

func (a *App) backupFile() string { return filepath.Join(a.cfgDir, "sysproxy-backup.json") }

// ---------- 事件总线：store → 50ms 合帧 → 前端 ----------

func (a *App) onStoreEvent(ev store.Event) {
	a.pendMu.Lock()
	switch ev.Type {
	case "new", "update":
		a.pendUp[ev.Flow.ID] = toMeta(ev.Flow) // 值拷贝快照，规避并发读 Flow
	case "evict":
		for _, id := range ev.IDs {
			delete(a.pendUp, id)
		}
		a.pendEv = append(a.pendEv, ev.IDs...)
	}
	if !a.flushDue {
		a.flushDue = true
		time.AfterFunc(50*time.Millisecond, a.flush)
	}
	a.pendMu.Unlock()
}

func (a *App) flush() {
	a.pendMu.Lock()
	ups := make([]FlowMeta, 0, len(a.pendUp))
	for _, m := range a.pendUp {
		ups = append(ups, m)
	}
	evs := a.pendEv
	a.pendUp = make(map[string]FlowMeta)
	a.pendEv = nil
	a.flushDue = false
	a.pendMu.Unlock()

	// SSE fan-out（M8+）：与 Wails emit 同一份合帧结果、同一批次。
	// 必须在下面的 ctx==nil 早退之前——headless 不走 Wails startup、a.ctx 恒 nil，
	// 放在早退之后会导致 headless 推送通道完全无数据（且 GUI 测试发现不了）。
	a.fanOutFlows(ups, evs)

	if a.ctx == nil {
		return // 未 startup/headless：跳过 Wails emit，前端挂载后用 ListFlows 拉全量
	}
	if len(ups) > 0 {
		runtime.EventsEmit(a.ctx, "flow:upsert", ups)
	}
	if len(evs) > 0 {
		runtime.EventsEmit(a.ctx, "flow:evict", evs)
	}
}

// fanOutFlows 把一帧合帧结果推给本地控制 API 的 SSE 订阅者（无控制 API 时 no-op）。
// 复用 flush 的 []FlowMeta/[]string，不新增 store 订阅、不新增合帧逻辑。
func (a *App) fanOutFlows(ups []FlowMeta, evs []string) {
	a.mu.Lock()
	hub := a.ctlHub()
	a.mu.Unlock()
	if hub == nil {
		return
	}
	if len(ups) > 0 {
		fs := make([]ctlapi.Filterable, len(ups)) // FlowMeta 实现 FilterFields()
		for i := range ups {
			fs[i] = ups[i]
		}
		hub.Publish("flows", ctlapi.FlowsUpsert{Type: "upsert", Flows: fs})
	}
	if len(evs) > 0 {
		hub.Publish("flows", ctlapi.FlowsEvict{Type: "evict", IDs: evs})
	}
}

// publishStatus 向 SSE status 频道推一帧当前状态快照（无订阅者时 hub 内部快速返回）。
// 调用点：代理启停、系统代理切换、settings 热应用重启、启动完成、启动失败（设计 §3.2）。
func (a *App) publishStatus() {
	a.mu.Lock()
	hub := a.ctlHub()
	a.mu.Unlock()
	if hub != nil {
		hub.Publish("status", a.ctlStatusSnapshot())
	}
}

// ctlHub 返回当前控制 API 的 SSE Hub；控制 API 未启动（端口占用降级）时为 nil。
func (a *App) ctlHub() *ctlapi.Hub {
	if a.ctl == nil {
		return nil
	}
	return a.ctl.Hub()
}
