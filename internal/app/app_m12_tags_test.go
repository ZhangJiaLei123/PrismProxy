package app

// M12 标签与数据复盘绑定层测试（设计 §4.3-§4.5、AC12/AC13/AC14/AC15）。

import (
	"strings"
	"sync"
	"testing"
	"time"

	"prismproxy/internal/settings"
)

// tagWait 轮询等待条件成立（打标回显/落盘为同步路径，仅 closeArchive 等收尾需要轮询兜底）
func tagWait(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", msg)
}

// metaByID 从 ListFlows 找指定 id 的 meta
func metaByID(a *App, id string) *FlowMeta {
	for _, m := range a.ListFlows() {
		if m.ID == id {
			mm := m
			return &mm
		}
	}
	return nil
}

// TestTagFlowsBasic 打标主路径：归档落库 + tagIndex 回显 + 标签列表计数 + 复盘查询闭环。
func TestTagFlowsBasic(t *testing.T) {
	a := newTestApp(t)
	defer a.closeArchive()

	a.st.Add(testFlow("m12-1"))
	a.st.Add(testFlow("m12-2"))

	res, err := a.TagFlows([]string{"m12-1", "m12-2", "m12-1", " "}, "登录流程", false)
	if err != nil {
		t.Fatalf("TagFlows: %v", err)
	}
	if res.Tagged != 2 || res.Archived != 2 || res.Skipped != 0 {
		t.Fatalf("统计错误: tagged=%d archived=%d skipped=%d", res.Tagged, res.Archived, res.Skipped)
	}
	if res.Tag.Name != "登录流程" || res.Tag.Count != 2 {
		t.Fatalf("标签字典项错误: %+v", res.Tag)
	}

	// 回显：meta.Tags 即时带标签（tagIndex COW）
	tagWait(t, func() bool {
		m := metaByID(a, "m12-1")
		return m != nil && len(m.Tags) == 1 && m.Tags[0] == "登录流程"
	}, "meta.Tags 回显")

	// 标签列表
	tags, err := a.ListTags()
	if err != nil || len(tags) != 1 || tags[0].Count != 2 {
		t.Fatalf("ListTags = %+v, %v", tags, err)
	}

	// 复盘列表（all 与指定标签）
	flows, total, err := a.ReviewFlowList("all", 200, 0)
	if err != nil || total != 2 || len(flows) != 2 {
		t.Fatalf("ReviewFlowList all = %d 条 total=%d, %v", len(flows), total, err)
	}
	flows, total, err = a.ReviewFlowList(res.Tag.ID, 200, 0)
	if err != nil || total != 2 || len(flows) != 2 {
		t.Fatalf("ReviewFlowList 标签 = %d 条 total=%d, %v", len(flows), total, err)
	}
	// 分页：limit=1 offset=1 → 1 条
	page, _, err := a.ReviewFlowList(res.Tag.ID, 1, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("分页 = %d 条, %v", len(page), err)
	}
	// 复盘详情 + 正文（走归档库，含库查标签补齐）
	d, err := a.ReviewFlowDetail("m12-1")
	if err != nil || d == nil {
		t.Fatalf("ReviewFlowDetail: %v", err)
	}
	if len(d.Tags) != 1 || d.Tags[0] != "登录流程" {
		t.Fatalf("复盘详情标签 = %v", d.Tags)
	}
	body, err := a.ReviewFlowBody("m12-1", "resp")
	if err != nil {
		t.Fatalf("ReviewFlowBody: %v", err)
	}
	if string(body.Body) != `{"ok":true}` {
		t.Fatalf("复盘正文解压 = %q", string(body.Body))
	}
}

// TestTagFlowsValidation 入参校验：空名/超长/无 id 均报错；非法标签 id 查询报错。
func TestTagFlowsValidation(t *testing.T) {
	a := newTestApp(t)
	defer a.closeArchive()
	a.st.Add(testFlow("v1"))

	if _, err := a.TagFlows([]string{"v1"}, "   ", false); err == nil {
		t.Fatal("空白标签名应报错")
	}
	if _, err := a.TagFlows([]string{"v1"}, strings.Repeat("长", tagNameMaxLen+1), false); err == nil {
		t.Fatal("超长标签名应报错")
	}
	if _, err := a.TagFlows(nil, "x", false); err == nil {
		t.Fatal("空 id 列表应报错")
	}
	// 合法边界：40 字符应通过
	if _, err := a.TagFlows([]string{"v1"}, strings.Repeat("签", tagNameMaxLen), false); err != nil {
		t.Fatalf("40 字符标签应通过: %v", err)
	}
	// 内存与库中均不可得 → skipped
	res, err := a.TagFlows([]string{"ghost-id"}, "幽灵", false)
	if err != nil || res.Skipped != 1 || res.Tagged != 0 {
		t.Fatalf("不可得流应计 skipped: %+v, %v", res, err)
	}
	if _, _, err := a.ReviewFlowList("not-a-tag-id", 10, 0); err == nil {
		t.Fatal("非法标签 id 查询应报错")
	}
}

// TestTagFlowsAutoClear autoClear=true：打标后列表清空（置顶保留），且被清流不复活。
func TestTagFlowsAutoClear(t *testing.T) {
	a := newTestApp(t)
	defer a.closeArchive()
	a.st.Add(testFlow("ac1"))
	a.st.Add(testFlow("ac2"))
	// 置顶一条（应保留）
	pinned := testFlow("ac-pin")
	pinned.Pinned = true
	a.st.Add(pinned)

	res, err := a.TagFlows([]string{"ac1", "ac2"}, "标记后清空", true)
	if err != nil || res.Tagged != 2 {
		t.Fatalf("TagFlows autoClear: %+v, %v", res, err)
	}
	tagWait(t, func() bool { return len(a.st.List()) == 1 }, "autoClear 后仅剩置顶")
	if left := a.st.List(); len(left) != 1 || left[0].ID != "ac-pin" {
		t.Fatalf("autoClear 应保留置顶 1 条，得 %d", len(left))
	}
	// 归档库中两条仍在（标签=归档，清空列表不影响已归档数据）
	flows, total, err := a.ReviewFlowList("all", 10, 0)
	if err != nil || total != 2 || len(flows) != 2 {
		t.Fatalf("归档库应保留 2 条，得 %d (total=%d), %v", len(flows), total, err)
	}
}

// TestTagRenameAndDelete 重命名（跨窗口回显）与删除（仅关联/连删两路径 + tombstone）。
func TestTagRenameAndDelete(t *testing.T) {
	a := newTestApp(t)
	defer a.closeArchive()
	a.st.Add(testFlow("rd1"))
	a.st.Add(testFlow("rd2"))

	res, err := a.TagFlows([]string{"rd1", "rd2"}, "旧名", false)
	if err != nil {
		t.Fatalf("TagFlows: %v", err)
	}
	// 重命名：tagIndex 即时刷新
	if err := a.RenameTagApp(res.Tag.ID, "新名字"); err != nil {
		t.Fatalf("RenameTagApp: %v", err)
	}
	tagWait(t, func() bool {
		m := metaByID(a, "rd1")
		return m != nil && len(m.Tags) == 1 && m.Tags[0] == "新名字"
	}, "重命名后回显")
	tags, _ := a.ListTags()
	if len(tags) != 1 || tags[0].Name != "新名字" {
		t.Fatalf("重命名后标签列表 = %+v", tags)
	}

	// 删除标签但保留流（flows=false）：关联解除、流仍在归档库
	tagID := tags[0].ID
	n, err := a.DeleteTagApp(tagID, false)
	if err != nil || n != 0 {
		t.Fatalf("DeleteTagApp(保留流) 删流数 = %d, %v", n, err)
	}
	tagWait(t, func() bool {
		m := metaByID(a, "rd1")
		return m != nil && len(m.Tags) == 0
	}, "删关联后徽章清空")
	if total := func() int {
		_, total, _ := a.ReviewFlowList("all", 10, 0)
		return total
	}(); total != 0 {
		t.Fatalf("标签删完后已标记流总数应为 0，得 %d", total)
	}
	// 流本身仍在归档库（可按 id 查详情）
	if d, err := a.ReviewFlowDetail("rd1"); err != nil || d == nil {
		t.Fatalf("保留流删除标签后流应仍可查: %v", err)
	}

	// 连删路径：重新打标后 DeleteTagApp(flows=true)
	res2, err := a.TagFlows([]string{"rd1", "rd2"}, "待连删", false)
	if err != nil {
		t.Fatalf("重新打标: %v", err)
	}
	n, err = a.DeleteTagApp(res2.Tag.ID, true)
	if err != nil || n != 2 {
		t.Fatalf("DeleteTagApp(连删) 删流数 = %d, %v", n, err)
	}
	if d, err := a.ReviewFlowDetail("rd1"); err == nil && d != nil {
		t.Fatal("连删后流应从归档库消失")
	}
}

// TestTombstoneBlocksReupsert AC13：连删流的后续录制 upsert 被 tombstone 拦截，不复活。
func TestTombstoneBlocksReupsert(t *testing.T) {
	a := newTestApp(t)
	// 开启录制持久化（onPersistEvent 仅在 persist 非 nil 时入队）
	if err := a.applyPersist(settingsPersistOn()); err != nil {
		t.Fatalf("applyPersist: %v", err)
	}
	defer a.stopPersist()

	a.st.Add(testFlow("tomb-1"))
	// 等待录制落盘
	po := a.currentPersist()
	persistWait(t, func() bool { return po.w.Written() >= 1 }, "录制流初始落盘")

	res, err := a.TagFlows([]string{"tomb-1"}, "连删标签", false)
	if err != nil {
		t.Fatalf("TagFlows: %v", err)
	}
	// 连删（流此时仍在内存 store）
	if _, err := a.DeleteTagApp(res.Tag.ID, true); err != nil {
		t.Fatalf("DeleteTagApp: %v", err)
	}
	if !a.isTombstoned("tomb-1") {
		t.Fatal("连删流应记入 tombstone")
	}
	// 模拟该流的后续 update（store 事件同步经订阅回调 onPersistEvent）：
	// 置顶切换产生 update 事件，若未拦截会被录制 Writer 重新 upsert 复活
	if err := a.SetFlowPinned("tomb-1", true); err != nil {
		t.Fatalf("SetFlowPinned: %v", err)
	}
	// 给异步队列一点时间，若未拦截流会被重新 upsert
	time.Sleep(300 * time.Millisecond)
	// 归档库仍查不到该流（未复活）
	if d, err := a.ReviewFlowDetail("tomb-1"); err == nil && d != nil {
		t.Fatal("tombstone 拦截失败：已删流被录制 upsert 复活")
	}
}

// settingsPersistOn 构造开启的持久化配置（RetainDays/MaxMB 给宽松值避免清理干扰）
func settingsPersistOn() settings.PersistConfig {
	return settings.PersistConfig{Enabled: true, RetainDays: 0, MaxMB: 0}
}

// TestTagFlowsConcurrentSwitchProject M1：在途打标与 closeArchive（项目切换/关闭）并发
// 不报错、不 panic、不死锁；closeArchive 必须等在途写完才 Close（-race 下验证）。
func TestTagFlowsConcurrentSwitchProject(t *testing.T) {
	a := newTestApp(t)
	for i := 0; i < 200; i++ {
		a.st.Add(testFlow("cm12-"+itoa(i)))
	}

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < 5; r++ {
				name := "并发标签" + itoa(g) + "-" + itoa(r)
				ids := []string{"cm12-" + itoa((g*5+r)%200), "cm12-" + itoa((g*5+r+1)%200)}
				// 「项目已切换」属注入代际后的预期拒绝，其余错误才失败
				if _, err := a.TagFlows(ids, name, false); err != nil &&
					!strings.Contains(err.Error(), "项目已切换") {
					t.Errorf("TagFlows 并发异常: %v", err)
					return
				}
			}
		}(g)
	}
	// 模拟切换链路：projGen++ → closeArchive（真实 switchProjectLocked 持 projMu 调用）
	for k := 0; k < 5; k++ {
		a.projGen.Add(1)
		a.closeArchive()
	}
	wg.Wait()
	// 收尾：closeArchive 后再切一代并关闭，确保全部在途被等待、不残留句柄
	a.projGen.Add(1)
	a.closeArchive()
}

// TestAcquireArchiveGenMismatch M1：archive 绑定旧代际时新代际请求不得借旧/新 writer 写库。
func TestAcquireArchiveGenMismatch(t *testing.T) {
	a := newTestApp(t)
	defer a.closeArchive() // 收尾关闭所有懒打开的 archive，释放 prism.db 句柄
	a.st.Add(testFlow("gen-1"))
	if _, err := a.TagFlows([]string{"gen-1"}, "初代标签", false); err != nil {
		t.Fatalf("首次打标: %v", err)
	}
	// 切换项目（未走完整 switchPersist，仅模拟代际推进 + 关旧 archive）
	a.projGen.Add(1)
	a.closeArchive()
	// 新代际下打标：acquireArchive 会为新项目懒打开新 archive，应成功（新项目库）。
	// 旧代际的 tagIndex 已被 resetTagIndex 清空，不污染新项目。
	a.st.Add(testFlow("gen-2"))
	res, err := a.TagFlows([]string{"gen-2"}, "新代标签", false)
	if err != nil {
		t.Fatalf("新代际打标应懒打开新项目 archive 并成功: %v", err)
	}
	if res.Tagged != 1 {
		t.Fatalf("新代际 tagged=%d，应为 1", res.Tagged)
	}
	if names := a.tagsOf("gen-1"); len(names) != 0 {
		t.Fatalf("旧项目流标签不应残留于新 tagIndex: %v", names)
	}
}

// TestTombstoneConsumerFilter M2 反向时序：删除前已滞留录制队列的批次，消费侧 flush
// 前过滤必须拦下——ON CONFLICT 不能让连删流复活。
func TestTombstoneConsumerFilter(t *testing.T) {
	a := newTestApp(t)
	if err := a.applyPersist(settingsPersistOn()); err != nil {
		t.Fatalf("applyPersist: %v", err)
	}
	defer a.stopPersist()
	po := a.currentPersist()

	// 先写一条正常流作为「消费仍在运转」的观察哨
	keeper := testFlow("m2-keep")
	po.w.Enqueue(keeper)
	persistWait(t, func() bool { return po.w.Written() >= 1 }, "观察哨流落盘")

	// 直接把待删流压入录制队列（绕过入队侧检查，模拟删除前已滞留的批次），
	// 并立即记墓碑——消费侧 flush 前必须过滤掉。
	victim := testFlow("m2-dead")
	po.w.Enqueue(victim)
	a.putTombstone([]string{"m2-dead"})

	// 等待观察哨后续批次/本批 flush 完成（队列排空）
	persistWait(t, func() bool {
		exists, _ := po.w.FlowExists("m2-keep")
		return exists
	}, "观察哨流可查")
	time.Sleep(300 * time.Millisecond) // 留足 flush 周期确认无延迟复活

	exists, err := po.w.FlowExists("m2-dead")
	if err != nil {
		t.Fatalf("FlowExists: %v", err)
	}
	if exists {
		t.Fatal("M2 消费侧过滤失败：删除前滞留队列的连删流被 flush 复活")
	}
}

// TestTombstoneBoundedAndUntombstone L5：墓碑集合 FIFO 有界淘汰 + 重新打标移出墓碑。
func TestTombstoneBoundedAndUntombstone(t *testing.T) {
	a := newTestApp(t)
	// 填充 cap+10 个 id，最旧 10 个应被淘汰
	ids := make([]string, 0, tombstoneCap+10)
	for i := 0; i < tombstoneCap+10; i++ {
		ids = append(ids, "tb-"+itoa(i))
	}
	a.putTombstone(ids)
	a.tombMu.Lock()
	n := len(a.deletedFlows)
	orderN := len(a.tombOrder)
	_, oldestAlive := a.deletedFlows["tb-0"]
	_, newestAlive := a.deletedFlows["tb-"+itoa(tombstoneCap+9)]
	a.tombMu.Unlock()
	if n != tombstoneCap || orderN != tombstoneCap {
		t.Fatalf("墓碑应有界为 %d，得 map=%d order=%d", tombstoneCap, n, orderN)
	}
	if oldestAlive {
		t.Fatal("最旧墓碑应被 FIFO 淘汰")
	}
	if !newestAlive {
		t.Fatal("最新墓碑应保留")
	}

	// 重新打标（untombstone）：移出后 isTombstoned=false，FIFO 序同步移除
	target := "tb-" + itoa(tombstoneCap+9)
	a.untombstone([]string{target})
	if a.isTombstoned(target) {
		t.Fatal("重新打标后应移出墓碑")
	}
	a.tombMu.Lock()
	inOrder := false
	for _, o := range a.tombOrder {
		if o == target {
			inOrder = true
		}
	}
	a.tombMu.Unlock()
	if inOrder {
		t.Fatal("移出墓碑后 FIFO 序中不应残留")
	}

	// resetTagIndex 清空（项目切换）
	a.resetTagIndex()
	if a.isTombstoned("tb-100") {
		t.Fatal("resetTagIndex 后墓碑应清空")
	}
}

// TestTagFlowsTaggedCountsNewLinks L4：重复打标 Tagged 只计新增关联数。
func TestTagFlowsTaggedCountsNewLinks(t *testing.T) {
	a := newTestApp(t)
	defer a.closeArchive()
	a.st.Add(testFlow("l4-1"))

	first, err := a.TagFlows([]string{"l4-1"}, "L4标签", false)
	if err != nil || first.Tagged != 1 {
		t.Fatalf("首次 tagged=%d, %v", first.Tagged, err)
	}
	second, err := a.TagFlows([]string{"l4-1"}, "L4标签", false)
	if err != nil {
		t.Fatalf("重复打标: %v", err)
	}
	// Tagged 口径=新增关联数（重复打标 0）；Archived=内存命中即归档（ON CONFLICT 重写），
	// 属既有设计语义，这里不约束。
	if second.Tagged != 0 {
		t.Fatalf("重复打标 tagged=%d，应为 0", second.Tagged)
	}
}

// itoa 测试内极简整数格式化（避免引入 strconv 到多处）
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// TestGetReviewURLGuard AC15/守卫：无项目报错；无 ctl（9595 被占）报明确错误。
func TestGetReviewURLGuard(t *testing.T) {
	// 无项目态
	a := newTestApp(t)
	a.proj = nil
	if _, err := a.GetReviewURL(); err == nil {
		t.Fatal("无项目应报错")
	}
	// 有项目但 ctl 未启动（模拟 9595 被占）
	a2 := newTestApp(t)
	if _, err := a2.GetReviewURL(); err == nil || !strings.Contains(err.Error(), "9595") {
		t.Fatalf("ctl 未启动应报端口提示，得 %v", err)
	}
}
