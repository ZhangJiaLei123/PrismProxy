package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchSourceBytes_BOMAndErrors 覆盖 CLI/非当前项目规则/域名组导入的统一读入口：
// 本地文件与 HTTP 内容均剥 UTF-8 BOM；文件错误包"读取文件"、HTTP 错误不包"读取文件"。
func TestFetchSourceBytes_BOMAndErrors(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}

	// 1) 本地文件带 BOM → 剥除
	dir := t.TempDir()
	f := filepath.Join(dir, "rules.json")
	if err := os.WriteFile(f, append(append([]byte{}, bom...), []byte("{\"version\":1}")...), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fetchSourceBytes(f)
	if err != nil {
		t.Fatalf("读取本地文件失败: %v", err)
	}
	if strings.HasPrefix(string(got), "\ufeff") {
		t.Fatalf("本地文件 BOM 未剥离: %q", got)
	}
	if string(got) != `{"version":1}` {
		t.Fatalf("本地文件内容异常: %q", got)
	}

	// 2) 不存在的本地文件 → 错误含"读取文件"
	if _, err := fetchSourceBytes(filepath.Join(dir, "missing.json")); err == nil ||
		!strings.Contains(err.Error(), "读取文件") {
		t.Fatalf("不存在文件应报\"读取文件\"错误，实际: %v", err)
	}

	// 3) HTTP 返回带 BOM 体 → 剥除
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.Write(append(append([]byte{}, bom...), []byte("a.com\nb.com\n")...))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	got, err = fetchSourceBytes(srv.URL + "/ok")
	if err != nil {
		t.Fatalf("HTTP 读取失败: %v", err)
	}
	if strings.HasPrefix(string(got), "\ufeff") {
		t.Fatalf("HTTP 内容 BOM 未剥离: %q", got)
	}
	if string(got) != "a.com\nb.com\n" {
		t.Fatalf("HTTP 内容异常: %q", got)
	}

	// 4) HTTP 404 → 错误不含"读取文件"（网络错误由 httpGet 包装为"下载失败"）
	if _, err := fetchSourceBytes(srv.URL + "/missing"); err == nil ||
		strings.Contains(err.Error(), "读取文件") ||
		!strings.Contains(err.Error(), "下载失败") {
		t.Fatalf("HTTP 404 应报\"下载失败\"且不含\"读取文件\"，实际: %v", err)
	}
}

// TestStripUTF8BOM 幂等：无 BOM 原样返回，有 BOM 剥离，重复调用安全。
func TestStripUTF8BOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	plain := []byte("hello")
	if string(stripUTF8BOM(plain)) != "hello" {
		t.Fatal("无 BOM 内容应原样返回")
	}
	once := stripUTF8BOM(append(append([]byte{}, bom...), plain...))
	if string(once) != "hello" {
		t.Fatalf("单次剥离异常: %q", once)
	}
	if string(stripUTF8BOM(once)) != "hello" {
		t.Fatal("二次剥离应幂等")
	}
}
