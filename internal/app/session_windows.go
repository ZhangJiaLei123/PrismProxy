//go:build windows

package app

import (
	"runtime"
	"syscall"
	"unsafe"
)

// 系统关机/注销时，主窗口（Wails winc）不处理 WM_QUERYENDSESSION/WM_ENDSESSION，
// OnShutdown 钩子不会触发。此处在独立线程上建一个隐藏顶层窗口接收 WM_ENDSESSION：
// 会话确实结束（wParam=TRUE）时同步执行系统代理还原，
// 避免关机前注册表残留指向本工具、下次开机未启动程序时断网。
//
// 注意：message-only 窗口（HWND_MESSAGE）收不到 WM_QUERYENDSESSION，必须建普通顶层窗口。
const (
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	cwUseDefault      = 0x80000000
)

// winClass 窗口类名（仅进程内注册，任意唯一串）
var winClass = syscall.StringToUTF16Ptr("PrismProxySessionWnd")

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procDefWindow   = user32.NewProc("DefWindowProcW")
	procRegister    = user32.NewProc("RegisterClassExW")
	procCreateWin   = user32.NewProc("CreateWindowExW")
	procGetMsg      = user32.NewProc("GetMessageW")
	procTranslate   = user32.NewProc("TranslateMessage")
	procDispatch    = user32.NewProc("DispatchMessageW")
	procGetModule   = kernel32.NewProc("GetModuleHandleW")
)

// wndClassExW WNDCLASSEXW
type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// winMsg MSG
type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// watchSessionEnd 在独立线程上创建隐藏窗口并跑消息循环；收到 WM_ENDSESSION(TRUE) 时调 onEnd。
// 失败静默返回（关机清理是尽力而为，不影响主程序）。
func watchSessionEnd(onEnd func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hinst, _, _ := procGetModule.Call(0)

	cb := syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
		switch msg {
		case wmQueryEndSession:
			return 1 // 允许关机/注销
		case wmEndSession:
			if wparam == 1 { // TRUE = 会话确实结束
				onEnd()
			}
			return 0
		}
		ret, _, _ := procDefWindow.Call(hwnd, uintptr(msg), wparam, lparam)
		return ret
	})

	wc := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		WndProc:   cb,
		Instance:  hinst,
		ClassName: winClass,
	}
	// 注册失败（如内存不足）不致命：CreateWindowExW 会随之失败，直接返回
	procRegister.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := procCreateWin.Call(
		0,                       // dwExStyle
		uintptr(unsafe.Pointer(winClass)),
		uintptr(unsafe.Pointer(winClass)),
		0,                       // WS_OVERLAPPED（不可见）
		cwUseDefault, cwUseDefault, cwUseDefault, cwUseDefault,
		0, 0, hinst, 0,
	)
	if hwnd == 0 {
		return
	}

	var m winMsg
	for {
		r, _, _ := procGetMsg.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // WM_QUIT(-1 语义) 或错误
			return
		}
		procTranslate.Call(uintptr(unsafe.Pointer(&m)))
		procDispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
}
