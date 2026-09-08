package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/settings"
)

// ---------- ADB 安卓模拟器/真机自动代理 Bindings ----------
//
// 设备侧 HTTP 代理通过 `adb shell settings put global http_proxy <host>:<port>` 写入，
// 清除用 `... http_proxy :0`。host 为设备访问宿主机 PrismProxy 的 IP（雷电 NAT 默认
// 172.16.1.2），端口取代理实际监听端口。每条配置保存「名称 + adb 路径 + 可选设备序列号」，
// AutoSet=true 的配置在代理启动时自动设置、停止时自动清除。
//
// 自动挂钩的竞态收敛：所有自动 set/clear 都入队 adbCh，由单个 worker 串行执行；
// 每次代理启动/停止代际号 adbGen 自增，任务入队时捕获代际，worker 执行前校验——
// 热重启（stop 紧接 start）时 stop 的 clear 代际已过期，直接丢弃，只有最新 start 的
// set 生效，杜绝「clear 晚于 set 落盘导致设备直连」的竞态。

const (
	// adbTimeout 单条 adb 命令超时：adb server 首次启动/设备连接可能较慢。
	adbTimeout = 20 * time.Second
	// adbSessionTimeout 关机/注销/退出路径的同步清除超时：关机时限紧迫，多设备并行。
	adbSessionTimeout = 8 * time.Second
	// adbQueueLen 自动任务队列容量（满则丢弃并记日志，正常启停任务量极小）。
	adbQueueLen = 64
)

// adbOp 一个自动代理任务（set 或 clear），gen 为入队时的代理代际号。
type adbOp struct {
	gen    uint64
	isSet  bool
	host   string // 仅 set 使用：设备侧访问宿主机的 IP
	device settings.ADBDevice
}

// startAdbWorker 启动自动 ADB 任务 worker（进程生命周期内单实例，NewApp 时启动）。
func (a *App) startAdbWorker() {
	a.adbCh = make(chan adbOp, adbQueueLen)
	go func() {
		for op := range a.adbCh {
			if op.gen != a.adbGen.Load() {
				continue // 已有更新的启停代际：本任务过期（热重启的 clear 被后续 set 覆盖）
			}
			var err error
			if op.isSet {
				_, err = a.AdbSetProxy(op.device.Path, op.device.Serial, op.host)
			} else {
				_, err = a.AdbClearProxy(op.device.Path, op.device.Serial)
			}
			if err != nil {
				action := "设置"
				if !op.isSet {
					action = "清除"
				}
				runtimeLogf(a, "自动%s设备代理失败（%s）: %v", action, op.device.Name, err)
			}
		}
	}()
}

// enqueueAdbOps 为当前所有 AutoSet 配置入队一批 set/clear 任务（gen 为调用方捕获的代际号）。
func (a *App) enqueueAdbOps(gen uint64, isSet bool) {
	host, cfgs := a.adbSnapshot()
	for _, c := range cfgs {
		if !c.AutoSet || strings.TrimSpace(c.Path) == "" {
			continue
		}
		op := adbOp{gen: gen, isSet: isSet, device: c}
		if isSet {
			op.host = host
		}
		select {
		case a.adbCh <- op:
		default:
			runtimeLogf(a, "ADB 自动任务队列已满，跳过（%s）", c.Name)
		}
	}
}

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
// 区分 device/offline/unauthorized 三态；多设备在线时提示需填写序列号。
func (a *App) AdbTest(adbPath string) (string, error) {
	out, err := a.runAdb(adbPath, "", "devices")
	if err != nil {
		return "", err
	}
	var devs, offline, unauth []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		// 形如 "emulator-5554\tdevice" / "offline" / "unauthorized"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[1] {
		case "device":
			devs = append(devs, fields[0])
		case "offline":
			offline = append(offline, fields[0])
		case "unauthorized":
			unauth = append(unauth, fields[0])
		}
	}
	switch {
	case len(devs) > 0:
		msg := fmt.Sprintf("连接正常，%d 台设备：%s", len(devs), strings.Join(devs, "、"))
		if len(unauth) > 0 {
			msg += fmt.Sprintf("；%d 台未授权（%s）", len(unauth), strings.Join(unauth, "、"))
		}
		if len(offline) > 0 {
			msg += fmt.Sprintf("；%d 台离线（%s）", len(offline), strings.Join(offline, "、"))
		}
		if len(devs) > 1 {
			msg += "。检测到多台设备：请在该配置填写「设备序列号」（adb devices 第一列），否则设置/清除会报 more than one device"
		}
		return msg, nil
	case len(unauth) > 0:
		return "", fmt.Errorf("检测到 %d 台设备但未授权（%s）：请在设备屏幕上点「允许 USB 调试」后重试", len(unauth), strings.Join(unauth, "、"))
	case len(offline) > 0:
		return "", fmt.Errorf("检测到 %d 台离线设备（%s）：请等待设备启动完成或重连后重试", len(offline), strings.Join(offline, "、"))
	default:
		return "未检测到已连接设备（adb 可用）", nil
	}
}

// AdbSetProxy 对指定设备写入全局 http_proxy（host:port 取代理实际监听地址）。
// serial 为空且 adb server 下有多台设备时 adb 会报错（需在配置中填序列号）。
func (a *App) AdbSetProxy(adbPath, serial, deviceHost string) (string, error) {
	if deviceHost == "" {
		// 全局 ADB 配置；projMu → a.mu 锁序：先取快照再进 a.mu，禁止反向（设计 §5.4）。
		a.projMu.Lock()
		deviceHost = a.gcfg.ADB.DeviceProxyHost
		a.projMu.Unlock()
	}
	a.mu.Lock()
	running := a.srv != nil
	addr := a.addr
	a.mu.Unlock()
	if !running {
		return "", fmt.Errorf("代理未启动，无法设置设备代理")
	}
	deviceHost = strings.TrimSpace(deviceHost)
	if deviceHost == "" {
		deviceHost = settings.DefaultDeviceProxyHost
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("解析代理监听地址 %q: %w", addr, err)
	}
	proxy := deviceHost + ":" + port
	if _, err := a.runAdb(adbPath, serial, "shell", "settings", "put", "global", "http_proxy", proxy); err != nil {
		return "", err
	}
	return fmt.Sprintf("已设置设备代理 %s", proxy), nil
}

// AdbClearProxy 清除指定设备的全局 http_proxy（`:0` = 无代理）。
func (a *App) AdbClearProxy(adbPath, serial string) (string, error) {
	if _, err := a.runAdb(adbPath, serial, "shell", "settings", "put", "global", "http_proxy", ":0"); err != nil {
		return "", err
	}
	return "已清除设备代理", nil
}

// runAdb 执行一条 adb 命令并返回合并输出；路径为空/命令失败/超时均包装为可读错误。
// serial 非空时拼 `adb -s <serial> ...` 选定设备（同一 adb server 多设备必需）。
func (a *App) runAdb(adbPath, serial string, args ...string) (string, error) {
	return a.runAdbTimeout(adbTimeout, adbPath, serial, args...)
}

// runAdbTimeout 同 runAdb，但允许指定超时（退出/关机路径用更短超时）。
func (a *App) runAdbTimeout(timeout time.Duration, adbPath, serial string, args ...string) (string, error) {
	if strings.TrimSpace(adbPath) == "" {
		return "", fmt.Errorf("未配置 adb 路径")
	}
	full := args
	if s := strings.TrimSpace(serial); s != "" {
		full = append([]string{"-s", s}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, adbPath, full...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("adb 命令超时（%s）", timeout)
	}
	if err != nil {
		msg := fmt.Sprintf("adb %s: %v", strings.Join(full, " "), err)
		if s := strings.TrimSpace(string(out)); s != "" {
			msg += ": " + s
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

// adbSnapshot 在 projMu 下读取 ADB 全局配置（M9 起随全局，gcfg）。
func (a *App) adbSnapshot() (host string, cfgs []settings.ADBDevice) {
	a.projMu.Lock()
	defer a.projMu.Unlock()
	host = a.gcfg.ADB.DeviceProxyHost
	cfgs = append([]settings.ADBDevice(nil), a.gcfg.ADB.Configs...)
	return
}

// clearAdbProxiesSync 同步清除所有 AutoSet 配置的设备代理（Shutdown/关机回调用）：
// 多设备并行、总耗时收敛到单条超时；进程即将退出/关机时限紧迫，不能 fire-and-forget。
func (a *App) clearAdbProxiesSync(timeout time.Duration) {
	_, cfgs := a.adbSnapshot()
	var wg sync.WaitGroup
	for _, c := range cfgs {
		if !c.AutoSet || strings.TrimSpace(c.Path) == "" {
			continue
		}
		wg.Add(1)
		go func(c settings.ADBDevice) {
			defer wg.Done()
			if _, err := a.runAdbTimeout(timeout, c.Path, c.Serial, "shell", "settings", "put", "global", "http_proxy", ":0"); err != nil {
				log.Printf("退出时清除设备代理失败（%s）: %v", c.Name, err)
			}
		}(c)
	}
	wg.Wait()
}

// convergeAdbConfigs 处理 ADB 配置热更新（SaveSettings 保存后调用）：
//   - 旧配置中 AutoSet 但新配置被删除/取消勾选的设备：补一次 clear——否则停止代理时
//     快照已不含它，设备 http_proxy 会永久残留、指向死代理导致断网；
//   - 代理运行中新开启 AutoSet 的设备：补一次 set（未重启监听地址时没有启动挂钩）。
//     热重启（restarted=true）场景的新增 set 由启动挂钩统一处理，此处不重复。
func (a *App) convergeAdbConfigs(old, nu settings.ADBConfig, restarted bool) {
	type adbKey struct{ path, serial string }
	keyOf := func(c settings.ADBDevice) adbKey {
		return adbKey{strings.TrimSpace(c.Path), strings.TrimSpace(c.Serial)}
	}
	newIdx := map[adbKey]settings.ADBDevice{}
	for _, c := range nu.Configs {
		if strings.TrimSpace(c.Path) != "" {
			newIdx[keyOf(c)] = c
		}
	}
	var removed, added []settings.ADBDevice
	for _, c := range old.Configs {
		if !c.AutoSet || strings.TrimSpace(c.Path) == "" {
			continue
		}
		if nc, ok := newIdx[keyOf(c)]; !ok || !nc.AutoSet {
			removed = append(removed, c)
		}
	}
	if !restarted {
		oldAuto := map[adbKey]bool{}
		for _, c := range old.Configs {
			if c.AutoSet && strings.TrimSpace(c.Path) != "" {
				oldAuto[keyOf(c)] = true
			}
		}
		for _, c := range nu.Configs {
			if c.AutoSet && strings.TrimSpace(c.Path) != "" && !oldAuto[keyOf(c)] {
				added = append(added, c)
			}
		}
	}
	if len(removed) == 0 && len(added) == 0 {
		return
	}
	go func() {
		a.mu.Lock()
		running := a.srv != nil
		a.mu.Unlock()
		for _, c := range removed {
			if _, err := a.AdbClearProxy(c.Path, c.Serial); err != nil {
				runtimeLogf(a, "配置变更后清除设备代理失败（%s）: %v", c.Name, err)
			}
		}
		if !running {
			return
		}
		for _, c := range added {
			if _, err := a.AdbSetProxy(c.Path, c.Serial, nu.DeviceProxyHost); err != nil {
				runtimeLogf(a, "配置变更后设置设备代理失败（%s）: %v", c.Name, err)
			}
		}
	}()
}

// runtimeLogf GUI 下走 wails 日志、headless 下退化为标准 log（a.ctx 为 nil）。
func runtimeLogf(a *App, format string, args ...any) {
	if a.ctx != nil {
		runtime.LogErrorf(a.ctx, format, args...)
		return
	}
	log.Printf(format, args...)
}
