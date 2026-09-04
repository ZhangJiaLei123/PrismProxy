package settings

import (
	"os"
	"path/filepath"
	"testing"

	"prismproxy/internal/rules"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ListenAddr != "127.0.0.1:9090" || s.MaxFlows != 2000 || s.MaxBodyMB != 256 {
		t.Fatalf("默认值不对: %+v", s)
	}
	if len(s.BypassList) != len(BuiltinBypass) {
		t.Fatal("默认绕过列表应为内置列表")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := Default()
	s.ListenAddr = "0.0.0.0:8888"
	s.UpstreamMode = UpstreamManual
	s.UpstreamProxy = "127.0.0.1:7890"
	s.MaxFlows = 500
	s.MaxBodyMB = 64
	s.BypassList = append(s.BypassList, "my.corp.com")
	s.CaptureRules = []rules.CaptureRule{{Action: rules.ActionExclude, Host: "@ai", URLRe: "/telemetry/"}}
	s.DecryptRules = []rules.DecryptRule{{Action: rules.ActionBypass, Host: "pin.example.com"}}
	s.ProcessRules = []rules.ProcessRule{{Action: rules.ActionExclude, Name: "dnplayer.exe"}}

	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != s.ListenAddr || got.UpstreamMode != s.UpstreamMode ||
		got.UpstreamProxy != s.UpstreamProxy || got.MaxFlows != 500 || got.MaxBodyMB != 64 {
		t.Fatalf("往返后基础字段不一致: %+v", got)
	}
	if len(got.BypassList) != len(s.BypassList) {
		t.Fatal("绕过列表丢失")
	}
	if len(got.CaptureRules) != 1 || got.CaptureRules[0].Host != "@ai" {
		t.Fatalf("捕获规则丢失: %+v", got.CaptureRules)
	}
	if len(got.DecryptRules) != 1 || got.DecryptRules[0].Host != "pin.example.com" {
		t.Fatalf("解密规则丢失: %+v", got.DecryptRules)
	}
	if len(got.ProcessRules) != 1 || got.ProcessRules[0].Name != "dnplayer.exe" {
		t.Fatalf("进程规则丢失: %+v", got.ProcessRules)
	}
}

func TestLoadLegacyMissingFieldsFallback(t *testing.T) {
	dir := t.TempDir()
	// 模拟旧版配置：只有 listenAddr
	if err := Default().Save(dir); err != nil {
		t.Fatal(err)
	}
	// 手工截断成旧版最小 JSON
	minJSON := []byte(`{"listenAddr":"127.0.0.1:9999"}`)
	if err := os.WriteFile(filepath.Join(dir, fileName), minJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != "127.0.0.1:9999" {
		t.Fatal("旧版字段应保留")
	}
	if got.MaxFlows != 2000 || len(got.BypassList) != len(BuiltinBypass) {
		t.Fatal("缺失字段应以默认值兜底")
	}
}

func TestValidate(t *testing.T) {
	bad := Default()
	bad.ListenAddr = "not-an-addr"
	if err := bad.Validate(); err == nil {
		t.Fatal("非法监听地址应报错")
	}

	bad = Default()
	bad.UpstreamMode = UpstreamManual
	if err := bad.Validate(); err == nil {
		t.Fatal("manual 模式空代理地址应报错")
	}

	bad = Default()
	bad.UpstreamMode = "bogus"
	if err := bad.Validate(); err == nil {
		t.Fatal("非法上游模式应报错")
	}

	bad = Default()
	bad.CaptureRules = []rules.CaptureRule{{Action: rules.ActionExclude, URLRe: "["}}
	if err := bad.Validate(); err == nil {
		t.Fatal("非法规则应报错")
	}

	good := Default()
	good.UpstreamMode = UpstreamManual
	good.UpstreamProxy = "127.0.0.1:7890"
	if err := good.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
}
