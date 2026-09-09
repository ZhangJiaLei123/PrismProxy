package app

// ctl_server.go：App 的本地控制 API（M8）生命周期管理。
// GUI startup 与 headless 主流程均调用 startCtlAPI；shutdown/退出时 stopCtlAPI。

import (
	"io/fs"
	"log"

	"prismproxy/internal/ctlapi"
)

// startCtlAPI 启动本地控制 API：端口占用等失败仅 log 不致命（GUI/代理照常运行）。
// GUI 模式随后应 SetUI(true)；headless 不调用（ui:* 界面动作返回 ui=false）。
func (a *App) startCtlAPI() {
	var staticFS fs.FS
	if a.staticFS != nil {
		// 根 embed.FS（含 frontend/dist 前缀）→ 子树裁剪到 dist，供复盘页托管（设计 §6.3）
		sub, err := fs.Sub(a.staticFS, "frontend/dist")
		if err != nil {
			log.Printf("复盘页静态资源子树裁剪失败（复盘页不可用，控制 API 照常）: %v", err)
		} else {
			staticFS = sub
		}
	}
	srv := ctlapi.NewServer(ctlapi.DefaultAddr, "", ctlapi.EndpointFile(a.cfgDir), newCtlService(a), staticFS)
	if err := srv.Start(); err != nil {
		log.Printf("控制 API 启动失败（cli 子命令暂不可用）: %v", err)
		return
	}
	a.mu.Lock()
	a.ctl = srv
	a.mu.Unlock()
	// 控制 API 就绪后推一帧状态：headless 下代理先于控制 API 启动（成功路径此前无发布点），
	// GUI 下与 startup 末尾的发布重复一帧，幂等无害。
	a.publishStatus()
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
