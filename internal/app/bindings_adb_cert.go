package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"prismproxy/internal/mitm"
	"prismproxy/internal/settings"
)

// ---------- ADB 安卓系统 CA 注入（root 设备/模拟器）----------
//
// Android 7+ App 默认只信任系统 CA，且 LDPlayer14（Android 14）实测用户 CA 合并不生效。
// 既有可行方案：把 Prism CA 加进 /data/local/tmp/prism-cacerts 后 bind-mount 覆盖
// /apex/com.android.conscrypt/cacerts（APEX 只读，无法 remount）。bind-mount 是运行时
// 内核挂载表，**设备重启即丢失**，需重做。本文件把该流程收敛为 UI 一键操作；证书源目录
// 持久存放于 /data/local/tmp/prism-cacerts，重做时只重新挂载+同步 zygote，秒级完成。

const (
	// adbCertDir 设备侧持久注入目录（/data/local/tmp 对 shell 用户可读写、重启不丢）。
	adbCertDir = "/data/local/tmp/prism-cacerts"
	// adbCertPem 设备侧 Prism CA PEM 暂存路径（adb push 落地点）。
	adbCertPem = "/data/local/tmp/prism-ca.pem"
	// adbCertScript 设备侧注入脚本路径。
	adbCertScript = "/data/local/tmp/prism-ca-inject.sh"
	// adbCertStatusScript 设备侧只读状态探测脚本路径。
	adbCertStatusScript = "/data/local/tmp/prism-ca-status.sh"
	// adbCertOK 脚本成功标记（避免只凭退出码判断，部分 toybox su 对退出码透传不稳定）。
	adbCertOK = "PRISMCERT_OK"
	// adbCertTimeout 整条注入链（两次 push + 脚本执行）超时。
	adbCertTimeout = 40 * time.Second
)

// androidCertInjectScript 设备侧注入脚本（# 注释均不依赖中文 locale，避免 toybox 解析问题）。
// 参数 $1=证书 hash（不含 .0）。流程：
//  1. 惰性克隆系统 cacerts 到持久目录（首次），写入 CA 并修权限/属主/SELinux 标签；
//  2. bind-mount 覆盖 conscrypt cacerts（已挂载则跳过，幂等）；
//  3. nsenter 同步 init/zygote 挂载命名空间，使已运行 App 也能读到新 CA。
const androidCertInjectScript = `#!/system/bin/sh
SRC=` + adbCertDir + `
DST=/apex/com.android.conscrypt/cacerts
HASH="$1"
[ -n "$HASH" ] || { echo "missing hash"; exit 1; }

umask 022
if [ ! -d "$SRC" ]; then
  mkdir -p "$SRC" || exit 1
  cp "$DST"/* "$SRC"/ 2>/dev/null
fi
cp /data/local/tmp/prism-ca.pem "$SRC/$HASH.0" || exit 1
chmod 644 "$SRC/$HASH.0"
chown root:root "$SRC/$HASH.0" 2>/dev/null
chcon u:object_r:system_security_cacerts_file:s0 "$SRC/$HASH.0" 2>/dev/null

mount | grep -F "on $DST " >/dev/null 2>&1 || mount --bind "$SRC" "$DST" || { echo "bind mount failed"; exit 1; }
[ -f "$DST/$HASH.0" ] || { echo "cert not visible after mount"; exit 1; }

for pid in 1 $(pgrep zygote) $(pgrep zygote64); do
  nsenter --mount=/proc/$pid/ns/mnt -- mount --bind "$SRC" "$DST" 2>/dev/null
done

# 权威校验：脚本所在（adb shell）命名空间与 init(pid 1) 命名空间都必须看到证书。
# 仅在 shell 命名空间自检会漏掉 adbd 重启/挂载传播窗口，导致新 shell 短时间误报缺失。
[ -f "$DST/$HASH.0" ] || { echo "cert missing in shell ns"; exit 1; }
if nsenter --mount=/proc/1/ns/mnt -- test -f "$DST/$HASH.0" 2>/dev/null; then
  echo ` + adbCertOK + `
else
  echo "cert missing in init ns"; exit 1
fi
`

// androidCertStatusScript 设备侧只读探测脚本（参数 $1=hash，不含 .0）。
// 必须 push 后以「单参数」执行：Windows adb 用 `shell sh -c <含分号/空格的多参数>` 时，
// toybox sh 会把参数二次拆分（实测 `a;b` 被拆成两条、if/then 直接语法错），导致误判。
// 判断顺序：
//  1. 当前进程（adb shell）命名空间可见 → 直接已注入；
//  2. 否则在 root 下用 nsenter 进 init(pid 1)/zygote 命名空间复核——App 真正继承的挂载根，
//     即使 adbd 处于私有挂载视图（shell 看不到）也能给出权威结论；
//  3. 都不可见（或非 root 被 SELinux 拒绝 nsenter）→ 未注入。
const androidCertStatusScript = `#!/system/bin/sh
F=/apex/com.android.conscrypt/cacerts/"$1".0
[ -n "$1" ] || { echo "PRISM_CERT_ERROR missing hash"; exit 1; }
if [ -f "$F" ]; then
  echo PRISM_CERT_INSTALLED
  exit 0
fi
seen=0
for pid in 1 $(pgrep zygote) $(pgrep zygote64); do
  if nsenter --mount=/proc/$pid/ns/mnt -- test -f "$F" 2>/dev/null; then seen=1; fi
done
if [ "$seen" = "1" ]; then
  echo PRISM_CERT_INSTALLED
else
  echo PRISM_CERT_MISSING
fi
`

// AdbInstallSystemCert 把 PrismProxy 根 CA 注入指定设备的系统 CA 存储（需 root/可 adb root）。
// 设备重启后 bind-mount 失效，重复执行本操作即可（证书源目录持久化，重做很快）。
// 返回给前端展示的结果说明。
func (a *App) AdbInstallSystemCert(adbPath, serial string) (string, error) {
	caPEM, hash, err := a.currentCAPEMAndHash()
	if err != nil {
		return "", err
	}

	// 本地临时目录放 PEM + 脚本（push 需要真实文件）；用完即删。
	tmpDir, err := os.MkdirTemp("", "prism-adb-cert-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	pemLocal := filepath.Join(tmpDir, "prism-ca.pem")
	scriptLocal := filepath.Join(tmpDir, "prism-ca-inject.sh")
	if err := os.WriteFile(pemLocal, caPEM, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(scriptLocal, []byte(androidCertInjectScript), 0o644); err != nil {
		return "", err
	}

	run := func(args ...string) (string, error) {
		return a.runAdbTimeout(adbCertTimeout, adbPath, serial, args...)
	}

	// push 不要求 root（/data/local/tmp shell 可写）；脚本内挂载才需要 root。
	if _, err := run("push", pemLocal, adbCertPem); err != nil {
		return "", fmt.Errorf("推送 CA 失败: %w", err)
	}
	if _, err := run("push", scriptLocal, adbCertScript); err != nil {
		return "", fmt.Errorf("推送注入脚本失败: %w", err)
	}

	// 首选 adb root 后直接执行（雷电 ro.debuggable=1 支持；会短暂重启 adbd，用宽松超时）。
	out, err := run("root")
	rooted := err == nil && !strings.Contains(strings.ToLower(out), "cannot run as root")
	if !rooted {
		// 非 root 但有 root 管理器：尝试 shell 内 su 0 提权执行。
		out, err = run("shell", "su", "0", "sh", adbCertScript, hash)
	} else {
		// adb root 会重启 adbd，等待设备回来后再执行脚本。
		if _, werr := run("wait-for-device"); werr != nil {
			return "", fmt.Errorf("adb root 后等待设备失败: %w", werr)
		}
		time.Sleep(time.Second)
		out, err = run("shell", "sh", adbCertScript, hash)
	}
	if err != nil {
		return "", fmt.Errorf("执行系统证书注入失败（需 root 设备/模拟器）: %w", err)
	}
	if !strings.Contains(out, adbCertOK) {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = "设备未授予 root（adb root / su 均不可用）"
		}
		return "", fmt.Errorf("系统证书注入失败: %s", detail)
	}

	// 脚本末尾已在 init(pid 1)/zygote 挂载命名空间权威校验通过：pid 1 是全部 App 进程的
	// 挂载根，那里可见即代表对应用生效，因此脚本输出 OK 即可判定成功。
	// 注意：注入会把 adbd 切到 root；状态查询 AdbSystemCertStatus 独立用「push 脚本」探测，
	// 不复用 `sh -c 多参数`（Windows adb 下会被 toybox 拆分 argv 导致误判）。
	return fmt.Sprintf("系统证书已注入（%s.0）。设备重启后挂载会失效，重新点本按钮即可恢复；已运行的 App 如仍报证书错误请强制停止后重开", hash), nil
}

// AdbSystemCertStatus 查询指定设备的系统 CA 注入状态：
// 返回 "installed"（bind-mount 存活且证书可见）/ "missing"（需注入或重启后丢失）；
// adb 不可用/设备离线等命令级失败返回错误，由调用方按「未知」处理，避免离线误报成未注入。
func (a *App) AdbSystemCertStatus(adbPath, serial string) (string, error) {
	_, hash, err := a.currentCAPEMAndHash()
	if err != nil {
		return "", err
	}

	// push 只读探测脚本（/data/local/tmp shell 可写），随后以单参数执行，规避 argv 拆分。
	tmpDir, err := os.MkdirTemp("", "prism-adb-status-*")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	scriptLocal := filepath.Join(tmpDir, "prism-ca-status.sh")
	if err := os.WriteFile(scriptLocal, []byte(androidCertStatusScript), 0o644); err != nil {
		return "", err
	}
	run := func(args ...string) (string, error) {
		return a.runAdbTimeout(adbCertTimeout, adbPath, serial, args...)
	}
	if _, err := run("push", scriptLocal, adbCertStatusScript); err != nil {
		return "", fmt.Errorf("推送状态探测脚本失败: %w", err)
	}

	// 第一轮：当前身份（可能已因上次注入处于 root）直接探测。
	out, err := run("shell", "sh", adbCertStatusScript, hash)
	if err != nil {
		return "", err
	}
	if strings.Contains(out, "PRISM_CERT_INSTALLED") {
		return "installed", nil
	}

	// 第二轮：当前 shell 看不到（adbd 私有挂载视图，或仍是 shell 用户无法 nsenter init），
	// best-effort 提权到 root 后用 init/zygote 命名空间复核；非 root 设备会失败，按缺失返回。
	rootOut, rerr := run("root")
	rooted := rerr == nil && !strings.Contains(strings.ToLower(rootOut), "cannot run as root")
	if !rooted {
		if strings.Contains(out, "PRISM_CERT_MISSING") {
			return "missing", nil
		}
		return "", fmt.Errorf("无法识别的证书状态输出: %s", strings.TrimSpace(out))
	}
	if _, werr := run("wait-for-device"); werr != nil {
		return "", fmt.Errorf("adb root 后等待设备失败: %w", werr)
	}
	time.Sleep(1500 * time.Millisecond)
	out2, err := run("shell", "sh", adbCertStatusScript, hash)
	if err != nil {
		return "", err
	}
	if strings.Contains(out2, "PRISM_CERT_INSTALLED") {
		return "installed", nil
	}
	if strings.Contains(out2, "PRISM_CERT_MISSING") {
		return "missing", nil
	}
	return "", fmt.Errorf("无法识别的证书状态输出: %s", strings.TrimSpace(out2))
}

// bestEffortEnsureSystemCert 自动挂钩用：先只读探测注入是否存活，缺失才执行注入；
// 任何失败只记日志（非 root/设备未就绪不应让自动设置代理的流程报错）。
func (a *App) bestEffortEnsureSystemCert(device settings.ADBDevice) {
	status, err := a.AdbSystemCertStatus(device.Path, device.Serial)
	if err != nil {
		runtimeLogf(a, "查询系统证书状态失败（%s）: %v", device.Name, err)
		return
	}
	if status == "installed" {
		return
	}
	if _, err := a.AdbInstallSystemCert(device.Path, device.Serial); err != nil {
		runtimeLogf(a, "自动注入系统证书失败（%s，可在设置→ADB 手动重试）: %v", device.Name, err)
	} else {
		runtimeLogf(a, "已自动为 %s 注入系统 CA（重启后会自动重做）", device.Name)
	}
}

// currentCAPEMAndHash 返回当前生效 CA 的 PEM 与 subject_hash_old。
// 优先用内存中代理已加载的 CA（代理运行中）；否则从磁盘配置读取（代理未启动时也要能注入）。
func (a *App) currentCAPEMAndHash() (pem []byte, hash string, err error) {
	a.mu.Lock()
	ca := a.ca
	caDir := filepath.Join(a.cfgDir, "ca")
	a.mu.Unlock()

	if ca != nil {
		return ca.CertPEM, ca.SubjectHashOld(), nil
	}
	loaded, lerr := mitm.LoadOrCreateCA(caDir)
	if lerr != nil {
		return nil, "", fmt.Errorf("加载根 CA 失败（请先成功启动一次代理）: %w", lerr)
	}
	return loaded.CertPEM, loaded.SubjectHashOld(), nil
}
