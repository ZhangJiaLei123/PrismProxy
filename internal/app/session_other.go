//go:build !windows

package app

// watchSessionEnd 非 Windows 平台空实现（关机/注销会话消息是 Win32 概念）
func watchSessionEnd(onEnd func()) {}
