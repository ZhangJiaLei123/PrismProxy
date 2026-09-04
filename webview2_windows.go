//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// webview2ClientGUID WebView2 运行时（Evergreen）在 EdgeUpdate 下的固定客户端 GUID
const webview2ClientGUID = "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

// webview2BootstrapperURL 微软官方常青引导程序（在线安装器）下载链接
const webview2BootstrapperURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// webview2Installed 检测 WebView2 运行时是否已安装。
// 安装后会在 EdgeUpdate\Clients\{GUID} 写入 pv（版本号）；
// HKLM 64/32 位两处视图与 HKCU 任一命中即视为已安装（涵盖全机/单用户安装）。
func webview2Installed() bool {
	roots := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webview2ClientGUID},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webview2ClientGUID},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webview2ClientGUID},
	}
	for _, r := range roots {
		k, err := registry.OpenKey(r.root, r.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && pv != "" && pv != "0.0.0.0" {
			return true
		}
	}
	return false
}

// ensureWebView2 启动自检：已安装返回 true；缺失时弹框引导，用户确认后打开官方下载页，返回 false 由调用方退出
func ensureWebView2() bool {
	if webview2Installed() {
		return true
	}
	const (
		mbOKCancel    = 0x0001
		mbIconWarning = 0x0030
		idOK          = 1
	)
	text := "未检测到 Microsoft Edge WebView2 运行时，PrismProxy 的界面依赖它才能显示。\n\n" +
		"「确定」打开官方下载页，下载安装后重新启动本程序即可；「取消」直接退出。"
	if messageBoxW(text, "PrismProxy - 缺少 WebView2 运行时", mbOKCancel|mbIconWarning) == idOK {
		// rundll32 方式打开默认浏览器，避免 cmd 黑窗闪烁
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", webview2BootstrapperURL).Start()
	}
	return false
}

var procMessageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

// messageBoxW 无窗口阶段的原生弹框（wails 窗口尚未创建，不能用 runtime.MessageDialog）
func messageBoxW(text, caption string, style uint32) uintptr {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(caption)
	ret, _, _ := procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)),
		uintptr(style))
	return ret
}
