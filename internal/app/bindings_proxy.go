package app

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"prismproxy/internal/mitm"
	"prismproxy/internal/proxy"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/sysproxy"
)

// ---------- 代理生命周期 Bindings ----------

// StartProxy 启动代理（同步 listen，端口占用立即报错）；addr 为空用配置里的监听地址。
// 成功/失败均向 SSE status 频道推送（SetSystemProxy 内部连调时不重复推）。
func (a *App) StartProxy(addr string) error {
	if err := a.startProxy(addr); err != nil {
		a.publishStatus()
		return err
	}
	a.publishStatus()
	return nil
}

// startProxy 实际启动逻辑（不发布 status，供 SetSystemProxy 内部连调避免重复推送）
func (a *App) startProxy(addr string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv != nil {
		return fmt.Errorf("proxy already running on %s", a.addr)
	}
	if addr == "" {
		addr = a.cfg.ListenAddr
	}

	var ca *mitm.CA
	if !a.noMITM {
		var err error
		ca, err = mitm.LoadOrCreateCA(filepath.Join(a.cfgDir, "ca"))
		if err != nil {
			return fmt.Errorf("init root ca: %w", err)
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败：%w（端口可能被占用，可在「设置」中更换监听地址/端口）", addr, err)
	}
	srv, err := proxy.NewServerOpts(addr, a.rec, ca, &proxy.Options{
		UpstreamProxy: a.resolveUpstream(addr),
		Engine:        a.eng,
	})
	if err != nil {
		ln.Close()
		return err
	}
	go func() {
		if err := srv.Serve(ln); err != nil && a.ctx != nil {
			runtime.LogErrorf(a.ctx, "proxy exited: %v", err)
		}
	}()

	a.srv, a.ca, a.addr = srv, ca, addr
	a.startErr = "" // 任何一次成功启动都清除此前的启动失败标记（含手动重启）
	// 代理已监听：自动设置 AutoSet 设备的 http_proxy。异步触发（goroutine 等本函数
	// defer 解锁后才拿快照/执行命令，避免重入死锁），覆盖 GUI 自启/手动/设置重启所有路径。
	go a.autoSetAdbProxies()
	return nil
}

// StopProxy 停止代理并向 SSE status 频道推送（SetSystemProxy 内部连调时走 stopProxy 不重复推）
func (a *App) StopProxy() error {
	err := a.stopProxy()
	a.publishStatus()
	return err
}

func (a *App) stopProxy() error {
	a.mu.Lock()
	if a.srv == nil {
		a.mu.Unlock()
		return nil
	}
	err := a.srv.Close()
	a.srv = nil
	a.mu.Unlock()
	// 代理已停：清除 AutoSet 设备的 http_proxy，避免设备仍指向失效代理导致断网。
	// 异步触发（goroutine 等解锁后拿快照），覆盖手动停止/设置重启路径；退出路径另在
	// Shutdown 中同步清除，不依赖本 fire-and-forget。
	go a.autoClearAdbProxies()
	return err
}

func (a *App) GetProxyStatus() ProxyStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	mode := "MITM"
	if a.ca == nil {
		mode = "tunnel-only"
	}
	return ProxyStatus{Running: a.srv != nil, Addr: a.addr, Mode: mode, FlowCount: len(a.st.List()), StartError: a.startErr}
}

// resolveUpstream 按配置解析上游代理地址（空=直连）
func (a *App) resolveUpstream(selfAddr string) string {
	switch a.cfg.UpstreamMode {
	case settings.UpstreamManual:
		return a.cfg.UpstreamProxy
	case settings.UpstreamSystem:
		return sysproxy.UpstreamFromSystem(selfAddr)
	}
	return ""
}

// rebuildEngine 按当前配置重编译规则引擎并热替换（Holder 原子替换，持锁仅做快照）
func (a *App) rebuildEngine() error {
	a.mu.Lock()
	fg, dr := a.cfg.FilterGroups, a.cfg.DecryptRules
	var gmap map[string][]string
	if a.groups != nil {
		gmap = a.groups.Domains
	}
	a.mu.Unlock()
	e, err := rules.NewEngine(fg, dr, gmap)
	if err != nil {
		return err
	}
	a.eng.Set(e)
	return nil
}

// InstallRootCA 把根证书装入当前用户受信根存储（certutil -user，免管理员）
func (a *App) InstallRootCA() error {
	a.mu.Lock()
	ca := a.ca
	a.mu.Unlock()
	if ca == nil {
		return fmt.Errorf("MITM 未启用，无根证书可安装")
	}
	out, err := exec.Command("certutil", "-user", "-addstore", "Root", ca.CertPEMPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil: %v: %s", err, string(out))
	}
	return nil
}
