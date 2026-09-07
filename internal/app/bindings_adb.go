package app

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/settings"
)

// ---------- ADB 安卓模拟器/真机自动代理 Bindings ----------
//
// 设备侧 HTTP 代理通过 `adb shell settings put global http_proxy <host>:<port>` 写入，
// 清除用 `... http_proxy :0`。host 为设备访问宿主机 PrismProxy 的 IP（雷电 NAT 默认
// 172.16.1.2），端口取代理实际监听端口。每条配置仅保存「名称 + adb 路径」，
// AutoSet=true 的配置在代理启动时自动设置、停止时自动清除。

// adbTimeout 单条 adb 命令超时：adb server 首次启动/设备连接可能较慢。
const adbTimeout = 20 * time.Second

// PickAdbPath 弹出文件选择对话框选取 adb 可执行文件；用户取消返回空串（无错误）。
func (a *App) PickAdbPath() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("当前模式不支持文件选择")
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 adb 可执行文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "可执行文件 (*.exe)", Pattern: "*.exe"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

// AdbTest 测试 adb 路径并返回已连接设备信息（`adb devices` 解析）。
func (a *App) AdbTest(adbPath string) (string, error) {
	out, err := a.runAdb(adbPath, "devices")
	if err != nil {
		return "", err
	}
	var devs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		// 形如 "emulator-5554\tdevice" 或 "offline/unauthorized"
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			devs = append(devs, fields[0])
		}
	}
	if len(devs) == 0 {
		return "未检测到已连接设备（adb 可用）", nil
	}
	return fmt.Sprintf("连接正常，%d 台设备：%s", len(devs), strings.Join(devs, "、")), nil
}

// AdbSetProxy 对指定 adb 路径的设备写入全局 http_proxy（host:port 取代理实际监听地址）。
func (a *App) AdbSetProxy(adbPath, deviceHost string) (string, error) {
	a.mu.Lock()
	running := a.srv != nil
	addr := a.addr
	if deviceHost == "" {
		deviceHost = a.cfg.ADB.DeviceProxyHost
	}
	a.mu.Unlock()
	if !running {
		return "", fmt.Errorf("代理未启动，无法设置设备代理")
	}
	if deviceHost == "" {
		deviceHost = settings.DefaultDeviceProxyHost
	}
	_, port, err := splitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("解析代理监听地址 %q: %w", addr, err)
	}
	proxy := deviceHost + ":" + port
	if _, err := a.runAdb(adbPath, "shell", "settings", "put", "global", "http_proxy", proxy); err != nil {
		return "", err
	}
	return fmt.Sprintf("已设置设备代理 %s", proxy), nil
}

// AdbClearProxy 清除指定 adb 路径设备的全局 http_proxy（`:0` = 无代理）。
func (a *App) AdbClearProxy(adbPath string) (string, error) {
	if _, err := a.runAdb(adbPath, "shell", "settings", "put", "global", "http_proxy", ":0"); err != nil {
		return "", err
	}
	return "已清除设备代理", nil
}

// runAdb 执行一条 adb 命令并返回合并输出；路径为空/命令失败/超时均包装为可读错误。
func (a *App) runAdb(adbPath string, args ...string) (string, error) {
	if strings.TrimSpace(adbPath) == "" {
		return "", fmt.Errorf("未配置 adb 路径")
	}
	ctx, cancel := context.WithTimeout(context.Background(), adbTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, adbPath, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("adb 命令超时（%s）", adbTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("adb %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// adbSnapshot 取当前 adb 配置快照（设备侧 host + 配置列表），供自动挂钩使用。
func (a *App) adbSnapshot() (host string, cfgs []settings.ADBDevice) {
	a.mu.Lock()
	defer a.mu.Unlock()
	host = a.cfg.ADB.DeviceProxyHost
	cfgs = append([]settings.ADBDevice(nil), a.cfg.ADB.Configs...)
	return
}

// autoSetAdbProxies 代理启动后：对 AutoSet 的配置自动设置设备代理（异步、best-effort，
// 不阻塞启停流程；失败仅记录日志）。
func (a *App) autoSetAdbProxies() {
	host, cfgs := a.adbSnapshot()
	for _, c := range cfgs {
		if !c.AutoSet || strings.TrimSpace(c.Path) == "" {
			continue
		}
		go func(c settings.ADBDevice) {
			if _, err := a.AdbSetProxy(c.Path, host); err != nil {
				runtimeLogf(a, "自动设置设备代理失败（%s）: %v", c.Name, err)
			}
		}(c)
	}
}

// autoClearAdbProxies 代理停止后：清除 AutoSet 配置的设备代理（异步、best-effort），
// 避免代理停了设备仍指向失效代理导致断网。
func (a *App) autoClearAdbProxies() {
	_, cfgs := a.adbSnapshot()
	for _, c := range cfgs {
		if !c.AutoSet || strings.TrimSpace(c.Path) == "" {
			continue
		}
		go func(c settings.ADBDevice) {
			if _, err := a.AdbClearProxy(c.Path); err != nil {
				runtimeLogf(a, "自动清除设备代理失败（%s）: %v", c.Name, err)
			}
		}(c)
	}
}

// clearAdbProxiesSync 退出时同步清除 AutoSet 配置的设备代理（Shutdown 用，
// 等待至多 adbTimeout；进程即将退出，不能用 fire-and-forget）。
func (a *App) clearAdbProxiesSync() {
	_, cfgs := a.adbSnapshot()
	for _, c := range cfgs {
		if !c.AutoSet || strings.TrimSpace(c.Path) == "" {
			continue
		}
		if _, err := a.AdbClearProxy(c.Path); err != nil {
			log.Printf("退出时清除设备代理失败（%s）: %v", c.Name, err)
		}
	}
}

// splitHostPort 从 host:port 取出端口（仅校验形态）。
func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i <= 0 || i == len(addr)-1 {
		return "", "", fmt.Errorf("非法地址 %q", addr)
	}
	return addr[:i], addr[i+1:], nil
}

// runtimeLogf GUI 下走 wails 日志、headless 下退化为标准 log（a.ctx 为 nil）。
func runtimeLogf(a *App, format string, args ...any) {
	if a.ctx != nil {
		runtime.LogErrorf(a.ctx, format, args...)
		return
	}
	log.Printf(format, args...)
}
