package main

import (
	"embed"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"prismproxy/internal/capture"
	"prismproxy/internal/domains"
	"prismproxy/internal/mitm"
	"prismproxy/internal/proxy"
	"prismproxy/internal/rules"
	"prismproxy/internal/settings"
	"prismproxy/internal/store"
	"prismproxy/internal/sysproxy"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	headless := flag.Bool("headless", false, "run as CLI proxy (no UI)")
	addr := flag.String("addr", "", "listen address (empty = use saved settings)")
	noMITM := flag.Bool("no-mitm", false, "disable HTTPS MITM (CONNECT tunnels blindly)")
	flag.Parse()

	if *headless {
		runHeadless(*addr, *noMITM)
		return
	}

	// WebView2 运行时自检（wails 对缺失运行时只报裸错误，提前引导）
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

// runHeadless 无 UI 命令行模式（调试/冒烟用；M4 起同样吃 settings.json）
func runHeadless(addr string, noMITM bool) {
	cfgDir := settings.DefaultConfigDir()

	cfg, err := settings.Load(cfgDir)
	if err != nil {
		cfg = settings.Default()
	}
	// 旧 captureRules/processRules → filterGroups 迁移（规则设计 §六）：迁移即落盘一次
	if cfg.Migrate() {
		if serr := cfg.Save(cfgDir); serr != nil {
			log.Printf("迁移配置落盘失败（内存态已迁移）: %v", serr)
		}
	}
	if addr == "" {
		addr = cfg.ListenAddr
	}

	groups, _ := domains.LoadUser(filepath.Join(cfgDir, "domains"))
	var gmap map[string][]string
	if groups != nil {
		gmap = groups.Domains
	}
	eng := &rules.Holder{}
	// 编译失败兜底：log warn + 空引擎全放行（规则设计 §5.3）
	if e, err := rules.NewEngine(cfg.FilterGroups, cfg.DecryptRules, gmap); err == nil {
		eng.Set(e)
	} else {
		log.Printf("过滤规则编译失败，当前过滤未生效（全量显示）: %v", err)
	}

	st := store.New(cfg.MaxFlows)
	st.SetLimits(cfg.MaxFlows, int64(cfg.MaxBodyMB)<<20)
	rec := capture.NewRecorder(st)
	proxy.ApplyRulesFilter(rec, eng)

	// Root CA：首次启动生成并持久化到用户配置目录（%APPDATA%\PrismProxy\ca）
	var ca *mitm.CA
	if !noMITM {
		caDir := filepath.Join(cfgDir, "ca")
		ca, err = mitm.LoadOrCreateCA(caDir)
		if err != nil {
			log.Fatalf("init root ca: %v", err)
		}
		log.Printf("root ca ready: %s (cert expires %s)", ca.CertPEMPath(), ca.Cert.NotAfter.Format("2006-01-02"))
	}

	upstream := ""
	switch cfg.UpstreamMode {
	case settings.UpstreamManual:
		upstream = cfg.UpstreamProxy
	case settings.UpstreamSystem:
		upstream = sysproxy.UpstreamFromSystem(addr)
	}
	srv, err := proxy.NewServerOpts(addr, rec, ca, &proxy.Options{UpstreamProxy: upstream, Engine: eng})
	if err != nil {
		log.Fatalf("init proxy server: %v", err)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Printf("proxy server exited: %v", err)
		}
	}()
	mode := "MITM"
	if ca == nil {
		mode = "tunnel-only"
	}
	log.Printf("PrismProxy (headless) listening on %s mode=%s (CA page: http://%s/ca)", addr, mode, addr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	srv.Close()
}
