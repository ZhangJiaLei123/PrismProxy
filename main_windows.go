//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// GUI 子系统（wails 默认 -H=windowsgui）exe 的 CLI 模式输出修复。
//
// 实测行为（PRISM_CLI_DEBUG=1 诊断日志验证）：
//   - PowerShell/AI 工具/重定向：句柄是管道/文件，直接可用；
//   - cmd/终端直接启动：GUI 子进程未附着控制台（consolewnd=0/procs=0），
//     GetStdHandle 返回 NULL，所有标准输出静默丢失；提示符也不等待 GUI
//     子进程，因此接通输出后需先换行，避免输出与提示符拼在同一行。
//
// 策略：0 字节 WriteFile 探测现有句柄是否真正可用；不可用则直接打开
// CONOUT$/CONIN$ 接管当前（继承的）控制台；若确实没有附着控制台
// （双击启动等），FreeConsole + AttachConsole(ATTACH_PARENT_PROCESS) 后再试。
func attachParentConsole() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	getStd := k32.NewProc("GetStdHandle")
	writeFile := k32.NewProc("WriteFile")
	freeConsole := k32.NewProc("FreeConsole")
	attachConsole := k32.NewProc("AttachConsole")

	const (
		attachParentProcess = ^uintptr(0)  // ATTACH_PARENT_PROCESS = (DWORD)-1
		stdInputHandle      = ^uintptr(9)  // STD_INPUT_HANDLE  = -10
		stdOutputHandle     = ^uintptr(10) // STD_OUTPUT_HANDLE = -11
		stdErrorHandle      = ^uintptr(11) // STD_ERROR_HANDLE  = -12
	)

	getH := func(which uintptr) uintptr {
		r1, _, _ := getStd.Call(which)
		if r1 == ^uintptr(0) { // INVALID_HANDLE_VALUE
			return 0
		}
		return r1
	}
	// 0 字节写入探针：管道/文件句柄成功；NULL 句柄直接判失败
	writable := func(h uintptr) bool {
		if h == 0 {
			return false
		}
		var n uint32
		r, _, _ := writeFile.Call(h, 0, 0, uintptr(unsafe.Pointer(&n)), 0)
		return r != 0
	}
	// 把 CONOUT$/CONIN$ 接到缺失的标准流；GetConsoleMode 确认进程确实附着了
	// 可用控制台（避免接到无主句柄）。返回是否完成了任一接线。
	wire := func(fixOut, fixErr bool) bool {
		getMode := k32.NewProc("GetConsoleMode")
		wired := false
		use := func(name, label string, target **os.File) {
			f, err := openConsole(name)
			if err != nil {
				cliDebug(fmt.Sprintf("%s open fail: %v (errno=%d)", name, err, int(err.(syscall.Errno))))
				return
			}
			if r, _, _ := getMode.Call(f.Fd()); r != 0 {
				*target = f
				wired = true
				cliDebug(label + "<=" + name)
			} else {
				f.Close()
				cliDebug(name + " no console attached")
			}
		}
		if fixOut {
			use("CONOUT$", "stdout", &os.Stdout)
		}
		if fixErr {
			use("CONOUT$", "stderr", &os.Stderr)
		}
		if getH(stdInputHandle) == 0 {
			use("CONIN$", "stdin", &os.Stdin)
		}
		return wired
	}

	outH, errH := getH(stdOutputHandle), getH(stdErrorHandle)
	outOK, errOK := writable(outH), writable(errH)
	cliDebug(fmt.Sprintf("stdhandle out=%#x(err=%v) err=%#x(err=%v)", outH, !outOK, errH, !errOK))
	diagnoseConsole(k32)

	if outOK && errOK {
		return // 管道/重定向场景，句柄可直接用
	}
	// 句柄为 NULL/不可写：先尝试直接打开 CONOUT$ 接管屏幕缓冲区
	// （cmd 不等待 GUI 子进程，提示符已先打印，输出前先换行避免与提示符拼在同一行）
	if wire(!outOK, !errOK) {
		if !outOK {
			fmt.Fprintln(os.Stdout)
		}
		return
	}
	// 没有附着控制台（双击启动、无控制台宿主）：挂回父控制台后再接一次
	freeConsole.Call()
	r1, _, callErr := attachConsole.Call(attachParentProcess)
	cliDebug(fmt.Sprintf("free+attach r1=%d lasterr=%v", r1, callErr))
	if r1 != 0 && wire(!outOK, !errOK) {
		if !outOK {
			fmt.Fprintln(os.Stdout)
		}
	}
}

func openConsole(name string) (*os.File, error) {
	h, err := syscall.CreateFile(
		syscall.StringToUTF16Ptr(name),
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}

// diagnoseConsole 记录控制台附着状态的原始事实（PRISM_CLI_DEBUG=1 时）
func diagnoseConsole(k32 *syscall.LazyDLL) {
	hwnd, _, _ := k32.NewProc("GetConsoleWindow").Call()
	var ids [4]uint32
	n, _, _ := k32.NewProc("GetConsoleProcessList").Call(
		uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	cliDebug(fmt.Sprintf("consolewnd=%#x consoleprocs=%d", hwnd, n))
}

// cliDebug：PRISM_CLI_DEBUG=1 时把附着过程追加写到 %TEMP%\prism_cli_attach.log，用于诊断输出丢失
func cliDebug(msg string) {
	if os.Getenv("PRISM_CLI_DEBUG") != "1" {
		return
	}
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "prism_cli_attach.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[cli-attach] %s\n", msg)
}
