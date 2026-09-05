package main

import (
	"embed"
	"flag"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"prismproxy/internal/app"
	"prismproxy/internal/ctlapi"
	"prismproxy/internal/settings"
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
		app.RunHeadless(*addr, *noMITM)
		return
	}

	// WebView2 运行时自检（wails 对缺失运行时只返回裸错误，提前引导）
	if !app.EnsureWebView2() {
		os.Exit(1)
	}

	a := app.NewApp(*addr, *noMITM)
	err := wails.Run(&options.App{
		Title:     "PrismProxy",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  a.Startup,
		OnShutdown: a.Shutdown,
		Bind:       []interface{}{a},
	})
	if err != nil {
		log.Fatalf("wails: %v", err)
	}
}
