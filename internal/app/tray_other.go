//go:build !windows

package app

// startTray 非 Windows 平台暂无托盘实现（D5 首期仅 Windows，同 session_windows.go
// 范式拆分）；其余平台经 Ctrl+C/SIGTERM 退出。
func startTray(a *App, onQuit func()) {}

// stopTray 非 Windows 平台空实现（幂等）
func stopTray() {}
