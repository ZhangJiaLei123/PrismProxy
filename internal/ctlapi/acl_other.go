//go:build !windows

package ctlapi

// hardenFileACL 非 Windows 平台：文件已以 0600 权限写入，无需额外处理
func hardenFileACL(path string) {}
