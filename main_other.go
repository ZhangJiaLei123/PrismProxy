//go:build !windows

package main

// 非 Windows 平台 CLI 模式无需控制台附着（可执行文件本就是控制台子系统）。
func attachParentConsole() {}
