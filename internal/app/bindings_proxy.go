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

// StartProxy 启动代理（同步 listen，端口占用立即报错）；addr 为空用配置里的监听地址
func (a *App) StartProxy(addr string) error {
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
	return nil
}

func (a *App) StopProxy() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srv == nil {
		return nil
	}
	err := a.srv.Close()
	a.srv = nil
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
