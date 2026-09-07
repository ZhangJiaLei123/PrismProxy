package app

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"prismproxy/internal/sysproxy"
)

// RunHeadless 无 UI 命令行模式（调试/冒烟用；吃 settings.json）。
// M8 起复用 App 服务层并启动本地控制 API（cli 子命令可连），与 GUI 模式能力对齐。
func RunHeadless(addr string, noMITM bool) {
	a := NewApp(addr, noMITM)

	// 崩溃自愈：上次接管系统代理期间被强杀 → 按备份还原（GUI/headless 共用）
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
		log.Printf("PrismProxy (headless) listening on %s mode=%s (CA page: http://%s/ca)", st.Addr, st.Mode, st.Addr)
	}

	// 本地控制 API（headless：ui:* 界面动作返回 ui=false 提示）
	a.startCtlAPI()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	// 同步清除 AutoSet 设备的 http_proxy（异步 worker 会随进程退出被截断；并行 8s 超时），
	// 与 GUI Shutdown 同口径，避免设备仍指向失效代理导致断网。
	a.clearAdbProxiesSync(adbSessionTimeout)
	a.restoreSystemProxy()
	_ = a.stopProxyNoHooks()
	a.stopCtlAPI()
	a.stopPersist() // M7：刷盘剩余队列并关闭数据库
}
