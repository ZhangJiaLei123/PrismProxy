package main

import (
	"embed"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"prismproxy/internal/ctlapi"
	"prismproxy/internal/settings"
	"prismproxy/internal/sysproxy"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// M8：cli 子命令模式——控制运行中的实例（不启动 GUI/代理），须在 flag.Parse 前拦截
	if len(os.Args) >= 2 && os.Args[1] == "cli" {
		os.Exit(ctlapi.RunCLI(settings.DefaultConfigDir(), os.Args[2:]))
	}

	headless := flag.Bool("headless", false, "run as CLI proxy (no UI)")
	addr := flag.String("addr", "", "listen address (empty = use saved settings)")
	noMITM := flag.Bool("no-mitm", false, "disable HTTPS MITM (CONNECT tunnels blindly)")
	flag.Parse()

	if *headless {
		runHeadless(*addr, *noMITM)
		return
	}

	// WebView2 运行时自检（wails 对缺失运行时只返回裸错误，提前引导）
	if !ensureWebView2() {
		os.Exit(1)
	}

	app := NewApp(*addr, *noMITM)
	err := wails.Run(&options.App{
		Title:     "PrismProxy",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []interface{}{app},
	})
	if err != nil {
		log.Fatalf("wails: %v", err)
	}
}

// runHeadless 无 UI 命令行模式（调试/冒烟用；吃 settings.json）。
// M8 起复用 App 服务层并启动本地控制 API（cli 子命令可连），与 GUI 模式能力对齐。
func runHeadless(addr string, noMITM bool) {
	app := NewApp(addr, noMITM)

	// 崩溃自愈：上次接管系统代理期间被强杀 → 按备份还原（GUI/headless 共用）
	if healed, err := sysproxy.SelfHeal(app.backupFile()); err != nil {
		log.Printf("系统代理自愈失败: %v", err)
	} else if healed {
		log.Printf("检测到上次异常退出，已恢复原系统代理设置")
	}

	// 启动抓包（失败仅记录，控制 API 仍可用以便排查）
	if err := app.StartProxy(addr); err != nil {
		log.Printf("代理启动失败: %v", err)
	} else {
		st := app.GetProxyStatus()
		log.Printf("PrismProxy (headless) listening on %s mode=%s (CA page: http://%s/ca)", st.Addr, st.Mode, st.Addr)
	}

	// 本地控制 API（headless：ui:* 界面动作返回 ui=false 提示）
	app.startCtlAPI()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	app.restoreSystemProxy()
	_ = app.StopProxy()
	app.stopCtlAPI()
}
