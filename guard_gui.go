//go:build !prismcli

package main

// 正常构建（wails build / go build）：GUI 分支无需守卫。
func guardNonCLILaunch() {}
