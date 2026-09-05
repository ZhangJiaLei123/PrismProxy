package ctlapi

import (
	"log"
	"os"
	"os/exec"
)

// hardenFileACL 收紧 endpoint 文件 ACL：去继承，仅当前用户完全控制（与 CA 私钥同策略）
func hardenFileACL(path string) {
	user := os.Getenv("USERNAME")
	if user == "" {
		return
	}
	// icacls <file> /inheritance:r /grant:r "<user>:F"
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", user+":F")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("warn: harden endpoint file acl failed: %v (%s)", err, out)
	}
}
