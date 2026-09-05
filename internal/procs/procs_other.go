//go:build !windows

package procs

// List 非 Windows 平台暂不支持进程枚举（项目仅发布 Windows）。
func List() ([]string, error) {
	return nil, nil
}
