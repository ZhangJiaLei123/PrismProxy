package app

// serve.go：常驻后台模式（架构 3.0 serve 形态，由 -headless 正名演进；
// -headless 保留为兼容别名，main.go 两路均进 RunServe）。
// 代理 + ctlapi + 写库 + 系统托盘（D5）：托盘是 AI/脚本 detached 拉起 serve 后
// 用户不依赖命令行的唯一可感知/退出入口（设计 doc/架构3.0设计.md §4.4）。

import (
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	"prismproxy/internal/settings"
	"prismproxy/internal/sysproxy"
)

// RunServe 启动常驻后台并阻塞至退出信号（Ctrl+C/SIGTERM 或托盘「退出后台」），
// 退出时执行与 GUI Shutdown 同口径的完整清理管线后返回。
func RunServe(addr string, noMITM bool, staticFS fs.FS) {
	serveLog()
	a := NewApp(addr, noMITM, staticFS)

	// 崩溃自愈：上次接管系统代理期间被强杀 → 按备份还原（GUI/serve 共用）
	if healed, err := sysproxy.SelfHeal(a.backupFile()); err != nil {
		log.Printf("系统代理自愈失败: %v", err)
	} else if healed {
		log.Printf("检测到上次异常退出，已恢复原系统代理设置")
	}

	// 启动抓包（失败仅记录，控制 API 仍可用以便排查）
	if err := a.StartProxy(addr); err != nil {
		log.Printf("代理启动失败: %v", err)
	} else {
		st := a.GetProxyStatus()
		log.Printf("PrismProxy (serve) listening on %s mode=%s (CA page: http://%s/ca)", st.Addr, st.Mode, st.Addr)
	}

	// 本地控制 API（serve：ui:* 界面动作返回 ui=false 提示）
	a.startCtlAPI()

	// 系统关机/注销/重启：隐藏窗口接收 WM_ENDSESSION 同步清理（P2-9：headless 此前
	// 未挂载，常驻后台关机不还原系统代理；serve 正名后必须挂载，见设计 §4.4/§13 W1）
	go watchSessionEnd(a.onSessionEnd)

	// 系统托盘（D5）：状态展示 + 打开 GUI（过渡期置灰）+ 退出后台
	var quitOnce sync.Once
	quit := make(chan struct{})
	startTray(a, func() { quitOnce.Do(func() { close(quit) }) })

	// 退出信号：终端手动运行时 Ctrl+C/SIGTERM，或托盘「退出后台」
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-quit:
	}

	log.Printf("shutting down")
	stopTray() // 先移除托盘图标（用户即时反馈），清理（秒级）完成后进程退出
	// 完整清理管线（与 GUI Shutdown 同口径）：同步清除 AutoSet 设备代理 → 还原系统代理
	// → 停代理（不再触发清除挂钩）→ 停 ctlapi（删 endpoint 文件）→ 刷盘关库 → 关归档库
	a.clearAdbProxiesSync(adbSessionTimeout)
	a.restoreSystemProxy()
	_ = a.stopProxyNoHooks()
	a.stopCtlAPI()
	a.stopPersist()
	a.closeArchive()
}

// serveLog serve 无窗常驻（可能被 detached 拉起，stderr 无宿主）：日志双写
// config/logs/serve.log（超过 8MB 轮换为 .old）+ stderr（终端手动运行时可见）。
func serveLog() {
	p := filepath.Join(settings.DefaultConfigDir(), "logs", "serve.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 8<<20 {
		_ = os.Rename(p, p+".old") // 轮换失败忽略：继续追加写
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return // 目录建不出来（只读盘等）保持纯 stderr
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

// trayUpdate serve 托盘状态刷新回调（startTray 注入；publishStatus/emitProjectChanged
// 触发）。atomic.Pointer：托盘 goroutine 注册与任意发布 goroutine 触发并发；nil = 无托盘
//（GUI 模式恒 nil，notifyTray 零开销）。
var trayUpdate atomic.Pointer[func()]

// setTrayUpdater 注册托盘状态刷新回调（当前仅 serve 托盘 onReady 时注册一次）
func (a *App) setTrayUpdater(fn func()) {
	trayUpdate.Store(&fn)
}

// notifyTray 状态变化时通知托盘刷新（未注册回调时零开销）
func (a *App) notifyTray() {
	if fn := trayUpdate.Load(); fn != nil {
		(*fn)()
	}
}
