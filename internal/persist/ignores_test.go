package persist

import (
	"testing"
	"time"

	"prismproxy/internal/capture"
)

// TestReviewIgnoreCRUD 名单增删查 + 归一化 + 重复加入不新增。
func TestReviewIgnoreCRUD(t *testing.T) {
	w := archiveWriter(t)

	// 空库读名单不报错（表由 migrate 建好）
	if igs, err := w.ListReviewIgnores(); err != nil || len(igs) != 0 {
		t.Fatalf("空名单 = %d, %v", len(igs), err)
	}

	// host 归一化：去端口/小写/去 *. 前缀
	ig, added, err := w.AddReviewIgnore("host", "  API.Example.com:8443  ", "")
	if err != nil || !added || ig.Value != "api.example.com" {
		t.Fatalf("AddReviewIgnore host = %+v added=%v err=%v", ig, added, err)
	}
	// 重复加入（归一化后同值）：不新增
	if _, added2, err := w.AddReviewIgnore("host", "*.api.example.com", "备注"); err != nil || added2 {
		t.Fatalf("重复 host added=%v err=%v（期望 false）", added2, err)
	}

	// path 归一化：去 query、补 /、拒绝 /
	if ig, added, err := w.AddReviewIgnore("path", "v1/health?x=1", ""); err != nil || !added || ig.Value != "/v1/health" {
		t.Fatalf("AddReviewIgnore path = %+v added=%v err=%v", ig, added, err)
	}
	if _, _, err := w.AddReviewIgnore("path", "/", ""); err == nil {
		t.Fatal("path=/ 应被拒绝")
	}
	// proc 仅 trim
	if ig, _, err := w.AddReviewIgnore("proc", "  WeChat.exe ", ""); err != nil || ig.Value != "WeChat.exe" {
		t.Fatalf("AddReviewIgnore proc = %+v err=%v", ig, err)
	}
	// 非法 kind
	if _, _, err := w.AddReviewIgnore("bad", "x", ""); err == nil {
		t.Fatal("非法 kind 应报错")
	}

	igs, err := w.ListReviewIgnores()
	if err != nil || len(igs) != 3 {
		t.Fatalf("名单应有 3 条，得 %d, %v", len(igs), err)
	}

	// 删除：归一化值删不到（未归一化的带端口形式）
	if ok, err := w.DeleteReviewIgnore("host", "api.example.com:8443"); err != nil || ok {
		t.Fatalf("未归一化值删除应 false，得 %v, %v", ok, err)
	}
	if ok, err := w.DeleteReviewIgnore("host", "api.example.com"); err != nil || !ok {
		t.Fatalf("删除 host 应 true，得 %v, %v", ok, err)
	}
	if igs, _ := w.ListReviewIgnores(); len(igs) != 2 {
		t.Fatalf("删除后应剩 2 条，得 %d", len(igs))
	}
}

// seedIgnoreRows 归档一组差异化（host/path/进程）的流，并直接改独立列；
// 进程名在 data JSON 内，逐条构造（同毫秒用不同 id，顺序断言走显式排序）。
func seedIgnoreRows(t *testing.T, w *Writer) []string {
	t.Helper()
	specs := []struct {
		id, host, path, proc string
	}{
		{"ig-01", "api.example.com", "/v1/login", "chrome.exe"},
		{"ig-02", "api.example.com:8443", "/v1/login", "chrome.exe"},
		{"ig-03", "a.api.example.com", "/v1/users", "WeChat.exe"},
		{"ig-04", "a.api.example.com:443", "/v1/users/123", "WeChat.exe"},
		{"ig-05", "other.com", "/v2/ping", "healthcheck.exe"},
		{"ig-06", "example.com.cn", "/v1/login", "curl.exe"}, // 不得被 example.com 误伤
	}
	flows := make([]*capture.Flow, 0, len(specs))
	ids := make([]string, 0, len(specs))
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).UnixMilli()
	for i, s := range specs {
		f := testFlow(s.id, s.host, capture.StateDone, "x")
		f.Timing.Start = time.UnixMilli(base + int64(i)*60000)
		f.Process = &capture.ProcessInfo{PID: uint32(100 + i), Name: s.proc}
		f.Request.Method = "GET"
		f.Request.URL = "https://" + s.host + s.path
		flows = append(flows, f)
		ids = append(ids, s.id)
	}
	if err := w.ArchiveFlows(flows); err != nil {
		t.Fatalf("ArchiveFlows: %v", err)
	}
	// testFlow 的 host 即写入 f.host；path 由落库时从 URL 解析。保险起见显式校正独立列。
	for _, s := range specs {
		if _, err := w.db.Exec(`UPDATE flows SET host=?, path=? WHERE id=?`, s.host, s.path, s.id); err != nil {
			t.Fatalf("校正列失败: %v", err)
		}
	}
	return ids
}

func countRows(t *testing.T, w *Writer, opts ReviewListOpts) int {
	t.Helper()
	n, err := w.CountFlows("all", "all", 0, 0, opts)
	if err != nil {
		t.Fatalf("CountFlows: %v", err)
	}
	return n
}

// TestReviewIgnoreQueryHost 域名忽略：自身/带端口/任意层子域（含子域带端口）均隐藏；
// 眼睛开启（ShowIgnored）时全部可见；近似域名不被误伤。
func TestReviewIgnoreQueryHost(t *testing.T) {
	w := archiveWriter(t)
	seedIgnoreRows(t, w)

	if n := countRows(t, w, ReviewListOpts{}); n != 6 {
		t.Fatalf("无忽略应 6 条，得 %d", n)
	}
	if _, _, err := w.AddReviewIgnore("host", "api.example.com", ""); err != nil {
		t.Fatalf("AddReviewIgnore: %v", err)
	}
	// 隐藏：ig-01/02/03/04 命中，留 ig-05/06
	if n := countRows(t, w, ReviewListOpts{}); n != 2 {
		t.Fatalf("host 忽略后应 2 条，得 %d", n)
	}
	rows, err := w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{SortKey: "time"})
	if err != nil || len(rows) != 2 {
		t.Fatalf("host 忽略列表 = %d, %v", len(rows), err)
	}
	for _, f := range rows {
		if string(f.ID) != "ig-05" && string(f.ID) != "ig-06" {
			t.Fatalf("未命中忽略的行被排除: %s", f.ID)
		}
	}
	// 眼睛开启：6 条全回
	if n := countRows(t, w, ReviewListOpts{ShowIgnored: true}); n != 6 {
		t.Fatalf("ShowIgnored=true 应 6 条，得 %d", n)
	}
}

// TestReviewIgnoreQueryPath 路径忽略：精确路径与下级路径隐藏。
func TestReviewIgnoreQueryPath(t *testing.T) {
	w := archiveWriter(t)
	seedIgnoreRows(t, w)

	if _, _, err := w.AddReviewIgnore("path", "/v1/users", ""); err != nil {
		t.Fatalf("AddReviewIgnore: %v", err)
	}
	// 隐藏 ig-03(/v1/users)、ig-04(/v1/users/123)；/v1/login 与 /v2/ping 保留
	if n := countRows(t, w, ReviewListOpts{}); n != 4 {
		t.Fatalf("path 忽略后应 4 条，得 %d", n)
	}
}

// TestReviewIgnoreQueryProc 进程忽略：大小写不敏感等值匹配，不影响其他进程。
func TestReviewIgnoreQueryProc(t *testing.T) {
	w := archiveWriter(t)
	seedIgnoreRows(t, w)

	if _, _, err := w.AddReviewIgnore("proc", "wechat.exe", ""); err != nil {
		t.Fatalf("AddReviewIgnore: %v", err)
	}
	// 隐藏 ig-03/04（WeChat.exe），其余 4 条保留
	if n := countRows(t, w, ReviewListOpts{}); n != 4 {
		t.Fatalf("proc 忽略后应 4 条，得 %d", n)
	}
}

// TestReviewIgnoreOrderBy 表头排序：非时间列默认 asc、显式 desc、非法列回落时间倒序。
func TestReviewIgnoreOrderBy(t *testing.T) {
	w := archiveWriter(t)
	ids := seedIgnoreRows(t, w)

	// host asc：字典序最小在前。a.api.example.com(:443) 两条同列值组——
	// 无端口串小于带端口串，故首行 ig-03。
	rows, err := w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{SortKey: "host"})
	if err != nil {
		t.Fatalf("FlowsByTag host asc: %v", err)
	}
	if string(rows[0].ID) != "ig-03" {
		t.Fatalf("host asc 首行 = %s，期望 ig-03", rows[0].ID)
	}
	// host desc：other.com 与 api/a.api 比较，'o' 最大 → ig-05
	rows, _ = w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{SortKey: "host", SortDir: "desc"})
	if string(rows[0].ID) != "ig-05" {
		t.Fatalf("host desc 首行 = %s，期望 ig-05", rows[0].ID)
	}
	// 默认（无排序参数）= 时间倒序：最后一条在前
	rows, _ = w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{})
	if string(rows[0].ID) != ids[len(ids)-1] {
		t.Fatalf("默认应时间倒序，首行 = %s，期望 %s", rows[0].ID, ids[len(ids)-1])
	}
	// proc 排序：json_extract $.Process.Name asc，chrome 最小
	rows, _ = w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{SortKey: "proc"})
	if got := rows[0].Process.Name; got != "chrome.exe" {
		t.Fatalf("proc asc 首行进程序 = %s，期望 chrome.exe", got)
	}
}
