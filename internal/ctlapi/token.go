package ctlapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// newToken 生成 32 字节随机 token（64 位十六进制字符）
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见（系统熵源异常）；直接 panic 暴露环境问题
		panic("crypto/rand 不可用: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// secureEqual 常量时间字符串比较（防 token 侧信道）
func secureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
