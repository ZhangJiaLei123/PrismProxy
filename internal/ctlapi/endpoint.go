package ctlapi

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Endpoint 控制 API 连接信息（实例启动后落盘，cli 子进程自动发现）。
// 落盘于 exe 同级 config 目录（便携模式，与 settings.json 同目录）。
type Endpoint struct {
	Addr  string `json:"addr"`  // 实际监听地址（127.0.0.1:9595）
	Token string `json:"token"` // Bearer token
}

// endpointFileName endpoint 文件名
const endpointFileName = "ctl-endpoint.json"

// EndpointFile 返回 configDir 下的 endpoint 文件路径
func EndpointFile(configDir string) string {
	return filepath.Join(configDir, endpointFileName)
}

// WriteEndpoint 原子写入 endpoint 文件并收紧 ACL（仅当前用户可读，防本机其他用户窃取 token）
func WriteEndpoint(path string, ep Endpoint) error {
	if path == "" {
		// 测试/无落盘场景：空路径会让 tmp := path+".tmp" 写到当前工作目录，显式拒绝
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	hardenFileACL(path)
	return nil
}

// ReadEndpoint 读取 endpoint 文件（不存在返回错误，cli 据此提示"实例未运行"）
func ReadEndpoint(path string) (Endpoint, error) {
	var ep Endpoint
	data, err := os.ReadFile(path)
	if err != nil {
		return ep, err
	}
	if err := json.Unmarshal(data, &ep); err != nil {
		return ep, err
	}
	return ep, nil
}

// RemoveEndpoint 删除 endpoint 文件（实例退出时；文件不存在视为成功）
func RemoveEndpoint(path string) {
	_ = os.Remove(path)
}
