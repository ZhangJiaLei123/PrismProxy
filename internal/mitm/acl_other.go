//go:build !windows

package mitm

// hardenKeyACL 非 Windows 平台依赖文件创建时的 0600 权限即可
func hardenKeyACL(path string) {}
