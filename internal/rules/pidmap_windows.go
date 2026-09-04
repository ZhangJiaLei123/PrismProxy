package rules

import (
	"encoding/binary"
	"path/filepath"
	"syscall"
	"unsafe"

	"prismproxy/internal/capture"
)

// 纯 syscall + LazyDLL 直调系统 DLL，M1 零外部依赖

var (
	iphlpapi                = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")

	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCreateToolhelp32Snapshot   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = kernel32.NewProc("Process32FirstW")
	procProcess32NextW             = kernel32.NewProc("Process32NextW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
)

const (
	afInet  = 2
	afInet6 = 23

	tcpTableOwnerPIDAll = 5

	processQueryLimitedInformation = 0x1000

	th32csSnapProcess  = 0x00000002
	invalidHandleValue = ^uintptr(0)
	maxPath            = 260
)

// FindProcess 在 TCP 表中查找「本地端口 = clientPort 且远端端口 = serverPort」的连接所属进程。
// serverPort 传 0 表示不校验远端端口。IPv4/IPv6 双表都查。
// 权限不足取不到进程路径时降级：仅返回进程名（Path 为空）。
func FindProcess(clientPort, serverPort uint16) *capture.ProcessInfo {
	pid, ok := findOwnerPID(clientPort, serverPort, afInet)
	if !ok {
		pid, ok = findOwnerPID(clientPort, serverPort, afInet6)
	}
	if !ok {
		return nil
	}

	info := &capture.ProcessInfo{PID: pid}
	if path, err := processImagePath(pid); err == nil {
		info.Path = path
		info.Name = filepath.Base(path)
	} else {
		// 权限降级：目标进程以管理员/服务运行时 OpenProcess 失败，退回 Toolhelp 快照取进程名
		info.Name = processNameFromSnapshot(pid)
	}
	return info
}

func findOwnerPID(clientPort, serverPort uint16, af uint32) (uint32, bool) {
	buf, err := tcpTable(af)
	if err != nil || len(buf) < 4 {
		return 0, false
	}
	n := binary.LittleEndian.Uint32(buf)
	rows := buf[4:]

	rowSize := 24 // MIB_TCPROW_OWNER_PID：6 个 DWORD
	if af == afInet6 {
		rowSize = 56 // MIB_TCP6ROW_OWNER_PID
	}
	for i := 0; i < int(n); i++ {
		if (i+1)*rowSize > len(rows) {
			break
		}
		row := rows[i*rowSize:]
		var localPort, remotePort, pid uint32
		if af == afInet {
			localPort = binary.LittleEndian.Uint32(row[8:])
			remotePort = binary.LittleEndian.Uint32(row[16:])
			pid = binary.LittleEndian.Uint32(row[20:])
		} else {
			localPort = binary.LittleEndian.Uint32(row[20:])
			remotePort = binary.LittleEndian.Uint32(row[44:])
			pid = binary.LittleEndian.Uint32(row[52:])
		}
		if decodePort(localPort) == clientPort && (serverPort == 0 || decodePort(remotePort) == serverPort) {
			return pid, true
		}
	}
	return 0, false
}

func tcpTable(af uint32) ([]byte, error) {
	var size uint32
	// 第一次调用取所需缓冲大小（预期返回 ERROR_INSUFFICIENT_BUFFER）
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDAll, 0)
	if size == 0 {
		return nil, syscall.EINVAL
	}
	buf := make([]byte, size)
	r1, _, err := procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(af),
		tcpTableOwnerPIDAll,
		0,
	)
	if r1 != 0 {
		return nil, err
	}
	return buf, nil
}

// decodePort 端口以网络字节序存放在 DWORD 低 16 位中，需交换两个字节
func decodePort(v uint32) uint16 {
	return uint16(v>>8 | (v&0xff)<<8)
}

func processImagePath(pid uint32) (string, error) {
	h, _, err := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return "", err
	}
	defer procCloseHandle.Call(h)

	var name [maxPath]uint16
	size := uint32(maxPath)
	r1, _, err := procQueryFullProcessImageNameW.Call(
		h, 0,
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 {
		return "", err
	}
	return syscall.UTF16ToString(name[:size]), nil
}

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [maxPath]uint16
}

func processNameFromSnapshot(pid uint32) string {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == invalidHandleValue {
		return ""
	}
	defer procCloseHandle.Call(snap)

	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	r1, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	for r1 != 0 {
		if pe.ProcessID == pid {
			return syscall.UTF16ToString(pe.ExeFile[:])
		}
		r1, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	}
	return ""
}
