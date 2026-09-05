package app

// ctl_server.go：App 的本地控制 API（M8）生命周期管理。
// GUI startup 与 headless 主流程均调用 startCtlAPI；shutdown/退出时 stopCtlAPI。

import (
	"log"

	"prismproxy/internal/ctlapi"
)

// startCtlAPI 启动本地控制 API：端口占用等失败仅 log 不致命（GUI/代理照常运行）。
// GUI 模式随后应 SetUI(true)；headless 不调用（ui:* 界面动作返回 ui=false）。
func (a *App) startCtlAPI() {
	srv := ctlapi.NewServer(ctlapi.DefaultAddr, "", ctlapi.EndpointFile(a.cfgDir), newCtlService(a))
	if err := srv.Start(); err != nil {
		log.Printf("控制 API 启动失败（cli 子命令暂不可用）: %v", err)
		return
	}
	a.mu.Lock()
	a.ctl = srv
	a.mu.Unlock()
}

// stopCtlAPI 停止控制 API 并清理 endpoint 文件（shutdown / headless 退出时调用，幂等）
func (a *App) stopCtlAPI() {
	a.mu.Lock()
	srv := a.ctl
	a.ctl = nil
	a.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}
