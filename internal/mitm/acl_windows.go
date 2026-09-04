package mitm

import (
	"log"
	"os"
	"os/exec"
)

// hardenKeyACL 收紧私钥文件 ACL：去继承，仅当前用户完全控制（方案 §4.2）
func hardenKeyACL(path string) {
	user := os.Getenv("USERNAME")
	if user == "" {
		return
	}
	// icacls <file> /inheritance:r /grant:r "<user>:F"
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", user+":F")
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("warn: harden key acl failed: %v (%s)", err, out)
	}
}
