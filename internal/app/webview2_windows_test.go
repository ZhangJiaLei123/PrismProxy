//go:build windows

package app

import "testing"

// 开发机冒烟：本机 GUI 可运行即装有 WebView2，自检必须为 true
func TestWebView2Installed(t *testing.T) {
	if !webview2Installed() {
		t.Fatal("webview2Installed() = false，但本机装有 WebView2 运行时（注册表 pv 探测失效？）")
	}
}
