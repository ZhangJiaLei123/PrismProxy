package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"prismproxy/internal/capture"
	"prismproxy/internal/persist"
)

// M12 抓包标签与数据复盘（设计 §4.2-§4.5）。
//
// 锁序：projMu → archiveMu / tagIndex 写重建；tagIndex 为 copy-on-write +
// atomic.Pointer，读侧（toMeta）零锁。铁律：tagIndex 写侧重建（Store）之后再做
// pendUp 推送/st.Clear——Store 本身不触发任何 store 事件，重建完成后才允许触达
// store（onStoreEvent→toMeta 读到的已是新快照），杜绝重入/竞态（设计 §4.3）。

// tagNameMaxLen 标签名长度上限（对归一化展示名）
const tagNameMaxLen = 40

// tagBatchSize 打标分批事务大小（每批 flows/bodies 归档 + 关联写入，设计 §4.2-§4.3）
const tagBatchSize = 100

// TagInfo 标签字典项（Wails DTO；字段与 persist.TagInfo 对齐，前端历史标签/复盘侧栏用）
type TagInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Count      int    `json:"count"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

// TagResult TagFlows 返回统计
type TagResult struct {
	Tag      *TagInfo `json:"tag"`      // 标签字典项（含最新计数）
	Tagged   int      `json:"tagged"`   // 新增/刷新关联的流数（去重后）
	Archived int      `json:"archived"` // 本次实际写入 flows（归档）的流数
	Skipped  int      `json:"skipped"`  // 内存与库中均不可得、未能归档的流数
}

// tagsOf 读侧：从 tagIndex COW 快照取某流的标签名（零锁；拷贝切片防调用方修改）。
// 未命中视为无标签——列表热路径零 DB（设计 §4.5）。
func (a *App) tagsOf(flowID string) []string {
	p := a.tagIndex.Load()
	if p == nil {
		return nil
	}
	names := (*p)[flowID]
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// snapshotTagIndex 返回当前 tagIndex 快照 map（COW，不可变；nil 安全返回空 map）。
// 写侧重建时基于此拷贝出新 map，修改后 Store 原子替换。
func (a *App) snapshotTagIndex() map[string][]string {
	p := a.tagIndex.Load()
	if p == nil {
		return map[string][]string{}
	}
	src := *p
	dst := make(map[string][]string, len(src)+1)
	for k, v := range src {
		cp := make([]string, len(v))
		copy(cp, v)
		dst[k] = cp
	}
	return dst
}

// storeTagIndex 原子替换 tagIndex 快照（写侧唯一入口）。
func (a *App) storeTagIndex(m map[string][]string) {
	a.tagIndex.Store(&m)
}

// addTagToIndex COW 重建：把 tagName 并入一批 flowID 的标签集合（去重，新标签前置）。
// 调用方不得在 Store 之前触发 store 事件（设计铁律）。
func (a *App) addTagToIndex(ids []string, tagName string) {
	m := a.snapshotTagIndex()
	for _, id := range ids {
		names := m[id]
		exists := false
		for _, n := range names {
			if n == tagName {
				exists = true
				break
			}
		}
		if !exists {
			m[id] = append([]string{tagName}, names...) // 新标签在前（最近使用优先）
		}
	}
	a.storeTagIndex(m)
}

// rebuildTagsForIDs COW 重建：用归档库批量查询结果覆盖给定 ids 的标签集合
// （重命名/删除/合并后受影响流的名单刷新）。查询失败时保持原快照不动。
func (a *App) rebuildTagsForIDs(w *persist.Writer, ids []string) {
	if len(ids) == 0 {
		return
	}
	fresh, err := w.TagsForFlows(ids)
	if err != nil {
		return
	}
	m := a.snapshotTagIndex()
	for _, id := range ids {
		if names := fresh[id]; len(names) > 0 {
			m[id] = names
		} else {
			delete(m, id)
		}
	}
	a.storeTagIndex(m)
}

// tombstoneCap 墓碑集合容量上限（M12 审计修复 L5）：超限淘汰最旧记录。
// 墓碑仅用于拦截「删除前已滞留录制队列（容量 2048、flush 500ms）的批次」，
// 其生命周期本就只有数秒；有界化防止长期运行后 map 只增不减导致内存缓慢增长。
const tombstoneCap = 8192

// acquireArchive 取常驻归档 Writer 并登记一个在途操作（M12 审计修复 M1）。
// 返回 writer 及其创建时绑定的项目代际 gen；done 必须 defer 调用。
// 「取指针/创建 + archiveWG.Add」在 archiveMu 单次临界区内原子完成，杜绝
// closeArchive 在「解锁创建→再加锁 Add」缝隙里取走新 writer 并 Wait(0)→Close
// 的窄竞态。closeArchive 会先置 nil 拒绝新登记，再 Wait() 等全部在途操作结束后才 Close。
func (a *App) acquireArchive() (w *persist.Writer, gen uint64, done func(), err error) {
	a.archiveMu.Lock()
	defer a.archiveMu.Unlock()
	if a.archive == nil {
		path := a.dbPath()
		if path == "" {
			return nil, 0, func() {}, fmt.Errorf("请先打开或创建项目")
		}
		nw, oerr := persist.OpenArchive(path) // retainDays=0/maxMB=0 关保留清理；busy_timeout=15s
		if oerr != nil {
			return nil, 0, func() {}, fmt.Errorf("打开归档库: %w", oerr)
		}
		nw.Start()
		a.archive = nw
		a.archiveGen = a.projGen.Load()
	}
	a.archiveWG.Add(1)
	return a.archive, a.archiveGen, func() { a.archiveWG.Done() }, nil
}

// openArchiveReadOnly 打开一次性只读归档连接（不创建文件），并登记在途计数，
// 使 closeArchive 等待其关闭后才关闭/删除库（M1 覆盖只读路径；L2 不凭空建库）。
// gen 为入口项目代际：打开前与登记后各校验一次，切换窗口内的请求直接报错/放弃，
// 避免拿到旧项目只读句柄读到错库（Windows 下还会与 Close 抢文件）。
// 库文件不存在返回 (nil, noop, nil)，调用方按「无标签/无数据」处理。
func (a *App) openArchiveReadOnly(gen uint64) (*persist.Writer, func(), error) {
	path := a.dbPath()
	if path == "" {
		return nil, func() {}, fmt.Errorf("请先打开或创建项目")
	}
	if a.projGen.Load() != gen {
		return nil, func() {}, fmt.Errorf("项目已切换，请重试")
	}
	ro, err := persist.OpenReadOnly(path)
	if err != nil {
		if errors.Is(err, persist.ErrDBNotExist) {
			return nil, func() {}, nil
		}
		return nil, func() {}, fmt.Errorf("打开归档库: %w", err)
	}
	if a.projGen.Load() != gen {
		ro.Close()
		return nil, func() {}, fmt.Errorf("项目已切换，请重试")
	}
	// 先登记再复末：若切换的 closeArchive 恰好在此之前置 nil，Wait 仍会等到
	// 本登记，随后我们检测到代际变化立即关闭句柄并 Done（删除项目时不残留文件占用）。
	a.archiveMu.Lock()
	a.archiveWG.Add(1)
	sameGen := a.projGen.Load() == gen
	a.archiveMu.Unlock()
	if !sameGen {
		ro.Close()
		a.archiveWG.Done()
		return nil, func() {}, fmt.Errorf("项目已切换，请重试")
	}
	return ro, func() {
		ro.Close()
		a.archiveWG.Done()
	}, nil
}

// closeArchive 关闭归档库并清空 tagIndex/tombstone（项目切换/关闭/退出调用）。
// 先置 nil 拒绝新在途登记，再 Wait 等既有在途打标/查询全部结束，然后才 Close——
// 在途操作完成后打标侧按项目代际变化放弃回写（见 TagFlows gen 校验，AC12）。
// 注意：不得在持有 projMu 时调用本方法后再等待 archiveWG（TagFlows 结束期不再取
// projMu，改用 projGen 快照比较，故无 projMu→archiveWG 反向依赖，不会死锁）。
func (a *App) closeArchive() {
	a.archiveMu.Lock()
	w := a.archive
	a.archive = nil
	a.archiveGen = 0
	a.archiveMu.Unlock()
	a.archiveWG.Wait() // 等全部在途打标/只读操作结束
	if w != nil {
		w.Close()
	}
	a.resetTagIndex()
}

// validateTagName 校验并归一化标签名：trim/空白折叠后 1–40 字符。
func validateTagName(name string) (string, error) {
	norm := persist.NormalizeTagName(name)
	if norm == "" {
		return "", fmt.Errorf("标签名不能为空")
	}
	if len([]rune(norm)) > tagNameMaxLen {
		return "", fmt.Errorf("标签名最长 %d 个字符", tagNameMaxLen)
	}
	return norm, nil
}

// TagFlows 给一批流打标签并归档（Wails 绑定 + ctlapi 共用，设计 §4.3）。
// ids 为流 ID 列表（前端传当前过滤可见流，后端去重）；name 为标签名；
// autoClear=true 时打标完成后由后端原子清空列表（保置顶）。复盘页 HTTP 入口恒传 false。
func (a *App) TagFlows(ids []string, name string, autoClear bool) (*TagResult, error) {
	norm, err := validateTagName(name)
	if err != nil {
		return nil, err
	}
	// 去重（HTTP 入口可传重复）
	seen := make(map[string]struct{}, len(ids))
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return nil, fmt.Errorf("没有可标记的流量")
	}

	// 项目代际快照：无项目态拒绝打标。打标期间允许项目切换——archive 在途计数
	// （M1）保证 writer 不会被并发 Close，结束后用 projGen 快照判定是否放弃回写；
	// acquireArchive 内创建/取指针与在途登记在同一临界区，并返回 archive 绑定的
	// 代际：若与当前不一致（切换窗口内新 archive 属于新项目），直接放弃整次写。
	// 注意：结束期不得再取 projMu（switchProjectLocked 持 projMu 等 archiveWG，
	// 反向加锁会死锁），projGen 是原子读（M12 审计修复 M1）。
	gen := a.projGen.Load()
	if a.currentID() == "" {
		return nil, fmt.Errorf("请先打开或创建项目")
	}

	w, archGen, done, err := a.acquireArchive()
	if err != nil {
		return nil, err
	}
	defer done()
	if archGen != gen {
		return nil, fmt.Errorf("项目已切换，请重试")
	}

	// 1. 标签字典 upsert（新建或刷新 last_used_at），拿 tagID
	tag, err := w.UpsertTag(norm)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()

	res := &TagResult{Tag: toTagInfoDTO(tag)}
	// 逐批：内存命中→归档 flows/bodies；内存未命中→查库（已在库跳过 flows），均不可得计 skipped。
	// 关联与 flows 归档分批提交（每批单事务，压缩持锁窗口，设计 §4.2）。
	var inMemoryIDs []string // 内存中仍存在（回显推送用）
	var linkedIDs []string   // 本次成功建立关联的流（L5：重新打标后移出墓碑）
	for start := 0; start < len(uniq); start += tagBatchSize {
		end := start + tagBatchSize
		if end > len(uniq) {
			end = len(uniq)
		}
		batch := uniq[start:end]

		flowsToArchive := make([]*capture.Flow, 0, len(batch))
		linkIDs := make([]string, 0, len(batch))
		for _, id := range batch {
			if f, ok := a.st.Get(id); ok {
				flowsToArchive = append(flowsToArchive, f)
				linkIDs = append(linkIDs, id)
				inMemoryIDs = append(inMemoryIDs, id)
				continue
			}
			// 内存已淘汰：库中已有该流（录制/归档写过）则只补关联，否则 skipped
			exists, qerr := w.FlowExists(id)
			if qerr != nil {
				return nil, qerr
			}
			if exists {
				linkIDs = append(linkIDs, id)
			} else {
				res.Skipped++
			}
		}
		if len(flowsToArchive) > 0 {
			if err := w.ArchiveFlows(flowsToArchive); err != nil {
				return nil, err
			}
			res.Archived += len(flowsToArchive)
		}
		if len(linkIDs) > 0 {
			// L4：Tagged 统一为「新增关联数」——AddFlowTags 内部 ON CONFLICT DO NOTHING，
			// 仅累加 RowsAffected>0 的真实新增（重复打标不重复计数，与 mock 口径一致）。
			added, aerr := w.AddFlowTags(linkIDs, tag.ID, now)
			if aerr != nil {
				return nil, aerr
			}
			res.Tagged += added
			linkedIDs = append(linkedIDs, linkIDs...)
		}
	}

	// 打标期间发生项目切换：归档库已被 closeArchive 关闭（在途计数保证本次写完后才关），
	// 回写 tagIndex 会污染新项目索引——原子比对代际，变化即放弃回写（切换链路随后
	// resetTagIndex/懒打开新项目库，AC12）。此处不得取 projMu（会死锁，见入口注释）。
	if a.projGen.Load() != gen {
		return res, nil
	}

	// L5：本次重新建立关联的流从墓碑移出（此前被连删、重新打标=数据重新合法，
	// 允许其后续 update 重新入录制队列）。
	a.untombstone(linkedIDs)

	// 2. tagIndex COW 重建（Store 之前不触达任何 store 事件）
	// L4：只写内存命中的流——库中补关联的流已不在内存，写进索引没有意义还会
	// 让快照残留无对应列表行的键；其标签在补载时由 backfillTagIndex 重建。
	a.addTagToIndex(inMemoryIDs, res.Tag.Name)

	// 3. 回显推送：内存中仍存在的被打标流构造最新 meta 投 pendUp 合帧通道
	// （Store 无主动发 update API；不得直接 EventsEmit，设计 §4.3 第 6 步）。
	ups := make([]FlowMeta, 0, len(inMemoryIDs))
	for _, id := range inMemoryIDs {
		if f, ok := a.st.Get(id); ok {
			ups = append(ups, a.toMeta(f))
		}
	}
	a.pushUpserts(ups)

	// 4. autoClear 后端原子执行（保置顶）：st.Clear 的 evict 经 onStoreEvent 会剔除
	// pendUp 中同 id 待发 upsert，清空后不复活（设计 §4.3 第 7 步）。
	if autoClear {
		a.st.Clear()
	}

	// 5. 刷新标签计数（TagResult.Tag.Count 取最新值）
	if tags, lerr := w.ListTags(); lerr == nil {
		for i := range tags {
			if tags[i].ID == tag.ID {
				res.Tag = toTagInfoDTO(&tags[i])
				break
			}
		}
	}
	return res, nil
}

// pushUpserts 把一批 meta 投入 pendUp 合帧缓冲（复用 50ms flush，不直接 EventsEmit）。
// 与 onStoreEvent 走同一把 pendMu、同一帧，保证 upsert/evict 时序正确。
func (a *App) pushUpserts(ups []FlowMeta) {
	if len(ups) == 0 {
		return
	}
	a.pendMu.Lock()
	for _, m := range ups {
		a.pendUp[m.ID] = m
	}
	if !a.flushDue {
		a.flushDue = true
		time.AfterFunc(50*time.Millisecond, a.flush)
	}
	a.pendMu.Unlock()
}

// ListTags 当前项目标签列表（含计数、最近使用时间），供弹窗历史标签下拉与复盘侧栏。
// 归档库从未打开（本项目未打过任何标签）时返回空列表，不触发建库。
func (a *App) ListTags() ([]TagInfo, error) {
	if a.currentID() == "" {
		return nil, fmt.Errorf("请先打开或创建项目")
	}
	w, done, err := a.reviewReader()
	if err != nil {
		return nil, err
	}
	defer done()
	if w == nil {
		// 库文件不存在（从未录制/打标）：无标签，不触发建库
		return []TagInfo{}, nil
	}
	tags, err := w.ListTags()
	if err != nil {
		return nil, err
	}
	return toTagInfoDTOs(tags), nil
}

// GetReviewURL 返回复盘页地址（ctlapi 基址 + token），供前端 BrowserOpenURL（设计 §5.3）。
// 9595 被占时 ctl 启动失败仅记日志、a.ctl 保持 nil（不换端口）——此处明确报错（AC15）。
func (a *App) GetReviewURL() (string, error) {
	a.projMu.Lock()
	projID := a.currentID()
	a.projMu.Unlock()
	if projID == "" {
		return "", fmt.Errorf("请先打开或创建项目")
	}
	a.mu.Lock()
	ctl := a.ctl
	a.mu.Unlock()
	if ctl == nil {
		return "", fmt.Errorf("控制 API 未启动（端口 9595 可能被占用），请重启应用或检查端口占用")
	}
	return fmt.Sprintf("http://%s/review.html?token=%s", ctl.Addr(), ctl.Token()), nil
}

// ---------- 复盘页查询/管理（供 ctl_bridge 转调，设计 §4.4） ----------

// ReviewFlowList 复盘流列表：tagID="all" 时按 scope 取数（archived=打标流，
// all=库内全量含自动录制未打标流）；start/end 为 unix 毫秒时间窗（含头尾，
// 0=不限，支持半开）；limit/offset 分页；opts.Q 非空时在 method/host/path 子串过滤，
// opts.SortKey/SortDir 控制排序，opts.ShowIgnored=false 时以 SQL 条件排除忽略名单
// （M12.2：忽略仅查询隐藏，不删数据）。
// 返回的 FlowMeta 带 Tags（从归档库批量查，不依赖内存 tagIndex——流可能已淘汰）。
//
// v3.1：归档库从未打开（w==nil，全新项目从未录制/打标）时返回空列表 + total=0，
// 不报错——复盘页首开应是空态引导而非 fatal（设计 §6.4、AC18）。
func (a *App) ReviewFlowList(tagID, scope string, start, end int64, limit, offset int, opts persist.ReviewListOpts) ([]FlowMeta, int, error) {
	if tagID != "all" && !persist.ValidTagID(tagID) {
		return nil, 0, fmt.Errorf("非法标签 id")
	}
	if _, err := persist.NormalizeScope(scope); err != nil {
		return nil, 0, err
	}
	w, done, err := a.reviewReader()
	if err != nil {
		return nil, 0, err
	}
	defer done()
	if w == nil {
		// 库文件不存在（从未录制/打标）：业务空态，非错误（v3.1 AC18）
		return []FlowMeta{}, 0, nil
	}
	flows, err := w.FlowsByTag(tagID, scope, start, end, limit, offset, opts)
	if err != nil {
		return nil, 0, err
	}
	// 标签集合：优先内存 tagIndex（热路径零 DB），未命中流从归档库批量查补齐
	ids := make([]string, 0, len(flows))
	for _, f := range flows {
		ids = append(ids, string(f.ID))
	}
	dbTags, _ := w.TagsForFlows(ids)
	out := make([]FlowMeta, 0, len(flows))
	for _, f := range flows {
		m := a.toMeta(f)
		if len(m.Tags) == 0 {
			if names := dbTags[string(f.ID)]; len(names) > 0 {
				m.Tags = names
			}
		}
		out = append(out, m)
	}
	// 总数：与列表同谓词（tagID+scope+窗口+q+忽略排除），避免分页 total 与数据不一致
	total, err := w.CountFlows(tagID, scope, start, end, opts)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ReviewListIgnores 返回复盘忽略名单（只读连接，旧库无表容错为空）。
func (a *App) ReviewListIgnores() ([]persist.ReviewIgnore, error) {
	w, done, err := a.reviewReader()
	if err != nil {
		return nil, err
	}
	defer done()
	if w == nil {
		return []persist.ReviewIgnore{}, nil
	}
	return w.ListReviewIgnores()
}

// ReviewAddIgnore 加入复盘忽略名单（写常驻归档库，项目代际校验同 TagFlows）。
// value 在 persist 层按 kind 归一化；added=false 表示已存在（幂等，仅刷新 note）。
func (a *App) ReviewAddIgnore(kind, value, note string) (persist.ReviewIgnore, bool, error) {
	gen := a.projGen.Load()
	if a.currentID() == "" {
		return persist.ReviewIgnore{}, false, fmt.Errorf("请先打开或创建项目")
	}
	w, archGen, done, err := a.acquireArchive()
	if err != nil {
		return persist.ReviewIgnore{}, false, err
	}
	defer done()
	if archGen != gen {
		return persist.ReviewIgnore{}, false, fmt.Errorf("项目已切换，请重试")
	}
	return w.AddReviewIgnore(kind, value, note)
}

// ReviewDeleteIgnore 删除一条复盘忽略项（kind + 已归一化 value）。
func (a *App) ReviewDeleteIgnore(kind, value string) (bool, error) {
	gen := a.projGen.Load()
	if a.currentID() == "" {
		return false, fmt.Errorf("请先打开或创建项目")
	}
	w, archGen, done, err := a.acquireArchive()
	if err != nil {
		return false, err
	}
	defer done()
	if archGen != gen {
		return false, fmt.Errorf("项目已切换，请重试")
	}
	// 接口值统一归一化后按存储口径删除（名单回传值为存储口径，host/proc 归一化幂等）
	v, verr := persist.NormalizeIgnoreValue(kind, value)
	if verr != nil {
		return false, verr
	}
	return w.DeleteReviewIgnore(kind, v)
}

// ReviewTagsOverview 标签侧栏概览（设计 §4.4 v3.1）：标签列表 + total（去重打标流数，
// COUNT(DISTINCT) 口径——禁止前端各标签 count 累加，流多标签时会虚高）+
// totalFlows（库内全部 flows 行数，含未打标自动录制流）。
// 归档库从未打开时返回空列表 + 双 0（空态，不报错）。
func (a *App) ReviewTagsOverview() ([]TagInfo, int, int, error) {
	w, done, err := a.reviewReader()
	if err != nil {
		return nil, 0, 0, err
	}
	defer done()
	if w == nil {
		return []TagInfo{}, 0, 0, nil
	}
	tags, err := w.ListTags()
	if err != nil {
		return nil, 0, 0, err
	}
	tagged, err := w.CountTaggedFlows()
	if err != nil {
		return nil, 0, 0, err
	}
	allFlows, err := w.CountAllFlows()
	if err != nil {
		return nil, 0, 0, err
	}
	return toTagInfoDTOs(tags), tagged, allFlows, nil
}

// ReviewHistogram 复盘密度直方图（M12.1，时间轴底图；HTTP-only，不新增 wails 绑定）。
// 参数语义同 ReviewFlowList；buckets<=0 默认 120、上限 500。
// 返回全域边界 start/end（供前端时间轴坐标系）与分桶。
func (a *App) ReviewHistogram(tagID, scope string, winStart, winEnd int64, buckets int) (int64, int64, []persist.HistBucket, error) {
	if tagID != "all" && !persist.ValidTagID(tagID) {
		return 0, 0, nil, fmt.Errorf("非法标签 id")
	}
	if _, err := persist.NormalizeScope(scope); err != nil {
		return 0, 0, nil, err
	}
	w, done, err := a.reviewReader()
	if err != nil {
		return 0, 0, nil, err
	}
	defer done()
	if w == nil {
		return 0, 0, []persist.HistBucket{}, nil
	}
	return w.Histogram(tagID, scope, winStart, winEnd, buckets)
}

// ReviewFlowDetail 复盘单流详情（含标签）：流可能已被内存淘汰，走归档库 LoadFlowByID。
func (a *App) ReviewFlowDetail(flowID string) (*FlowDetail, error) {
	w, done, err := a.reviewReader()
	if err != nil {
		return nil, err
	}
	defer done()
	if w == nil {
		return nil, fmt.Errorf("flow %s not found（可能已删除）", flowID)
	}
	f, err := w.LoadFlowByID(flowID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("flow %s not found（可能已删除）", flowID)
	}
	d := a.flowDetail(f)
	// 标签：内存索引未命中（历史/已淘汰流）则从库查
	if len(d.Tags) == 0 {
		if names, _ := w.TagsForFlows([]string{flowID}); len(names[flowID]) > 0 {
			d.Tags = names[flowID]
		}
	}
	return d, nil
}

// ReviewFlowBody 复盘单流正文（req|resp）：走归档库 LoadBody（不依赖内存），
// 复用 BodyPayload 解压口径。
func (a *App) ReviewFlowBody(flowID, which string) (*BodyPayload, error) {
	w, done, err := a.reviewReader()
	if err != nil {
		return nil, err
	}
	defer done()
	if w == nil {
		return nil, fmt.Errorf("flow %s not found（可能已删除）", flowID)
	}
	var msg *capture.Message
	f, err := w.LoadFlowByID(flowID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("flow %s not found（可能已删除）", flowID)
	}
	switch which {
	case "req":
		msg = f.Request
	case "resp":
		msg = f.Response
	default:
		return nil, fmt.Errorf("which 须为 req|resp")
	}
	if msg == nil {
		return &BodyPayload{}, nil
	}
	// 归档流 body 不在内存（marshalFlow 已置 nil、长度记 BodyLen），惰性回查
	raw, err := w.LoadBody(flowID, which)
	if err != nil {
		return nil, err
	}
	p := &BodyPayload{
		Encoding:    msg.ContentEncoding,
		ContentType: msg.Header.Get("Content-Type"),
		Truncated:   msg.BodyTruncated,
		Raw:         raw,
	}
	if len(raw) > 0 {
		dec, derr := capture.DecodeBody(msg.ContentEncoding, raw)
		if derr != nil {
			p.DecodeErr = derr.Error()
		} else {
			p.Body = dec
		}
	}
	return p, nil
}

// RenameTagApp 重命名标签（目标已存在则合并关联）；完成后 COW 刷新 tagIndex +
// 受影响内存流经 pendUp 推送（跨窗口闭环，设计 §4.4/AC6/AC14）。
func (a *App) RenameTagApp(oldID, newName string) error {
	norm, err := validateTagName(newName)
	if err != nil {
		return err
	}
	gen := a.projGen.Load()
	if a.currentID() == "" {
		return fmt.Errorf("请先打开或创建项目")
	}
	w, archGen, done, err := a.acquireArchive()
	if err != nil {
		return err
	}
	defer done()
	if archGen != gen {
		return fmt.Errorf("项目已切换，请重试")
	}
	affected, err := w.RenameTag(oldID, norm)
	if err != nil {
		return err
	}
	if a.projGen.Load() != gen {
		return nil // 切换中：旧项目索引随 closeArchive 清空，不回写
	}
	// COW 重建受影响流标签（Store 前不触达 store）
	a.rebuildTagsForIDs(w, affected)
	// pendUp 推送受影响的内存流（徽章即时刷新）
	ups := make([]FlowMeta, 0, len(affected))
	for _, id := range affected {
		if f, ok := a.st.Get(id); ok {
			ups = append(ups, a.toMeta(f))
		}
	}
	a.pushUpserts(ups)
	return nil
}

// DeleteTagApp 删除标签。deleteFlows=false 仅删关联（流保留在库）；true 连 flows/bodies
// 一起删（仍被其他标签引用的流保留）。两路径都 COW 刷新 tagIndex + pendUp 推送；
// deleteFlows=true 额外记 tombstone 封幽灵复活（设计 §4.4/AC13）。
func (a *App) DeleteTagApp(tagID string, deleteFlows bool) (deletedCount int, err error) {
	gen := a.projGen.Load()
	if a.currentID() == "" {
		return 0, fmt.Errorf("请先打开或创建项目")
	}
	w, archGen, done, err := a.acquireArchive()
	if err != nil {
		return 0, err
	}
	defer done()
	if archGen != gen {
		return 0, fmt.Errorf("项目已切换，请重试")
	}
	// 删除前收集该标签关联的全部流（两路径都需刷新其标签名单）
	affected, ferr := w.FlowIDsByTag(tagID)
	if ferr != nil {
		return 0, ferr
	}

	deletedIDs, err := w.DeleteTag(tagID, deleteFlows)
	if err != nil {
		return 0, err
	}
	if a.projGen.Load() != gen {
		return len(deletedIDs), nil
	}

	// tombstone：连删的流 id 记录（有界集合），拦截录制 Writer 后续 upsert 复活
	if deleteFlows && len(deletedIDs) > 0 {
		a.putTombstone(deletedIDs)
	}

	// COW 重建受影响流标签
	a.rebuildTagsForIDs(w, affected)
	// pendUp 推送：连删流若仍在内存也推送（徽章清空；流本身不从主窗列表移除——
	// 主窗是会话实时视图，归档删除不影响会话，设计 §4.4）。
	ups := make([]FlowMeta, 0, len(affected))
	for _, id := range affected {
		if f, ok := a.st.Get(id); ok {
			ups = append(ups, a.toMeta(f))
		}
	}
	a.pushUpserts(ups)
	return len(deletedIDs), nil
}

// reviewReader 复盘/列表查询取归档库（只读语义）。返回 nil writer（done 为 no-op）
// 表示库文件尚不存在，调用方按「无标签/无数据」处理。
//
// 常驻 archive 已打开：经 acquireArchive 登记在途（M1：读期间 closeArchive 不能 Close），
// 并校验 archive 绑定代际与入口一致，防止项目切换窗口借新 archive 读到新项目库。
// archive 未打开：用一次性只读连接（L2：OpenReadOnly 先 Stat 不凭空建库、mode=ro、
// 不 migrate/VACUUM）；openArchiveReadOnly 内部在打开前/登记后两次校验代际，
// 覆盖「打开瞬间切换项目、Windows 下旧库文件被 Close 占用」的窗口。
func (a *App) reviewReader() (w *persist.Writer, done func(), err error) {
	done = func() {}
	if a.currentID() == "" {
		return nil, done, fmt.Errorf("请先打开或创建项目")
	}
	gen := a.projGen.Load()
	a.archiveMu.Lock()
	ar := a.archive
	a.archiveMu.Unlock()
	if ar != nil {
		// 常驻 archive：登记在途（acquireArchiveGen 内复末代际与指针）
		return a.acquireArchiveGen(gen)
	}
	return a.openArchiveReadOnly(gen)
}

// acquireArchiveGen 以期望代际登记常驻 archive 在途计数；登记后复末代际与指针，
// 切换已发生则放弃（回退由调用方报「请重试」，绝不借新 archive 读错库）。
func (a *App) acquireArchiveGen(gen uint64) (*persist.Writer, func(), error) {
	a.archiveMu.Lock()
	defer a.archiveMu.Unlock()
	if a.archive == nil || a.archiveGen != gen {
		return nil, func() {}, fmt.Errorf("项目已切换，请重试")
	}
	a.archiveWG.Add(1)
	return a.archive, func() { a.archiveWG.Done() }, nil
}

func toTagInfoDTO(t *persist.TagInfo) *TagInfo {
	if t == nil {
		return nil
	}
	return &TagInfo{ID: t.ID, Name: t.Name, Count: t.Count, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

func toTagInfoDTOs(ts []persist.TagInfo) []TagInfo {
	out := make([]TagInfo, 0, len(ts))
	for i := range ts {
		out = append(out, TagInfo{ID: ts[i].ID, Name: ts[i].Name, Count: ts[i].Count,
			CreatedAt: ts[i].CreatedAt, LastUsedAt: ts[i].LastUsedAt})
	}
	return out
}

// ---------- Wails 专用单结构返回绑定（M12.3 修复） ----------
//
// Wails v2 方法绑定（internal/binding/boundMethod.go Call）只支持 1~2 个返回值：
// 输出数为 2 时取 (value, error)；**3 个及以上没有任何 case 命中，result 静默为
// null、error 也为 nil**——前端拿到 null（被兜底成空态），表现为标签/列表/时间轴
// 全空且无报错。HTTP（ctl_bridge）直接 Go 调用不受影响。
// 故主窗内嵌复盘改走下面的单结构返回包装；原多返回值方法保留给 HTTP 桥接与测试复用。

// WailsTagsOverview 标签概览单结构返回（包装 ReviewTagsOverview）。
type WailsTagsOverview struct {
	Tags       []TagInfo `json:"tags"`
	Total      int       `json:"total"`
	TotalFlows int       `json:"totalFlows"`
}

func (a *App) WailsTagsOverview() (*WailsTagsOverview, error) {
	tags, tagged, all, err := a.ReviewTagsOverview()
	if err != nil {
		return nil, err
	}
	return &WailsTagsOverview{Tags: tags, Total: tagged, TotalFlows: all}, nil
}

// WailsFlowListResult 复盘流列表单结构返回（包装 ReviewFlowList）。
type WailsFlowListResult struct {
	Flows []FlowMeta `json:"flows"`
	Total int        `json:"total"`
}

func (a *App) WailsFlowList(tagID, scope string, start, end int64, limit, offset int, opts persist.ReviewListOpts) (*WailsFlowListResult, error) {
	flows, total, err := a.ReviewFlowList(tagID, scope, start, end, limit, offset, opts)
	if err != nil {
		return nil, err
	}
	return &WailsFlowListResult{Flows: flows, Total: total}, nil
}

// WailsAddIgnoreResult 加入忽略名单单结构返回（包装 ReviewAddIgnore）。
type WailsAddIgnoreResult struct {
	Ignore persist.ReviewIgnore `json:"ignore"`
	Added  bool                 `json:"added"`
}

func (a *App) WailsAddIgnore(kind, value, note string) (*WailsAddIgnoreResult, error) {
	item, added, err := a.ReviewAddIgnore(kind, value, note)
	if err != nil {
		return nil, err
	}
	return &WailsAddIgnoreResult{Ignore: item, Added: added}, nil
}

// WailsHistogramResult 密度直方图单结构返回（包装 ReviewHistogram）。
type WailsHistogramResult struct {
	Start   int64                `json:"start"`
	End     int64                `json:"end"`
	Buckets []persist.HistBucket `json:"buckets"`
}

func (a *App) WailsHistogram(tagID, scope string, winStart, winEnd int64, buckets int) (*WailsHistogramResult, error) {
	t0, t1, hist, err := a.ReviewHistogram(tagID, scope, winStart, winEnd, buckets)
	if err != nil {
		return nil, err
	}
	return &WailsHistogramResult{Start: t0, End: t1, Buckets: hist}, nil
}
