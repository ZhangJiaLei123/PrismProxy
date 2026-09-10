package app

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

	gcfg   *settings.GlobalSettings // 全局环境配置（config/settings.json）
	proj   *settings.ProjectConfig  // 当前项目规则配置（config/projects/<id>/project.json）
	cfgDir string
	eng    *rules.Holder   // 规则引擎热更新容器（proxy 与 Recorder.Filter 共享）
	groups *domains.Groups // 当前项目域名组（用户导入，@组名 引用源；随项目切换替换）

	// 项目切换串行化（M9，项目配置设计 §5.4）：SwitchProject/项目 CRUD/SaveSettings/
	// 规则与域名组写操作均持有；锁序固定 projMu → a.mu/a.pmu，禁止反向。
	// projGen 项目代际（切换时 +1；recorder 每流读取故用原子，不走锁）。
	projMu  sync.Mutex
	projGen atomic.Uint64

	mu       sync.Mutex
	srv      *proxy.Server
	ca       *mitm.CA
	addr     string
	noMITM   bool
	startErr string // 启动自动抓包失败原因（GetProxyStatus 暴露给前端，事件竞态兜底）
	ctl      *ctlapi.Server // M8 本地控制 API（cli 子命令连接目标；GUI/headless 均启动）

	// M7 SQLite 持久化（方案 §4.11）：pmu 保护 writer 生命周期；落盘为旁路异步队列。
	// M9 起 writer 带所属项目 id/gen 打标（persistOwner，见 persist_wiring.go）。
	pmu          sync.Mutex
	persist      *persistOwner
	persistSubbed bool // store 持久化订阅是否已挂（订阅一次，靠 writer 启停控制写入）

	// M12 标签归档（标签 = 归档，设计 §4.2）：与自动录制解耦的独立 Writer
	// （retainDays/maxMB=0，不做保留清理），按需懒打开、随项目切换关闭。
	// 锁序：projMu → archiveMu / tagIndex 写重建；tagIndex 写重建期间不得触发
	// store 事件发射（onStoreEvent→toMeta 读 tagIndex，重入/竞态，设计 §4.3 铁律）。
	archiveMu sync.Mutex
	archive   *persist.Writer
	// archiveWG 在途归档库操作计数（M12 审计修复 M1）：取到 writer 指针后 Add(1)、
	// 用完 Done()；closeArchive 置 nil 拒新后 Wait() 等全部在途操作结束才 Close，
	// 杜绝「锁外 Close 与在途批量写并发 → database is closed」。
	archiveWG sync.WaitGroup
	// archiveGen 常驻 archive 创建时绑定的项目代际（M12 审计修复 M1）：
	// acquireArchive 在 archiveMu 内创建+Add 单次临界区完成，并记录当时 projGen；
	// 调用方核对与入口代际一致后才允许写，封死「打标期间项目切换→旧 archive 已置 nil
	// 却懒打开新库写进新项目」的错库窗口。closeArchive 后清零。
	archiveGen uint64
	// tagIndex：flowID → 标签名列表。copy-on-write + atomic.Pointer——
	// 读侧（toMeta，store 事件/绑定/HTTP 三类 goroutine）零锁取快照；
	// 写侧（打标/重命名/删除/历史回填/项目切换）整体重建新 map 后原子替换。
	tagIndex atomic.Pointer[map[string][]string]
	// deletedFlows tombstone（仅 DeleteTag(deleteFlows=true) 记录）：封幽灵复活——
	// 已连删的流若仍存活于内存环形队列，其后续 update 事件会被 onPersistEvent
	// 拦截不入录制队列，避免录制 Writer 重新 upsert 让已删数据复活（设计 §4.4）。
	tombMu       sync.Mutex
	deletedFlows map[string]struct{}
	tombOrder    []string // FIFO 淘汰序（M12 审计修复 L5：墓碑集合有界）

	// ADB 自动代理：启停代际号（每次启动/停止自增）+ 单 worker 串行任务队列，
	// worker 只执行最新代际的任务，收敛热重启/快速启停时 clear 与 set 的乱序竞态。
	adbGen atomic.Uint64
	adbCh  chan adbOp

	// 事件合帧缓冲（~50ms 窗口，方案 §4.4）
	pendMu   sync.Mutex
	pendUp   map[string]FlowMeta // 同 ID new/update 合并，取最新快照
	pendEv   []string
	flushDue bool

	// M12 复盘页静态资源（main 包 //go:embed frontend/dist 注入；ctlapi 托管 /review.html）。
	// 根 embed.FS——ctlapi 子树 fs.Sub 由 startCtlAPI 完成；nil（测试）时不托管静态资源。
	staticFS fs.FS
}

// NewApp addr 为空时使用全局配置里的监听地址。
// staticFS 为前端 dist 静态资源（main 包 //go:embed 注入，供 ctlapi 托管复盘页）；测试传 nil。
func NewApp(addr string, noMITM bool, staticFS fs.FS) *App {
	cfgDir := settings.DefaultConfigDir()

	// M9：旧版单配置 → 多项目结构一次性迁移（幂等；失败仅记录，按新结构兜底启动）
	if err := settings.MigrateToProjects(cfgDir); err != nil {
		log.Printf("配置迁移失败（按多项目默认兜底启动）: %v", err)
	}

	gcfg, err := settings.LoadGlobal(cfgDir)
	if err != nil {
		log.Printf("全局配置损坏，使用默认全局配置: %v", err)
		gcfg = settings.DefaultGlobal()
	}
	// M11：清单为空 = 无打开项目态（欢迎页）。不再自动创建「默认项目」——
	// 首次启动直接进欢迎页由用户新建；全部项目删完后同样回到此态。
	var proj *settings.ProjectConfig
	var groups *domains.Groups
	// 启动自愈：剔除清单中目录已缺失的幽灵条目（半迁移/手工删除 config/projects/<id> 导致），
	// 并落盘修正。清洗后清单全空即进入欢迎页，避免打开无目录的幽灵项目。
	if settings.PruneMissingProjects(cfgDir, gcfg) {
		if serr := gcfg.SaveGlobal(cfgDir); serr != nil {
			log.Printf("清洗项目清单后落盘失败: %v", serr)
		}
	}
	if len(gcfg.Projects) > 0 {
		// currentProject 校验：不在清单或目录缺失时回退清单第一个并落盘修正（设计 §5.1）
		meta := gcfg.Projects[0]
		valid := false
		for _, m := range gcfg.Projects {
			if m.ID == gcfg.CurrentProject {
				meta = m
				if info, err := os.Stat(settings.ProjectDir(cfgDir, m.ID)); err == nil && info.IsDir() {
					valid = true
				}
				break
			}
		}
		if !valid {
			log.Printf("当前项目 %q 无效，回退到 %q", gcfg.CurrentProject, gcfg.Projects[0].ID)
			meta = gcfg.Projects[0]
			gcfg.CurrentProject = meta.ID
			if serr := gcfg.SaveGlobal(cfgDir); serr != nil {
				log.Printf("回退当前项目落盘失败: %v", serr)
			}
		}

		p, err := settings.LoadProjectConfig(cfgDir, meta.ID, meta.Name)
		if err != nil {
			log.Printf("项目配置损坏，使用空白默认: %v", err)
			p = settings.DefaultProjectConfig(meta.ID, meta.Name)
		}
		p.Name = meta.Name // 显示名以清单为准
		// 旧 captureRules/processRules → filterGroups 迁移（规则设计 §六）：迁移即落盘一次
		if p.Migrate() {
			if serr := settings.SaveProjectConfig(cfgDir, p); serr != nil {
				log.Printf("迁移项目配置落盘失败（内存态已迁移）: %v", serr)
			}
		}
		proj = p

		g, gerr := domains.LoadUser(settings.ProjectDomainsDir(cfgDir, proj.ID))
		if gerr != nil {
			g = nil // 域名组缺失不致命：@组名 引用将不匹配
		}
		groups = g
	}

	eng := &rules.Holder{}
	// 无项目态：eng 保持 nil Engine（ShouldDisplay 默认全放行；欢迎页不展示流列表）。
	// 编译失败兜底：log warn + 空引擎全放行（规则设计 §5.3）。
	if proj != nil {
		var gmap map[string][]string
		if groups != nil {
			gmap = groups.Domains
		}
		if e, err := rules.NewEngine(proj.FilterGroups, proj.DecryptRules, gmap); err == nil {
			eng.Set(e)
		} else {
			log.Printf("过滤规则编译失败，当前过滤未生效（全量显示）: %v", err)
		}
	}

	if addr == "" {
		addr = gcfg.ListenAddr
	}
	a := &App{
		gcfg:     gcfg,
		proj:     proj,
		cfgDir:   cfgDir,
		eng:      eng,
		groups:   groups,
		addr:     addr,
		noMITM:   noMITM,
		pendUp:   make(map[string]FlowMeta),
		staticFS: staticFS,
	}
	a.st = store.New(gcfg.MaxFlows)
	a.st.SetLimits(gcfg.MaxFlows, int64(gcfg.MaxBodyMB)<<20)
	a.rec = capture.NewRecorder(a.st)
	// 项目代际打标（项目配置设计 §5.2）：切换项目后旧代际在途流的终态丢弃
	a.rec.GenFunc = func() uint64 { return a.projGen.Load() }
	proxy.ApplyRulesFilter(a.rec, a.eng) // 捕获/进程规则 exclude → 不记录
	a.st.Subscribe(a.onStoreEvent)
	a.initPersist() // M7：按配置开启 SQLite 持久化（默认关）并加载历史
	a.startAdbWorker() // ADB 自动代理任务 worker（串行 + 代际收敛）
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
	// 系统关机/注销/重启：隐藏窗口接收 WM_ENDSESSION，同步还原系统代理并清除设备代理
	// （Wails 主窗口不处理该消息，OnShutdown 在关机时不触发；见 session_windows.go）
	go watchSessionEnd(a.onSessionEnd)
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
	a.projMu.Lock()
	autoSys := a.gcfg.AutoSysProxy
	a.projMu.Unlock()
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
	// 同步清除 AutoSet 设备的 http_proxy（多设备并行、8s 超时；异步 worker 不保证
	// 在进程退出前完成）。随后 stopProxyNoHooks 停止监听但不再触发清除挂钩（避免冗余 clear）。
	a.clearAdbProxiesSync(adbSessionTimeout)
	a.restoreSystemProxy()
	_ = a.stopProxyNoHooks()
	a.stopCtlAPI()
	a.stopPersist()  // M7：刷盘剩余队列并关闭录制数据库
	a.closeArchive() // M12：关闭标签归档库（幂等）
}

// onSessionEnd 系统关机/注销/重启回调（WM_ENDSESSION，见 session_windows.go）：
// OnShutdown 此时不触发，故系统代理还原与设备代理清除都在此同步完成（短超时，
// 关机时限紧迫；设备代理不清会在下次开机 PrismProxy 未启动前让模拟器/真机断网）。
func (a *App) onSessionEnd() {
	a.clearAdbProxiesSync(adbSessionTimeout)
	a.restoreSystemProxy()
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

// projDir 当前项目目录 config/projects/<id>/（M9）；无打开项目（M11 欢迎页态）返回空串。
// 调用方必须先判断有当前项目（无项目态不应需要项目目录；dbPath 已自行守卫）。
func (a *App) projDir() string {
	if a.proj == nil {
		return ""
	}
	return settings.ProjectDir(a.cfgDir, a.proj.ID)
}

// currentID 当前项目 id；无打开项目返回空串（M11）。
func (a *App) currentID() string {
	if a.proj == nil {
		return ""
	}
	return a.proj.ID
}

// currentMeta 当前项目清单项（调用方持 projMu 或接受弱一致读）；无打开项目返回零值。
func (a *App) currentMeta() settings.ProjectMeta {
	if a.proj == nil {
		return settings.ProjectMeta{}
	}
	for _, m := range a.gcfg.Projects {
		if m.ID == a.proj.ID {
			return m
		}
	}
	return settings.ProjectMeta{ID: a.proj.ID, Name: a.proj.Name}
}

// ---------- 事件总线：store → 50ms 合帧 → 前端 ----------

func (a *App) onStoreEvent(ev store.Event) {
	a.pendMu.Lock()
	switch ev.Type {
	case "new", "update":
		a.pendUp[ev.Flow.ID] = a.toMeta(ev.Flow) // 值拷贝快照，规避并发读 Flow（M12 注入 Tags）
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

// publishStatusHoldingProj 调用方已持 projMu 时的免锁推送变体（SaveSettings 热应用等
// 持锁路径专用）：projMu 非重入锁，snapshot 构造若重取 projMu 会自死锁，须走
// ctlStatusSnapshotWithProject；普通路径一律用 publishStatus。
func (a *App) publishStatusHoldingProj() {
	a.mu.Lock()
	hub := a.ctlHub()
	a.mu.Unlock()
	if hub != nil {
		hub.Publish("status", a.ctlStatusSnapshotWithProject(a.currentMeta()))
	}
}

// ctlHub 返回当前控制 API 的 SSE Hub；控制 API 未启动（端口占用降级）时为 nil。
func (a *App) ctlHub() *ctlapi.Hub {
	if a.ctl == nil {
		return nil
	}
	return a.ctl.Hub()
}
