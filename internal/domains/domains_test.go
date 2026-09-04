package domains

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLoadMergedUserOverride(t *testing.T) {
	fsys := fstest.MapFS{
		"domains/baidu.txt": &fstest.MapFile{Data: []byte("baidu.com\n")},
	}
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "custom.txt"), []byte("Example.COM\n# 注释\n*.foo.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 同 id 覆盖内置组
	if err := os.WriteFile(filepath.Join(userDir, "baidu.txt"), []byte("evil-baidu.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := LoadMerged(fsys, "domains", userDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Domains["custom"]; len(got) != 2 || got[0] != "example.com" || got[1] != "foo.com" {
		t.Fatalf("custom group = %v", got)
	}
	if got := g.Domains["baidu"]; len(got) != 1 || got[0] != "evil-baidu.com" {
		t.Fatalf("override baidu = %v", got)
	}
	if !g.Custom["custom"] || !g.Custom["baidu"] {
		t.Fatalf("Custom flags = %v", g.Custom)
	}
}

func TestLoadMergedNoUserDir(t *testing.T) {
	fsys := fstest.MapFS{"domains/a.txt": &fstest.MapFile{Data: []byte("a.com\n")}}
	g, err := LoadMerged(fsys, "domains", filepath.Join(t.TempDir(), "not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Domains) != 1 || len(g.Custom) != 0 {
		t.Fatalf("got %v %v", g.Domains, g.Custom)
	}
}

func TestWriteUser(t *testing.T) {
	dir := t.TempDir()
	n, err := WriteUser(dir, "my-co", []byte("a.com\n b.com # 注释\n\na.com\n"))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// 原文落盘（保留注释）
	data, _ := os.ReadFile(filepath.Join(dir, "my-co.txt"))
	if string(data) != "a.com\n b.com # 注释\n\na.com\n" {
		t.Fatalf("raw = %q", data)
	}
	if _, err := WriteUser(dir, "Bad_ID", []byte("a.com")); err == nil {
		t.Fatal("invalid id should fail")
	}
	if _, err := WriteUser(dir, "ok", []byte("# 只有注释\n")); err == nil {
		t.Fatal("empty parse should fail")
	}
}

func TestDeleteUser(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x.com"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser(dir, "x"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUser(dir, "x"); err == nil {
		t.Fatal("second delete should fail")
	}
}
