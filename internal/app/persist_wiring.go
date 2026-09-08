package app

import (
	"fmt"
	"log"
	"path/filepath"

	"prismproxy/internal/capture"
	"prismproxy/internal/persist"
	"prismproxy/internal/settings"
	"prismproxy/internal/store"
)

// ---------- M7 SQLite 持久化接线（方案 §4.11） ----------
//
// 落盘是旁路：store 事件 → persist.Writer 有界异步队列（满则丢弃计数，绝不阻塞转发）。
// 历史流加载时 body 不入内存（惰性回查 DB），内存 store 语义（淘汰/置顶/清空）不变。
//
// 锁约定（关键）：pmu 仅保护 a.persist 指针的读写，临界区内**不得**调用会同步触发
// store 事件分发的方法（st.Add 等）——Store.emit 同步回调 onPersistEvent，而后者也要
// 取 pmu，在同一 goroutine 内二次加锁不可重入会死锁。因此历史补载（st.Add）一律在
// pmu 解锁后执行（历史流 Source=history 会被 onPersistEvent 跳过，无回写风险）。

// persistOwner M9 起 writer 的所属项目打标（项目配置设计 §5.2 审计 v3 补强）：
// writer 打开时记录所属项目 id 与代际 gen；onPersistEvent 入队前比对 flow.Gen 与
// writer 的 gen（而非当前 projGen），封死 projGen++ 到 stopPersist 之间新代际流
// 落旧项目库的窗口。切换瞬间在途流的终态两个项目都不保留（拍板丢弃）。
type persistOwner struct {
	w   *persist.Writer
	gen uint64 // 所属项目代际（打开时的 projGen）
	id  string // 所属项目 ID
}

// dbPath 持久化数据库路径：M9 起每项目独立 DB（config/projects/<id>/prism.db），
// 废弃 persist.dbPath 自定义路径。调用方需持有 projMu（或处于启动单线程期）。
// 无打开项目（M11 欢迎页态）返回空串——该态无库可落，调用方必须先判断。
func (a *App) dbPath() string {
	if a.proj == nil {
		return ""
	}
	return filepath.Join(a.projDir(), settings.DefaultDBPath)
}

// initPersist 启动时按配置开启持久化（默认关闭）：打开 DB、挂订阅、加载最近历史入 store。
// 无打开项目（M11 欢迎页态）无库可落，跳过——创建/切换到项目时由 switchPersist 开启。
func (a *App) initPersist() {
	cfg := a.gcfg.Persist
	if !cfg.Enabled || a.proj == nil {
		return
	}
	w, err := persist.Open(a.dbPath(), cfg.RetainDays, cfg.MaxMB)
	if err != nil {
		log.Printf("persist: 开启持久化失败（本次运行不落盘）: %v", err)
		return
	}
	w.Start()

	a.pmu.Lock()
	first := !a.persistSubbed
	a.persist = &persistOwner{w: w, gen: a.projGen.Load(), id: a.proj.ID}
	if first {
		a.persistSubbed = true
	}
	a.pmu.Unlock()

	// 订阅与历史补载均在 pmu 外：首次开启先挂订阅再 Add（顺序无死锁风险，Add 回调
	// onPersistEvent 取 pmu 时本 goroutine 并未持锁）；历史流 Source=history 会被跳过。
	if first {
		a.st.Subscribe(a.onPersistEvent)
	}
	hist, err := w.LoadRecent(a.gcfg.MaxFlows)
	if err != nil {
		log.Printf("persist: 加载历史失败: %v", err)
		return
	}
	if len(hist) > 0 {
		for _, f := range hist {
			a.st.Add(f) // 历史流经 store 正常事件进入列表（Source=history 标记）
		}
		log.Printf("persist: 已加载 %d 条历史流量", len(hist))
	}
}

// onPersistEvent store 事件 → 持久化队列。仅终态流落盘（Writer.Enqueue 内部也判状态双保险）。
// 代际比对（M9）：flow.Gen 必须与 writer 所属代际一致才入队，跨代际流直接丢弃，
// 防止项目切换窗口期新代际流写入旧项目库。
func (a *App) onPersistEvent(ev store.Event) {
	a.pmu.Lock()
	po := a.persist
	a.pmu.Unlock()
	if po == nil {
		return
	}
	switch ev.Type {
	case "new", "update":
		// 历史流自身入 store 会产生 "new" 事件；跳过避免回写已在库的历史
		if ev.Flow != nil && ev.Flow.Source != capture.SourceHistory && ev.Flow.Gen == po.gen {
			po.w.Enqueue(ev.Flow)
		}
	}
}

// applyPersist 设置保存后热应用持久化开关/参数（调用方持 projMu）。
// 关闭：停止并关闭 writer（DB 文件保留）。开启且由关到开：重连并补载历史；
// 仅保留天数/体积上限变化：热更新 writer 参数，不重连、不重扫（避免无关设置保存也重开 DB）。
// （M9 起 DB 路径随项目固定，同项目内不存在路径变化，故不再比较路径。）
func (a *App) applyPersist(cfg settings.PersistConfig) error {
	a.pmu.Lock()
	old := a.persist
	a.pmu.Unlock()

	// 关闭：停写（DB 文件保留）
	if !cfg.Enabled {
		if old != nil {
			old.w.Close()
			a.pmu.Lock()
			a.persist = nil
			a.pmu.Unlock()
			log.Printf("persist: 已关闭落盘（数据库文件保留）")
		}
		return nil
	}

	if old != nil {
		// 同项目同一 DB：仅保留策略可能变化，热更新参数即可（无需重连）
		old.w.UpdateRetention(cfg.RetainDays, cfg.MaxMB)
		return nil
	}

	// 由关到开：打开当前项目库并补载历史
	w, err := persist.Open(a.dbPath(), cfg.RetainDays, cfg.MaxMB)
	if err != nil {
		a.pmu.Lock()
		a.persist = nil
		a.pmu.Unlock()
		return fmt.Errorf("开启持久化: %w", err)
	}
	w.Start()

	a.pmu.Lock()
	first := !a.persistSubbed
	a.persist = &persistOwner{w: w, gen: a.projGen.Load(), id: a.proj.ID}
	if first {
		a.persistSubbed = true
	}
	a.pmu.Unlock()

	if first {
		a.st.Subscribe(a.onPersistEvent)
	}

	// 补载历史（pmu 外，避免 emit 回调重入死锁）：仅补内存中不存在的 ID
	hist, err := w.LoadRecent(a.gcfg.MaxFlows)
	if err != nil {
		log.Printf("persist: 加载历史失败: %v", err)
		return nil
	}
	n := 0
	for _, f := range hist {
		if _, ok := a.st.Get(f.ID); ok {
			continue
		}
		a.st.Add(f)
		n++
	}
	if n > 0 {
		log.Printf("persist: 补载 %d 条历史流量", n)
	}
	return nil
}

// switchPersist 项目切换时换库（项目配置设计 §5.2 步骤 6）。
// 调用方持 projMu 且已完成 st.ClearAll（store 已空，无旧项目流残留）。
// 顺序：停旧 writer（队列刷盘关闭）→ 按全局 persist 开关打开新项目库 →
// pmu 内换上新 owner（打标新代际）→ pmu 外补载新项目历史入 store。
func (a *App) switchPersist() {
	a.stopPersist()

	// 无打开项目（M11 关闭项目态）：只停旧库，不开新库。
	cfg := a.gcfg.Persist
	if !cfg.Enabled || a.proj == nil {
		return
	}
	w, err := persist.Open(a.dbPath(), cfg.RetainDays, cfg.MaxMB)
	if err != nil {
		log.Printf("persist: 切换项目后开启持久化失败（本次运行不落盘）: %v", err)
		return
	}
	w.Start()

	a.pmu.Lock()
	first := !a.persistSubbed
	a.persist = &persistOwner{w: w, gen: a.projGen.Load(), id: a.proj.ID}
	if first {
		a.persistSubbed = true
	}
	a.pmu.Unlock()

	if first {
		a.st.Subscribe(a.onPersistEvent)
	}

	// 补载新项目历史（pmu 外；历史流 Source=history 会被 onPersistEvent 跳过，无回写）
	hist, err := w.LoadRecent(a.gcfg.MaxFlows)
	if err != nil {
		log.Printf("persist: 加载新项目历史失败: %v", err)
		return
	}
	for _, f := range hist {
		a.st.Add(f)
	}
	if len(hist) > 0 {
		log.Printf("persist: 已加载新项目 %d 条历史流量", len(hist))
	}
}

// currentPersist 返回当前 writer 持有者（可能为 nil）
func (a *App) currentPersist() *persistOwner {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	return a.persist
}

// persistBody 惰性回查历史流消息体（req|resp）。
// writer 未开（持久化关闭）：返回 isHistory=false，由调用方据此提示而非静默空白；
// 历史流 DB 无该 body 记录：返回 (nil,true,nil)（确实无 body）；查询/解压错误透传。
func (a *App) persistBody(flowID, kind string) (body []byte, isHistory bool, err error) {
	po := a.currentPersist()
	if po == nil {
		return nil, false, nil
	}
	body, err = po.w.LoadBody(flowID, kind)
	return body, true, err
}

// loadHistBody 历史流惰性回查消息体并缓存到 msg.Body。
// 返回 hint：非空表示"未能加载正文"的原因（持久化已关闭 / 回查失败），供 UI 提示，
// 不再静默空白；正常（含该侧确实无 body）返回空串。仅历史流且 body 不在内存时调用。
func (a *App) loadHistBody(msg *capture.Message, flowID, kind string) string {
	raw, isHist, err := a.persistBody(flowID, kind)
	if err != nil {
		log.Printf("persist: 回查历史流 %s %s 正文失败: %v", flowID, kind, err)
		return "正文回查数据库失败（数据库可能已损坏或被占用）"
	}
	if !isHist {
		return "正文未加载（持久化已关闭；重新开启后可查看历史正文）"
	}
	if raw != nil {
		msg.Body = raw // 回查成功：缓存到该消息（该历史流独占于 store，非代理热路径共享）
	}
	return ""
}

// stopPersist 退出/切换项目时刷盘并关闭 DB
func (a *App) stopPersist() {
	a.pmu.Lock()
	po := a.persist
	a.persist = nil
	a.pmu.Unlock()
	if po != nil {
		po.w.Close()
	}
}
