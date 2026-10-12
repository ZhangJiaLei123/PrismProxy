//go:build windows

package app

import (
	_ "embed"

	"fyne.io/systray"
)

// trayicon.ico 多尺寸图标（16/24/32/48/64/128/256，PNG-in-ICO），由 build/appicon.png
// 一次性转换生成。Windows 托盘 SetIcon 经 LoadImage 加载，必须是 .ico（不能直接用 PNG）。
//
//go:embed trayicon.ico
var trayIcon []byte

// startTray 创建系统托盘并注册菜单（独立 goroutine 跑 systray 消息循环，不阻塞
// serve 主流程）。onQuit 在用户点「退出后台」时由托盘 goroutine 调用一次。
func startTray(a *App, onQuit func()) {
	go func() {
		systray.Run(func() {
			systray.SetIcon(trayIcon)
			systray.SetTooltip("PrismProxy serve")

			mStatus := systray.AddMenuItem("", "")
			mStatus.Disable()
			systray.AddSeparator()
			// 过渡期置灰（W1–W4）：无参 exe 仍是 2.x 同进程形态，点开即与 serve
			// 双后端并存写同一库；智能拉起（W6）落地后启用（设计 §4.4）。
			mOpen := systray.AddMenuItem("打开 GUI", "后续版本开放（当前过渡期请手动双击 exe 启动 GUI）")
			mOpen.Disable()
			systray.AddSeparator()
			mQuit := systray.AddMenuItem("退出后台", "停止代理并清理：还原系统代理、清除设备代理、关闭数据库")

			upd := func() {
				line := a.trayStateNow().line()
				mStatus.SetTitle(line)
				systray.SetTooltip("PrismProxy serve\n" + line)
			}
			upd() // onReady 主动刷一帧（此前 publishStatus 触发的回调早于 ready，丢失无碍）
			a.setTrayUpdater(upd)

			go func() {
				for range mQuit.ClickedCh {
					onQuit()
					return
				}
			}()
		}, nil)
	}()
}

// stopTray 移除托盘图标并结束消息循环（幂等：未启动/已退出时安全，Quit 内部 sync.Once）
func stopTray() {
	systray.Quit()
}
