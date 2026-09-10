package persist

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"prismproxy/internal/capture"
)

// archiveWriter 打开一个归档 Writer（不 Start——标签方法同步直写，无需队列消费者）。
func archiveWriter(t *testing.T) *Writer {
	t.Helper()
	w, err := OpenArchive(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// seedTagged 归档 n 条流并打上同一标签，返回标签与流 id 列表。
// id 前缀用 ASCII（避免中文 id 在同 started_at 下按字典序倒序时干扰顺序断言）。
func seedTagged(t *testing.T, w *Writer, prefix string, tagName string, n int) (*TagInfo, []string) {
	t.Helper()
	tag, err := w.UpsertTag(tagName)
	if err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	flows := make([]*capture.Flow, 0, n)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		// 先 sleep 再建流：Timing.Start 在 NewFlow 时定格，须保证建流时刻递增
		time.Sleep(2 * time.Millisecond)
		id := fmt.Sprintf("flow-%s-%02d", prefix, i)
		f := testFlow(id, "tagged.com", capture.StateDone, fmt.Sprintf(`{"i":%d}`, i))
		flows = append(flows, f)
		ids = append(ids, id)
	}
	if err := w.ArchiveFlows(flows); err != nil {
		t.Fatalf("ArchiveFlows: %v", err)
	}
	if _, err := w.AddFlowTags(ids, tag.ID, time.Now().UnixMilli()); err != nil {
		t.Fatalf("AddFlowTags: %v", err)
	}
	return tag, ids
}

func TestTagIDNormalizationAndValidation(t *testing.T) {
	// 去空白 + lower 归一：大小写/空白差异同名同 id
	if TagID("Login Flow") != TagID("loginflow") {
		t.Fatal("大小写/空白归一失败：TagID 应一致")
	}
	if TagID("登录 流程") != TagID("登录流程") {
		t.Fatal("内部空白归一失败")
	}
	if !ValidTagID(TagID("任意标签")) {
		t.Fatal("TagID 应通过 ValidTagID")
	}
	if ValidTagID("t_xyz") || ValidTagID("nope") || ValidTagID("t_00112233445") {
		t.Fatal("非法 id 不应通过校验")
	}
	if NormalizeTagName("  a   b\tc ") != "a b c" {
		t.Fatalf("空白折叠错误: %q", NormalizeTagName("  a   b\tc "))
	}
}

func TestUpsertTagAndList(t *testing.T) {
	w := archiveWriter(t)
	tag, err := w.UpsertTag("回归测试")
	if err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	if tag.Count != 0 {
		t.Fatalf("新标签计数应为 0，得 %d", tag.Count)
	}
	firstUsed := tag.LastUsedAt
	time.Sleep(2 * time.Millisecond)
	// 同名再 upsert：不新建，仅刷新 last_used_at
	tag2, err := w.UpsertTag("回归测试")
	if err != nil {
		t.Fatalf("UpsertTag 二次: %v", err)
	}
	if tag2.ID != tag.ID {
		t.Fatal("同名标签 id 应稳定")
	}
	if tag2.LastUsedAt < firstUsed {
		t.Fatal("二次 upsert 应刷新 last_used_at")
	}
	tags, err := w.ListTags()
	if err != nil || len(tags) != 1 {
		t.Fatalf("ListTags = %v, %d 个", err, len(tags))
	}
}

func TestTagFlowsArchiveIdempotentAndQuery(t *testing.T) {
	w := archiveWriter(t)
	tag, ids := seedTagged(t, w, "login", "登录流程", 5)

	// 归档幂等：重复归档同一流不应产生重复流/报错，也不应改变排序（started_at 不变）。
	// 必须先 LoadFlowByID 取回已归档行再原样回写——新建 testFlow 会带当前时刻
	// Timing.Start，ON CONFLICT 刷新 started_at 后排序自然变化，不构成幂等语义。
	same, err := w.LoadFlowByID(ids[0])
	if err != nil || same == nil {
		t.Fatalf("LoadFlowByID(ids[0]): %v %v", same, err)
	}
	if err := w.ArchiveFlows([]*capture.Flow{same}); err != nil {
		t.Fatalf("重复归档: %v", err)
	}
	// AddFlowTags 幂等：重复关联 DO NOTHING（返回新增 0）、计数不翻倍
	if added, err := w.AddFlowTags(ids, tag.ID, time.Now().UnixMilli()); err != nil || added != 0 {
		t.Fatalf("重复 AddFlowTags: added=%d, %v（期望新增 0）", added, err)
	}

	tags, _ := w.ListTags()
	if len(tags) != 1 || tags[0].Count != 5 {
		t.Fatalf("标签计数 = %d，期望 5（重复关联不翻倍）", tags[0].Count)
	}
	if n, _ := w.CountTaggedFlows(); n != 5 {
		t.Fatalf("CountTaggedFlows = %d，期望 5", n)
	}

	// FlowsByTag 分页：limit=2 offset=0 → 2 条（倒序最新在前）
	page1, err := w.FlowsByTag(tag.ID, "archived", 0, 0, 2, 0, ReviewListOpts{})
	if err != nil || len(page1) != 2 {
		t.Fatalf("FlowsByTag page1 = %d, %v", len(page1), err)
	}
	if page1[0].ID != ids[4] || page1[0].Source != capture.SourceHistory || page1[0].Pinned {
		t.Fatalf("倒序/历史标记错误: %+v", page1[0])
	}
	page3, err := w.FlowsByTag(tag.ID, "archived", 0, 0, 2, 4, ReviewListOpts{})
	if err != nil || len(page3) != 1 {
		t.Fatalf("FlowsByTag page3 = %d, %v", len(page3), err)
	}
	// all：全部已标记
	all, err := w.FlowsByTag("all", "archived", 0, 0, 0, 0, ReviewListOpts{}) // limit<=0 默认 200
	if err != nil || len(all) != 5 {
		t.Fatalf("FlowsByTag all = %d, %v", len(all), err)
	}
	if _, err := w.FlowsByTag("bad-id", "archived", 0, 0, 10, 0, ReviewListOpts{}); err == nil {
		t.Fatal("非法标签 id 应报错")
	}

	// TagsForFlows 批量查名
	names, err := w.TagsForFlows([]string{ids[0], "missing"})
	if err != nil {
		t.Fatalf("TagsForFlows: %v", err)
	}
	if len(names[ids[0]]) != 1 || names[ids[0]][0] != "登录流程" {
		t.Fatalf("标签名回查错误: %v", names[ids[0]])
	}
	if _, ok := names["missing"]; ok {
		t.Fatal("无标签流不应出现在结果 map")
	}

	// LoadFlowByID / FlowExists
	if ok, _ := w.FlowExists(ids[1]); !ok {
		t.Fatal("FlowExists 应为 true")
	}
	f, err := w.LoadFlowByID(ids[1])
	if err != nil || f == nil || f.ID != ids[1] {
		t.Fatalf("LoadFlowByID = %v, %v", f, err)
	}
	f2, err := w.LoadFlowByID("no-such-id")
	if err != nil || f2 != nil {
		t.Fatalf("不存在的流应返回 (nil,nil)，得 %v, %v", f2, err)
	}
	// body 惰性回查
	body, err := w.LoadBody(ids[0], "resp")
	if err != nil || string(body) == "" {
		t.Fatalf("LoadBody = %d 字节, %v", len(body), err)
	}
}

func TestRenameTagMergeAndRename(t *testing.T) {
	w := archiveWriter(t)
	// 两个标签：A 2 条（a00/a01）、B 3 条（b00/b01/b02），无重叠
	tagA, idsA := seedTagged(t, w, "a", "标签A", 2)
	tagB, _ := seedTagged(t, w, "b", "标签B", 3)
	// a01 也打 B → 合并时 B 新增 1 条（a00），a01 本就在 B
	if added, err := w.AddFlowTags([]string{idsA[1]}, tagB.ID, time.Now().UnixMilli()); err != nil || added != 1 {
		t.Fatalf("AddFlowTags: added=%d, %v（期望新增 1）", added, err)
	}

	// 1) 改名到全新名字：id 变化、关联迁移、计数不变
	affected, err := w.RenameTag(tagA.ID, "标签A改名")
	if err != nil {
		t.Fatalf("RenameTag: %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("受影响流 = %d，期望 2", len(affected))
	}
	tags, _ := w.ListTags()
	if len(tags) != 2 {
		t.Fatalf("改名后标签数 = %d，期望 2", len(tags))
	}
	var renamed *TagInfo
	for i := range tags {
		if tags[i].Name == "标签A改名" {
			renamed = &tags[i]
		}
	}
	if renamed == nil || renamed.Count != 2 {
		t.Fatalf("改名后标签未找到或计数错误: %+v", tags)
	}
	if renamed.ID == tagA.ID {
		t.Fatal("改名后 id（hash）应变化")
	}

	// 2) 合并：把改名后的标签再改成「标签B」→ 并入 B，旧标签删除
	affected2, err := w.RenameTag(renamed.ID, "标签B")
	if err != nil {
		t.Fatalf("RenameTag 合并: %v", err)
	}
	if len(affected2) != 2 {
		t.Fatalf("合并受影响流 = %d，期望 2", len(affected2))
	}
	tags2, _ := w.ListTags()
	if len(tags2) != 1 || tags2[0].ID != tagB.ID {
		t.Fatalf("合并后应仅剩标签 B，得 %+v", tags2)
	}
	// B 计数：原 B 3 条 + 共享 a01 = 4；合并迁入 A 独有 a00 → 5
	if tags2[0].Count != 5 {
		t.Fatalf("合并后 B 计数 = %d，期望 5", tags2[0].Count)
	}
	if n, _ := w.CountTaggedFlows(); n != 5 {
		t.Fatalf("合并后已标记流总数 = %d，期望 5（去重）", n)
	}

	// 3) 不存在的标签改名报错
	if _, err := w.RenameTag(TagID("幽灵标签"), "x"); err == nil {
		t.Fatal("删除的标签改名应报错")
	}
}

func TestDeleteTagKeepAndCascade(t *testing.T) {
	w := archiveWriter(t)
	tagA, idsA := seedTagged(t, w, "del", "待删标签", 3)
	tagB, _ := seedTagged(t, w, "keep", "保留标签", 2)
	// idsA[2] 同时属于 B（删 A 连流时应保留）
	if added, err := w.AddFlowTags([]string{idsA[2]}, tagB.ID, time.Now().UnixMilli()); err != nil || added != 1 {
		t.Fatalf("AddFlowTags: added=%d, %v（期望新增 1）", added, err)
	}

	// 1) deleteFlows=false：只删关联/标签，流全部保留
	deleted, err := w.DeleteTag(tagB.ID, false)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("仅删关联不应删流，得 %d, %v", len(deleted), err)
	}
	if ok, _ := w.FlowExists(idsA[2]); !ok {
		t.Fatal("保留关联删除后流应仍在库")
	}
	// 重新打 B 供下一步（验证标签可重建）
	tagB2, _ := w.UpsertTag("保留标签")
	if added, err := w.AddFlowTags([]string{idsA[2]}, tagB2.ID, time.Now().UnixMilli()); err != nil || added != 1 {
		t.Fatalf("重建标签关联: added=%d, %v（期望新增 1）", added, err)
	}

	// 2) deleteFlows=true：删 A 独有 2 条流（idsA[0]/idsA[1]）+ bodies；idsA[2] 被 B 引用保留
	cascade, err := w.DeleteTag(tagA.ID, true)
	if err != nil {
		t.Fatalf("DeleteTag cascade: %v", err)
	}
	if len(cascade) != 2 {
		t.Fatalf("级联删流 = %d，期望 2（共享流保留）", len(cascade))
	}
	if ok, _ := w.FlowExists(idsA[0]); ok {
		t.Fatal("idsA[0] 应已物理删除")
	}
	if ok, _ := w.FlowExists(idsA[2]); !ok {
		t.Fatal("idsA[2] 仍被 B 引用，应保留")
	}
	if b, _ := w.LoadBody(idsA[0], "resp"); b != nil {
		t.Fatal("被删流 body 应级联清除")
	}
	// 标签行已删
	tags, _ := w.ListTags()
	for _, tg := range tags {
		if tg.ID == tagA.ID {
			t.Fatal("标签 A 应已删除")
		}
	}
}

// TestRetentionExcludesTagged 按龄/按体积保留清理均不得删除已标记流（设计 §4.6）。
func TestRetentionExcludesTagged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prism.db")
	// 录制 Writer：1 天保留（按龄清理路径）
	rec, err := Open(path, 1, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec.Start()

	oldTagged := testFlow("old-tagged", "old.com", capture.StateDone, "tagged-body")
	oldTagged.Timing.Start = time.Now().Add(-72 * time.Hour)
	oldPlain := testFlow("old-plain", "old.com", capture.StateDone, "plain-body")
	oldPlain.Timing.Start = time.Now().Add(-72 * time.Hour)
	rec.Enqueue(oldTagged)
	rec.Enqueue(oldPlain)
	waitWritten(t, rec, 2)
	rec.Close()

	// 归档 Writer 打开同库：给旧流打标签（模拟「打标即归档、不受保留策略影响」）
	arch, err := OpenArchive(path)
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	tag, err := arch.UpsertTag("旧流归档")
	if err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	if added, err := arch.AddFlowTags([]string{"old-tagged"}, tag.ID, time.Now().UnixMilli()); err != nil || added != 1 {
		t.Fatalf("AddFlowTags: added=%d, %v（期望新增 1）", added, err)
	}
	arch.Close()

	// 重开录制 Writer 跑按龄清理
	rec2, err := Open(path, 1, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer rec2.Close()
	rec2.Start()
	if err := rec2.Retention(); err != nil {
		t.Fatalf("Retention: %v", err)
	}
	if ok, _ := rec2.FlowExists("old-plain"); ok {
		t.Fatal("未标记旧流应被按龄清理")
	}
	if ok, _ := rec2.FlowExists("old-tagged"); !ok {
		t.Fatal("已标记旧流不得被按龄清理（标签=归档）")
	}
}

// TestTagIDForNameDoubleMatch L1：tagIDForName 必须 id+name 双匹配。
// 手工插入一个 12 位 id 被异名占用、真实标签为 16 位升级 id 的场景：
// UpsertTag 旧实现仅按 id COUNT 会误返回占用者 id；双匹配实现应正确解析 16 位行。
func TestTagIDForNameDoubleMatch(t *testing.T) {
	w := archiveWriter(t)
	name := "碰撞标签"
	shortID := hashTagName(name, 12)
	longID := hashTagName(name, 16)
	if shortID == longID {
		t.Fatal("测试前提不成立：12/16 位 id 不应相同")
	}
	// 手工构造占用行（异名）+ 16 位升级行
	if err := w.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO tags(id,name,created_at,last_used_at) VALUES(?,?,?,?)`,
			shortID, "占用者标签", 1, 1); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO tags(id,name,created_at,last_used_at) VALUES(?,?,?,?)`,
			longID, name, 2, 2)
		return err
	}); err != nil {
		t.Fatalf("构造碰撞场景: %v", err)
	}
	// UpsertTag 应命中 16 位真实行并刷新其 last_used_at，而不是占用者行
	tag, err := w.UpsertTag(name)
	if err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	if tag.ID != longID {
		t.Fatalf("应解析到 16 位真实 id，得 %s（占用者 %s）", tag.ID, shortID)
	}
	// 占用者行不受影响
	var occupyUsed int64
	if err := w.db.QueryRow(`SELECT last_used_at FROM tags WHERE id=?`, shortID).Scan(&occupyUsed); err != nil {
		t.Fatalf("查占用者: %v", err)
	}
	if occupyUsed != 1 {
		t.Fatalf("占用者行的 last_used_at 不应被刷新，得 %d", occupyUsed)
	}
}

// TestOpenReadOnlyNoCreate L2：只读打开不存在的库不创建文件、返回 ErrDBNotExist；
// 已存在库只读连接可查但拒绝写（query_only）。
func TestOpenReadOnlyNoCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "ro.db")
	if _, err := OpenReadOnly(path); !errors.Is(err, ErrDBNotExist) {
		t.Fatalf("不存在库应返回 ErrDBNotExist，得 %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("只读打开不得创建库文件，Stat = %v", err)
	}

	// 建一个真库写条标签后关闭
	rw, err := OpenArchive(path)
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if _, err := rw.UpsertTag("只读测试"); err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	rw.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly 既有库: %v", err)
	}
	defer ro.Close()
	tags, err := ro.ListTags()
	if err != nil || len(tags) != 1 {
		t.Fatalf("只读连接应可查标签，得 %d 个 %v", len(tags), err)
	}
	// query_only：写必须被拒绝
	if _, err := ro.db.Exec(`CREATE TABLE should_fail(x)`); err == nil {
		t.Fatal("只读连接不应允许 DDL/写操作")
	}
}

// TestTombstoneFilterAtConsumer M2：删除前已滞留队列的流，在消费侧被墓碑回调过滤，
// 不会经 ON CONFLICT 复活已删数据。
func TestTombstoneFilterAtConsumer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tomb.db")
	w, err := Open(path, 0, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.Start()

	// 先暂停消费，把流压入队列滞留（确认两条都确实滞留在队列中，避免暂停前被消费的竞态）
	w.setPaused(true)
	f1 := testFlow("q-keep", "keep.com", capture.StateDone, `{"k":1}`)
	f2 := testFlow("q-dead", "dead.com", capture.StateDone, `{"d":1}`)
	w.Enqueue(f1)
	w.Enqueue(f2)
	deadline := time.Now().Add(2 * time.Second)
	for w.queued() < 2 && time.Now().Before(deadline) {
		// 极端竞态下暂停生效前已有流被消费：重新补入直到两条都滞留
		if w.queued() < 2 {
			w.Enqueue(f1)
			w.Enqueue(f2)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w.queued() < 2 {
		t.Fatalf("暂停后队列应滞留 2 条，实际 %d", w.queued())
	}

	// 注入墓碑：f2 在删除前已入队，出队时必须被剔除
	w.SetTombstoneFilter(func(flowID string) bool { return flowID == "q-dead" })
	w.setPaused(false)

	// 等待 f1 落盘；f2 永不落盘
	waitWritten(t, w, 1)
	w.Close()
	// Close 后连接已关，重开只读库验证 f2 未被写入
	ro, err := OpenArchive(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ro.Close()
	if ok, _ := ro.FlowExists("q-dead"); ok {
		t.Fatal("墓碑流在消费侧过滤失败：已被写入（会导致复活）")
	}
	if ok, _ := ro.FlowExists("q-keep"); !ok {
		t.Fatal("非墓碑流应正常落盘")
	}
}

// TestAddFlowTagsReturnsAdded L4：返回值只计新增关联，重复关联不计。
func TestAddFlowTagsReturnsAdded(t *testing.T) {
	w := archiveWriter(t)
	tag, _ := w.UpsertTag("计数标签")
	f := testFlow("cnt-1", "cnt.com", capture.StateDone, "x")
	if err := w.ArchiveFlows([]*capture.Flow{f}); err != nil {
		t.Fatalf("ArchiveFlows: %v", err)
	}
	added1, err := w.AddFlowTags([]string{"cnt-1", "cnt-missing"}, tag.ID, time.Now().UnixMilli())
	if err != nil || added1 != 2 {
		t.Fatalf("首次关联应新增 2，得 %d, %v", added1, err)
	}
	added2, err := w.AddFlowTags([]string{"cnt-1", "cnt-missing"}, tag.ID, time.Now().UnixMilli())
	if err != nil || added2 != 0 {
		t.Fatalf("重复关联应新增 0，得 %d, %v", added2, err)
	}
	added3, err := w.AddFlowTags([]string{"cnt-1", "cnt-2"}, tag.ID, time.Now().UnixMilli())
	if err != nil || added3 != 1 {
		t.Fatalf("混合关联应新增 1，得 %d, %v", added3, err)
	}
}

// seedTimed 归档 n 条指定 started_at 的流（不打标签），返回按时间升序的 id 列表。
func seedTimed(t *testing.T, w *Writer, prefix string, starts []int64) []string {
	t.Helper()
	flows := make([]*capture.Flow, 0, len(starts))
	ids := make([]string, 0, len(starts))
	for i, ts := range starts {
		id := fmt.Sprintf("time-%s-%02d", prefix, i)
		f := testFlow(id, "timed.com", capture.StateDone, "x")
		f.Timing.Start = time.UnixMilli(ts)
		flows = append(flows, f)
		ids = append(ids, id)
	}
	if err := w.ArchiveFlows(flows); err != nil {
		t.Fatalf("ArchiveFlows: %v", err)
	}
	return ids
}

// TestNormalizeScope scope 归一化：空→archived，非法报错。
func TestNormalizeScope(t *testing.T) {
	if s, err := NormalizeScope(""); err != nil || s != ScopeArchived {
		t.Fatalf("空 scope 应归一 archived，得 %q %v", s, err)
	}
	if s, err := NormalizeScope("all"); err != nil || s != ScopeAll {
		t.Fatalf("all 应保留，得 %q %v", s, err)
	}
	if _, err := NormalizeScope("weird"); err == nil {
		t.Fatal("非法 scope 应报错")
	}
}

// TestScopeMatrix M12.1 需求5：archived 只含打标流，all 含库内全部流；具体标签恒为打标流。
func TestScopeMatrix(t *testing.T) {
	w := archiveWriter(t)
	tag, _ := seedTagged(t, w, "scope", "范围标签", 3)
	// 额外归档 2 条未打标流（时间夹在打标流之间不影响范围语义）
	plain := seedTimed(t, w, "plain", []int64{
		time.Now().Add(-time.Hour).UnixMilli(),
		time.Now().Add(-2 * time.Hour).UnixMilli(),
	})

	// tagID=all：archived=3（仅打标流），all=5（全部）
	if n, err := w.CountFlows("all", "archived", 0, 0, ReviewListOpts{}); err != nil || n != 3 {
		t.Fatalf("CountFlows all/archived = %d, %v（期望 3）", n, err)
	}
	if n, err := w.CountFlows("all", "all", 0, 0, ReviewListOpts{}); err != nil || n != 5 {
		t.Fatalf("CountFlows all/all = %d, %v（期望 5）", n, err)
	}
	if n, _ := w.CountAllFlows(); n != 5 {
		t.Fatalf("CountAllFlows = %d（期望 5）", n)
	}
	if n, _ := w.CountTaggedFlows(); n != 3 {
		t.Fatalf("CountTaggedFlows = %d（期望 3）", n)
	}
	rows, err := w.FlowsByTag("all", "archived", 0, 0, 200, 0, ReviewListOpts{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("FlowsByTag all/archived = %d, %v", len(rows), err)
	}
	rows, err = w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{})
	if err != nil || len(rows) != 5 {
		t.Fatalf("FlowsByTag all/all = %d, %v", len(rows), err)
	}
	// 具体标签：scope 被忽略，恒为该标签 3 条（打标流），未打标 plain 不出现
	rows, err = w.FlowsByTag(tag.ID, "all", 0, 0, 200, 0, ReviewListOpts{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("具体标签 scope=all = %d, %v（期望 3）", len(rows), err)
	}
	for _, f := range rows {
		for _, pid := range plain {
			if string(f.ID) == pid {
				t.Fatalf("未打标流 %s 不应出现在具体标签视图", pid)
			}
		}
	}
	// tagID=all + 非法 scope 报错
	if _, err := w.FlowsByTag("all", "bad", 0, 0, 10, 0, ReviewListOpts{}); err == nil {
		t.Fatal("非法 scope 应报错")
	}
}

// TestKeywordQuery q 子串过滤：method/host/path 三列 OR，与分页 total 同谓词；
// q 不参与直方图底图（此处只验列表/计数口径）。
func TestKeywordQuery(t *testing.T) {
	w := archiveWriter(t)
	// 5 条流，host/path 各有差异（seedTimed 内默认 method=GET，见其实现）
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).UnixMilli()
	ids := seedTimed(t, w, "kw", []int64{base, base + 60000, base + 120000, base + 180000, base + 240000})
	// 直接改两条流的 method/host/path：更新 flows.data 中的 JSON 成本高，
	// 这里通过 DB 更新独立列（method/host/path 是冗余列，与 data 解耦）
	for i, upd := range []struct{ method, host, path string }{
		{"POST", "api.example.com", "/v1/login"},
		{"GET", "cdn.example.com", "/assets/app.js"},
	} {
		if _, err := w.db.Exec(`UPDATE flows SET method=?, host=?, path=? WHERE id=?`, upd.method, upd.host, upd.path, ids[i]); err != nil {
			t.Fatalf("更新列失败: %v", err)
		}
	}
	// host 子串命中 1 条
	if n, _ := w.CountFlows("all", "all", 0, 0, ReviewListOpts{Q: "cdn.example"}); n != 1 {
		t.Fatalf("q=cdn.example 应 1 条，得 %d", n)
	}
	// method 命中：POST 1 条
	if n, _ := w.CountFlows("all", "all", 0, 0, ReviewListOpts{Q: "post"}); n != 1 {
		t.Fatalf("q=post（大小写不敏感）应 1 条，得 %d", n)
	}
	// path 子串：/v1/ 命中 1 条
	rows, err := w.FlowsByTag("all", "all", 0, 0, 200, 0, ReviewListOpts{Q: "/v1/"})
	if err != nil || len(rows) != 1 || string(rows[0].ID) != ids[0] {
		t.Fatalf("q=/v1/ 列表 = %d, %v（期望仅 ids[0]）", len(rows), err)
	}
	// 元字符转义：% 不应扩大匹配
	if n, _ := w.CountFlows("all", "all", 0, 0, ReviewListOpts{Q: "%"}); n != 0 {
		t.Fatalf("q=%% 应 0 条（元字符已转义），得 %d", n)
	}
	// 空白 q 等同不过滤
	if n, _ := w.CountFlows("all", "all", 0, 0, ReviewListOpts{Q: "   "}); n != 5 {
		t.Fatalf("空白 q 应 5 条，得 %d", n)
	}
}

// TestTimeWindow M12.1 需求7：start/end 含头尾、半开窗口，与 total 同谓词。
func TestTimeWindow(t *testing.T) {
	w := archiveWriter(t)
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).UnixMilli()
	min := int64(60 * 1000)
	// 5 条流：base, +1m, +2m, +3m, +4m
	starts := []int64{base, base + min, base + 2*min, base + 3*min, base + 4*min}
	ids := seedTimed(t, w, "win", starts)

	// 含头尾：start=第2条 end=第4条 → 3 条（两端都含）
	if n, _ := w.CountFlows("all", "all", starts[1], starts[3], ReviewListOpts{}); n != 3 {
		t.Fatalf("闭窗 [1,3] 应 3 条，得 %d", n)
	}
	rows, err := w.FlowsByTag("all", "all", starts[1], starts[3], 200, 0, ReviewListOpts{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("闭窗列表 = %d, %v", len(rows), err)
	}
	// 倒序：第4条在前
	if string(rows[0].ID) != ids[3] || string(rows[2].ID) != ids[1] {
		t.Fatalf("窗口内倒序错误: %s,%s", rows[0].ID, rows[2].ID)
	}
	// 半开：仅下限 start=第3条 → 3 条（第3/4/5）
	if n, _ := w.CountFlows("all", "all", starts[2], 0, ReviewListOpts{}); n != 3 {
		t.Fatalf("[第3条,+∞) 应 3 条，得 %d", n)
	}
	// 半开：仅上限 end=第2条 → 2 条（第1/2）
	if n, _ := w.CountFlows("all", "all", 0, starts[1], ReviewListOpts{}); n != 2 {
		t.Fatalf("(-∞,第2条] 应 2 条，得 %d", n)
	}
	// 窗口外：无结果
	if n, _ := w.CountFlows("all", "all", starts[4]+min, 0, ReviewListOpts{}); n != 0 {
		t.Fatalf("全在窗口之后应 0 条，得 %d", n)
	}
	// 含头尾等值：单毫秒窗口落在某条流上 → 1 条
	if n, _ := w.CountFlows("all", "all", starts[0], starts[0], ReviewListOpts{}); n != 1 {
		t.Fatalf("等值闭窗应 1 条，得 %d", n)
	}
}

// TestHistogramBucketing M12.1 P0：分桶守恒（不整除/单流/空库/溢出并入末桶）。
func TestHistogramBucketing(t *testing.T) {
	// 空库：空切片、边界 0
	wEmpty := archiveWriter(t)
	t0, t1, hist, err := wEmpty.Histogram("all", "all", 0, 0, 120)
	if err != nil {
		t.Fatalf("空库 Histogram: %v", err)
	}
	if len(hist) != 0 || t0 != 0 || t1 != 0 {
		t.Fatalf("空库应返回空切片与 0 边界，得 len=%d [%d,%d]", len(hist), t0, t1)
	}

	// 单流库（MAX==MIN）：退化单桶 width=1，count=1，禁止除零
	wOne := archiveWriter(t)
	only := time.Now().UnixMilli()
	seedTimed(t, wOne, "one", []int64{only})
	_, _, histOne, err := wOne.Histogram("all", "all", 0, 0, 120)
	if err != nil {
		t.Fatalf("单流 Histogram: %v", err)
	}
	if len(histOne) != 1 || histOne[0].Count != 1 {
		t.Fatalf("单流应退化单桶 count=1，得 %+v", histOne)
	}

	// 多流库：全域 1000ms、buckets=120 → width=ceil(1000/120)=9，Σcount 守恒=6
	w := archiveWriter(t)
	base := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC).UnixMilli()
	span := int64(1000)
	starts := []int64{
		base, base + 100, base + 200, base + 500, base + 900, base + span,
	}
	seedTimed(t, w, "multi", starts)
	t0, t1, hist, err = w.Histogram("all", "all", 0, 0, 120)
	if err != nil {
		t.Fatalf("多流 Histogram: %v", err)
	}
	if t0 != base || t1 != base+span {
		t.Fatalf("全域边界 = [%d,%d]，期望 [%d,%d]", t0, t1, base, base+span)
	}
	sum := 0
	for _, b := range hist {
		sum += b.Count
	}
	if sum != 6 {
		t.Fatalf("Σcount=%d，期望 6（分桶守恒）", sum)
	}
	if len(hist) > 120 {
		t.Fatalf("桶数 %d 不得超过请求 120", len(hist))
	}
	// 末桶必须接住 base+span（不整除溢出点）：width=9 → b=1000/9=111
	if hist[len(hist)-1].Count == 0 {
		t.Fatal("末桶 count=0：base+span 溢出点未并入末桶")
	}
	// 桶区间单调、相邻衔接、首桶从 t0 起
	if hist[0].T0 != t0 {
		t.Fatalf("首桶 T0=%d，应为 t0=%d", hist[0].T0, t0)
	}
	for i := 1; i < len(hist); i++ {
		if hist[i].T0 != hist[i-1].T1 {
			t.Fatalf("桶 %d 不衔接：%d != %d", i, hist[i].T0, hist[i-1].T1)
		}
	}
	if hist[len(hist)-1].T1 != t1 {
		t.Fatalf("末桶 T1=%d 应为全域 MAX=%d", hist[len(hist)-1].T1, t1)
	}

	// 整除场景：span=1000、buckets=10 → width=100、n=11（ceil(span+1)/width），
	// n>buckets 钳到 10，base+1000 点 b=10 溢出并入末桶，仍守恒。
	_, _, hist2, err := w.Histogram("all", "all", 0, 0, 10)
	if err != nil || len(hist2) != 10 {
		t.Fatalf("整除非溢出桶数应钳为 10，得 %d, %v", len(hist2), err)
	}
	sum = 0
	for _, b := range hist2 {
		sum += b.Count
	}
	if sum != 6 {
		t.Fatalf("整除分桶 Σcount=%d，期望 6", sum)
	}

	// buckets 钳制：>500 钳 500（桶数不越界）
	_, _, histBig, err := w.Histogram("all", "all", 0, 0, 5000)
	if err != nil || len(histBig) > 500 {
		t.Fatalf("buckets 超上限应钳 500，得 %d, %v", len(histBig), err)
	}
}

// TestHistogramScopeAndWindow 底图范围跟随 tagID+scope；窗口分桶；窗口内无数据空切片。
func TestHistogramScopeAndWindow(t *testing.T) {
	w := archiveWriter(t)
	tag, _ := seedTagged(t, w, "hist", "直方图标签", 3)
	seedTimed(t, w, "hplain", []int64{
		time.Now().Add(-30 * time.Hour).UnixMilli(),
		time.Now().Add(-60 * time.Hour).UnixMilli(),
	})

	// archived 全域：Σcount=3（不含 2 条未打标）
	_, _, hist, err := w.Histogram("all", "archived", 0, 0, 120)
	if err != nil {
		t.Fatalf("archived Histogram: %v", err)
	}
	sum := 0
	for _, b := range hist {
		sum += b.Count
	}
	if sum != 3 {
		t.Fatalf("archived 底图 Σcount=%d，期望 3（同谓词）", sum)
	}
	// all 全域：Σcount=5
	_, _, histAll, err := w.Histogram("all", "all", 0, 0, 120)
	if err != nil {
		t.Fatalf("all Histogram: %v", err)
	}
	sum = 0
	for _, b := range histAll {
		sum += b.Count
	}
	if sum != 5 {
		t.Fatalf("all 底图 Σcount=%d，期望 5", sum)
	}
	// 具体标签：Σcount=3
	_, _, histTag, err := w.Histogram(tag.ID, "all", 0, 0, 120)
	if err != nil {
		t.Fatalf("tag Histogram: %v", err)
	}
	sum = 0
	for _, b := range histTag {
		sum += b.Count
	}
	if sum != 3 {
		t.Fatalf("具体标签底图 Σcount=%d，期望 3", sum)
	}
	// 窗口（双端）分桶：取一个远早于打标流的区间 → 回显窗口形状的全零桶
	// （坐标系稳定；无窗口空库才返回空切片）
	far := time.Now().Add(-90 * 24 * time.Hour).UnixMilli()
	t0, t1, h, err := w.Histogram("all", "all", far, far+3600_000, 120)
	if err != nil {
		t.Fatalf("窗口 Histogram: %v", err)
	}
	if t0 != far || t1 != far+3600_000 || len(h) == 0 {
		t.Fatalf("窗口应回显窗口边界并返回桶形，得 len=%d [%d,%d]", len(h), t0, t1)
	}
	for _, b := range h {
		if b.Count != 0 {
			t.Fatalf("窗口内无数据应全零桶，得 %+v", b)
		}
	}
}

// TestLoadFlowsByIDs AI 取数口径（M13 计划 P2-6）：started_at 降序与入参顺序无关、
// 缺失 id 缺席（失效即不返回）、重复 id 去重、空 ids 空结果、历史标记（Source/Pinned）。
func TestLoadFlowsByIDs(t *testing.T) {
	w := archiveWriter(t)
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC).UnixMilli()
	ids := seedTimed(t, w, "aid", []int64{base, base + 1000, base + 2000}) // ids[2] 最新

	// 乱序 + 含不存在 id + 重复 id：返回降序、缺失缺席、去重
	got, err := w.LoadFlowsByIDs([]string{ids[1], "gone-1", ids[2], ids[0], "gone-2", ids[1]})
	if err != nil {
		t.Fatalf("LoadFlowsByIDs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("返回流数 = %d，期望 3（缺失缺席+去重）", len(got))
	}
	for i, want := range []string{ids[2], ids[1], ids[0]} {
		if string(got[i].ID) != want {
			t.Fatalf("第 %d 条 = %s，期望 %s（降序）", i, got[i].ID, want)
		}
		if got[i].Source != capture.SourceHistory || got[i].Pinned {
			t.Fatalf("流 %s 标记错误: source=%v pinned=%v", got[i].ID, got[i].Source, got[i].Pinned)
		}
	}

	// 全失效（仅不存在 id）：空结果不报错
	miss, err := w.LoadFlowsByIDs([]string{"nope-a", "nope-b"})
	if err != nil || len(miss) != 0 {
		t.Fatalf("全失效应空结果不报错，得 %d, %v", len(miss), err)
	}

	// 空 ids：空结果
	empty, err := w.LoadFlowsByIDs(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("空 ids 应空结果，得 %d, %v", len(empty), err)
	}
}
