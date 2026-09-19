//go:build prismcli

package main

import (
	"fmt"
	"os"
)

// prism-cli.exe（-tags prismcli 构建，console 子系统）只承载 CLI 模式：
// console 进程 cmd/PowerShell 会等待，输出次序与退出码天然正确（GUI exe 从 cmd
// 直接启动时提示符必然先打出，进程内无法修复——教训 55）。
// 此构建不带 wails manifest，启动 GUI 必败（教训 43），入口直接拦截引导。
func guardNonCLILaunch() {
	fmt.Fprintln(os.Stderr, "prism-cli.exe 是命令行入口，用法: prism-cli.exe cli <命令>（如 prism-cli.exe cli help）；图形界面请运行 PrismProxy.exe")
	os.Exit(2)
}
