//go:build !windows

package main

// 非 Windows 平台无 WebView2 概念，自检恒通过
func ensureWebView2() bool { return true }
