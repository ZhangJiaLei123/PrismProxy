//go:build windows

// Package procs 枚举系统当前运行的进程名（供过滤规则进程维度选择）。
package procs

import (
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// List 返回当前系统全部运行进程的可执行文件名（小写、去重、按字母序）。
func List() ([]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))

	names := make(map[string]struct{})
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return nil, err
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		// 排除系统空闲进程（PID 0）与 System（PID 4）：二者不会产生经代理的 HTTP 流量
		if name != "" && name != "[system process]" && name != "system" {
			names[name] = struct{}{}
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break // ERROR_NO_MORE_FILES：枚举结束
		}
	}

	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}
