// Package persist 流量 SQLite 持久化（M7，方案 §4.11）。
//
// 设计要点：
//   - 选型 modernc.org/sqlite（纯 Go 无 CGO，保持单 exe 构建链）；禁用 mattn/go-sqlite3。
//   - 写入是旁路：热路径（store 订阅回调）只做深拷贝后丢入有界队列，队列满丢弃并计数，
//     绝不阻塞代理转发；后台 goroutine 批量事务落盘。
//   - flows 表存元数据 JSON（不含 body），bodies 表存请求/响应体 blob（zstd 压缩），
//     按 flow id + kind(req|resp) 行式存放。
//   - 读取：启动开启时加载最近 N 条元数据入内存 store（body 惰性——查看详情/正文时
//     才经 LoadBody 回查 DB），历史流以 capture.SourceHistory 标记。
//   - 保留策略：按 retainDays / maxMB 后台清理，DB 体积有界；落盘不改变内存 store 语义
//     （环形淘汰/置顶/清空均不影响 DB，DB 由保留策略独立管理）。
package persist

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/klauspost/compress/zstd"

	"prismproxy/internal/capture"
)

// queueCap 异步写入队列容量；满后新流丢弃（计数 Dropped()），绝不阻塞代理热路径
const queueCap = 2048

// batchSize / flushInterval 批量落盘阈值：攒够一批或定时到点即事务提交
const (
	batchSize     = 100
	flushInterval = 500 * time.Millisecond
)

// retentionInterval 保留策略后台清理周期
const retentionInterval = 10 * time.Minute

// Writer 持久化写入器：Open 后 Start 启动后台消费；Close 落盘剩余并关闭 DB。
// 零值不可用，须经 Open 构造。
type Writer struct {
	db    *sql.DB
	queue chan *capture.Flow
	wg    sync.WaitGroup
	stop  chan struct{}

	retainDays int
	maxMB      int

	dropped atomic.Int64 // 队列满丢弃的流数
	written atomic.Int64 // 已落盘流数
	active  atomic.Bool  // Start 后 true；Close 后 false
	paused  atomic.Bool  // 测试用：暂停消费排空（仅队列丢弃测试使用）

	// M12 审计修复 M2：墓碑过滤（仅录制 Writer 由 app 层注入）。消费侧每批落盘前
	// 过滤被判删的 flow id——它们可能在删除前已滞留队列（容量 2048、flush 500ms），
	// 仅在入队侧检查挡不住出队时的 ON CONFLICT 复活。归档 Writer 不注入（nil=不过滤）。
	isTombstoned func(flowID string) bool
}

// Open 打开（必要时创建）DB 并建表。dbPath 为 DB 文件路径（相对路径按 cwd 解析）。
// 录制库专用（busy_timeout=5s）。
func Open(dbPath string, retainDays, maxMB int) (*Writer, error) {
	return open(dbPath, retainDays, maxMB, 5000)
}

// OpenArchive 打开标签归档专用 Writer（M12）：retainDays=0/maxMB=0 关闭保留清理
// （归档数据永不自动删除）；busy_timeout=15s——与录制 Writer 同库跨连接池并发写时，
// 现代 SQLite 单写者模型下另一写者持锁窗口可能较长（批量 100 条 × 大 body zstd），
// 配合 ArchiveFlow 的 BUSY 退避重试兜底（设计 §4.2）。
func OpenArchive(dbPath string) (*Writer, error) {
	return open(dbPath, 0, 0, 15000)
}

// ErrDBNotExist 只读打开时库文件不存在（M12 审计修复 L2：只读路径不得凭空建库）。
var ErrDBNotExist = errors.New("数据库文件不存在")

// OpenReadOnly 以只读模式打开既有 DB（不创建文件、不跑 migrate/VACUUM），供复盘查询/
// 标签列表/索引回填等只读路径使用。库文件不存在返回包装了 ErrDBNotExist 的错误，
// 调用方据此静默为空结果（fs.ErrNotExist 一并视为不存在）。
func OpenReadOnly(dbPath string) (*Writer, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrDBNotExist, dbPath)
		}
		return nil, fmt.Errorf("检查数据库文件: %w", err)
	}
	// mode=ro 只读；query_only 连接级 PRAGMA 双保险，杜绝只读连接误写。
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(true)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return &Writer{db: db}, nil
}

func open(dbPath string, retainDays, maxMB, busyTimeoutMS int) (*Writer, error) {
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录: %w", err)
		}
	}
	// modernc.org/sqlite DSN：?_pragma=xxx 可设编译指示；busy_timeout 防偶发锁；
	// auto_vacuum=INCREMENTAL(2) 让删除产生的空闲页可经 PRAGMA incremental_vacuum 归还给 OS——
	// 否则 DELETE 不缩小主 .db 文件，体积上限裁剪会失效（只在文件尾空闲页能被释放）。
	// 注：auto_vacuum 仅在建表前生效，既有库由 migrate 做一次性 VACUUM 迁移（见下）。
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=auto_vacuum(2)", dbPath, busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// 单写连接：批量事务串行，避免 SQLITE_BUSY；读另起连接
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	w := &Writer{
		db:         db,
		retainDays: retainDays,
		maxMB:      maxMB,
	}
	if err := w.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return w, nil
}

func (w *Writer) migrate() error {
	// 既有库若 auto_vacuum=NONE（DSN 新参数对已存在库不生效），一次性转为 INCREMENTAL。
	// 必须在建表前（空库/既有表均如此）：PRAGMA auto_vacuum=INCREMENTAL 后 VACUUM 重写文件。
	// 全新空库此时无表，VACUUM 开销可忽略；既有库仅首次升级承担一次重写。
	var mode int
	if err := w.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("查询 auto_vacuum: %w", err)
	}
	if mode == 0 {
		if _, err := w.db.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return fmt.Errorf("设置 auto_vacuum: %w", err)
		}
		if _, err := w.db.Exec(`VACUUM`); err != nil {
			return fmt.Errorf("迁移 auto_vacuum（VACUUM）: %w", err)
		}
	}
	_, err := w.db.Exec(`
CREATE TABLE IF NOT EXISTS flows (
    id         TEXT PRIMARY KEY,
    started_at INTEGER NOT NULL,      -- unix 毫秒（排序/保留清理用）
    state      TEXT NOT NULL,
    scheme     TEXT,
    method     TEXT,
    host       TEXT,
    path       TEXT,
    status     INTEGER NOT NULL DEFAULT 0,
    source     TEXT NOT NULL DEFAULT 'capture',
    data       TEXT NOT NULL          -- 完整 Flow JSON（不含 body 字节，body 长度已并入 Message.BodyLen）
);
CREATE INDEX IF NOT EXISTS idx_flows_started ON flows(started_at);

CREATE TABLE IF NOT EXISTS bodies (
    flow_id  TEXT NOT NULL,
    kind     TEXT NOT NULL,          -- 'req' | 'resp'
    enc      TEXT NOT NULL DEFAULT '',-- 压缩方式：''=原样 | 'zstd'
    raw_len  INTEGER NOT NULL DEFAULT 0, -- 解压后原始字节数
    data     BLOB NOT NULL,
    PRIMARY KEY (flow_id, kind)
);

-- M12 标签字典（标签 = 归档；id 为 hash，见 tags.go）
CREATE TABLE IF NOT EXISTS tags (
    id           TEXT PRIMARY KEY,   -- "t_" + sha256(归一化名) 前 12 位 hex（碰撞时 16 位）
    name         TEXT NOT NULL,      -- 展示名（用户原始输入 trim 后）
    created_at   INTEGER NOT NULL,   -- unix 毫秒
    last_used_at INTEGER NOT NULL    -- unix 毫秒，最近一次打标时间
);

-- M12 流 ↔ 标签 多对多关联
CREATE TABLE IF NOT EXISTS flow_tags (
    flow_id   TEXT NOT NULL,
    tag_id    TEXT NOT NULL,
    tagged_at INTEGER NOT NULL,      -- unix 毫秒
    PRIMARY KEY (flow_id, tag_id)
);
CREATE INDEX IF NOT EXISTS idx_flow_tags_tag ON flow_tags(tag_id);
CREATE INDEX IF NOT EXISTS idx_flow_tags_flow ON flow_tags(flow_id);

-- M12.2 数据复盘隐藏名单（「忽略」只在复盘查询时排除，不删除 flows 数据）
CREATE TABLE IF NOT EXISTS review_ignores (
    kind       TEXT NOT NULL,          -- 'host' | 'path' | 'proc'
    value      TEXT NOT NULL,          -- 归一化值（host 去端口小写/去尾点；path 无 query 且 / 开头；proc 原样 trim）
    created_at INTEGER NOT NULL,       -- unix 毫秒
    note       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (kind, value)
);
`)
	return err
}

// Start 启动后台消费/定时刷盘/保留清理 goroutine。
func (w *Writer) Start() {
	if w.active.Swap(true) {
		return
	}
	w.stop = make(chan struct{})
	w.queue = make(chan *capture.Flow, queueCap)
	w.wg.Add(2)
	go w.consumeLoop()
	go w.retentionLoop()
}

// Enqueue 热路径入口：终态（done/error）流深拷贝后入队；队列满丢弃计数，绝不阻塞。
// 非终态（pending/streaming）忽略——终态快照已含全部信息，避免半成品落盘。
func (w *Writer) Enqueue(f *capture.Flow) {
	if f == nil || !w.active.Load() {
		return
	}
	if f.State != capture.StateDone && f.State != capture.StateError {
		return
	}
	cp := cloneFlow(f)
	select {
	case w.queue <- cp:
	default:
		w.dropped.Add(1)
		log.Printf("persist: 写入队列已满，丢弃流 %s（累计丢弃 %d）", f.ID, w.dropped.Load())
	}
}

// Dropped 队列满累计丢弃数
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Written 已落盘流数
func (w *Writer) Written() int64 { return w.written.Load() }

// UpdateRetention 热更新保留策略（保留天数/体积上限）。仅影响后续 retentionLoop，
// 不重连 DB、不中断写入队列（供设置变更在 DB 路径不变时避免 Close/Open）。
func (w *Writer) UpdateRetention(retainDays, maxMB int) {
	w.retainDays = retainDays
	w.maxMB = maxMB
}

// Path 返回当前打开的 DB 主文件路径（modernc 支持 PRAGMA database_list）；取不到返回 ""。
func (w *Writer) Path() string { return w.dbPath() }

// setPaused 暂停/恢复消费排空（仅测试用：确定性灌满队列验证丢弃）
func (w *Writer) setPaused(p bool) { w.paused.Store(p) }

// SetTombstoneFilter 注入墓碑判定回调（M12 审计修复 M2，仅录制 Writer 调用）。
// 消费侧每批落盘前剔除被判删的流，防止删除前已滞留队列的批次 ON CONFLICT 复活数据。
func (w *Writer) SetTombstoneFilter(fn func(flowID string) bool) {
	w.isTombstoned = fn
}

// queued 队列滞留数（仅测试用：暂停消费后确认批次确已滞留再解除暂停）。
func (w *Writer) queued() int {
	if w.queue == nil {
		return 0
	}
	return len(w.queue)
}

func (w *Writer) consumeLoop() {
	defer w.wg.Done()
	batch := make([]*capture.Flow, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// M12 审计修复 M2：落盘前剔除墓碑流（删除前已滞留队列的批次），
		// 防止 ON CONFLICT DO UPDATE 让已删数据复活。
		if w.isTombstoned != nil {
			kept := batch[:0]
			for _, f := range batch {
				if f != nil && w.isTombstoned(string(f.ID)) {
					continue
				}
				kept = append(kept, f)
			}
			batch = kept
		}
		if len(batch) == 0 {
			return
		}
		if err := w.writeBatch(batch); err != nil {
			log.Printf("persist: 批量写入失败（%d 条）: %v", len(batch), err)
		} else {
			w.written.Add(int64(len(batch)))
		}
		batch = batch[:0]
	}
	for {
		// 暂停态（测试队列丢弃用）：不排空队列，仅响应 ticker/stop
		if w.paused.Load() {
			select {
			case <-ticker.C:
			case <-w.stop:
				flush()
				return
			}
			continue
		}
		select {
		case f := <-w.queue:
			batch = append(batch, f)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-w.stop:
			// 退出：把队列里剩余的尽量落盘（非阻塞排空）
			for {
				select {
				case f := <-w.queue:
					batch = append(batch, f)
				default:
					flush()
					return
				}
			}
		}
	}
}

// writeBatch 单事务 upsert 一批流（flows 元数据 + bodies 压缩 blob）
func (w *Writer) writeBatch(batch []*capture.Flow) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmtFlow, err := tx.Prepare(`INSERT INTO flows(id,started_at,state,scheme,method,host,path,status,source,data)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
started_at=excluded.started_at,state=excluded.state,scheme=excluded.scheme,
method=excluded.method,host=excluded.host,path=excluded.path,status=excluded.status,
source=excluded.source,data=excluded.data`)
	if err != nil {
		return err
	}
	defer stmtFlow.Close()
	stmtBody, err := tx.Prepare(`INSERT INTO bodies(flow_id,kind,enc,raw_len,data)
VALUES(?,?,?,?,?) ON CONFLICT(flow_id,kind) DO UPDATE SET
enc=excluded.enc,raw_len=excluded.raw_len,data=excluded.data`)
	if err != nil {
		return err
	}
	defer stmtBody.Close()

	for _, f := range batch {
		started := int64(0)
		if f.Timing != nil {
			started = f.Timing.Start.UnixMilli()
		}
		method, host, path := "", "", ""
		status := 0
		if f.Request != nil {
			method = f.Request.Method
			host = f.ServerAddr
			path = requestPath(f.Request.URL)
		}
		if f.Response != nil {
			status = f.Response.StatusCode
		}
		src := f.Source
		if src == "" {
			src = capture.SourceCapture
		}
		data, err := marshalFlow(f)
		if err != nil {
			log.Printf("persist: 序列化流 %s 失败，跳过: %v", f.ID, err)
			continue
		}
		if _, err := stmtFlow.Exec(f.ID, started, string(f.State), f.Scheme, method, host, path, status, src, string(data)); err != nil {
			return fmt.Errorf("upsert flow %s: %w", f.ID, err)
		}
		if f.Request != nil && len(f.Request.Body) > 0 {
			if err := upsertBody(stmtBody, f.ID, "req", f.Request.Body); err != nil {
				return err
			}
		}
		if f.Response != nil && len(f.Response.Body) > 0 {
			if err := upsertBody(stmtBody, f.ID, "resp", f.Response.Body); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func upsertBody(stmt *sql.Stmt, flowID, kind string, raw []byte) error {
	enc := ""
	blob := raw
	// 小 body 压缩收益低且 zstd 有固定开销，<256B 原样存；其余 zstd 压缩（变短才采用）
	if len(raw) >= 256 {
		enc, blob = compressBody(raw)
	}
	_, err := stmt.Exec(flowID, kind, enc, len(raw), blob)
	return err
}

// archiveMaxRetries / archiveRetryBackoff：ArchiveFlow 遇 SQLITE_BUSY/LOCKED 时的
// 退避重试参数（跨 Writer 连接池并发写兜底，设计 §4.2）。
const (
	archiveMaxRetries   = 3
	archiveRetryBackoff = time.Second
)

// ArchiveFlow 归档单条流（M12，标签 = 归档）：同步 upsert flows 元数据 + bodies 正文，
// 幂等（INSERT ... ON CONFLICT DO UPDATE）。与录制 Enqueue 的区别：同步写、不走队列、
// 不受自动录制开关影响；内置 SQLITE_BUSY/LOCKED 退避重试——归档 Writer 与录制 Writer
// 可能同时打开同一 prism.db（两个独立连接池），busy_timeout 之外再兜一层重试。
func (w *Writer) ArchiveFlow(f *capture.Flow) error {
	if f == nil {
		return nil
	}
	// 深拷贝隔离：调用方（打标 goroutine）持有的是 store 内存真源流，marshalFlow 虽为
	// 只读浅拷贝，但 cloneFlow 与 Enqueue 口径一致，彻底避免与代理热路径/UI 并发读写。
	cp := cloneFlow(f)
	var lastErr error
	for attempt := 0; attempt < archiveMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(archiveRetryBackoff)
		}
		err := w.writeBatch([]*capture.Flow{cp})
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusy(err) {
			return err
		}
		log.Printf("persist: 归档写入遇到 BUSY（第 %d 次重试）: %v", attempt+1, err)
	}
	return fmt.Errorf("归档流 %s 重试耗尽: %w", f.ID, lastErr)
}

// isSQLiteBusy 判断是否 SQLITE_BUSY/SQLITE_LOCKED（modernc.org/sqlite 错误经字符串识别，
// 驱动错误码常量在不同版本间路径不稳定；busy_timeout 内通常不会到达这里，仅重试兜底用）。
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_LOCKED") || strings.Contains(msg, "database table is locked")
}

// zstd 编解码器（并发安全，包级复用）
var (
	zstdEncoder, _ = zstd.NewWriter(nil)
	zstdDecoder, _ = zstd.NewReader(nil)
)

// compressBody 返回 (enc, blob)：压缩后更短用 ("zstd", 压缩)，否则 ("", 原始)
func compressBody(raw []byte) (string, []byte) {
	compressed := zstdEncoder.EncodeAll(raw, make([]byte, 0, len(raw)))
	if len(compressed) < len(raw) {
		return "zstd", compressed
	}
	return "", raw
}

// decompressBody 解压 zstd blob
func decompressBody(blob []byte) ([]byte, error) {
	return zstdDecoder.DecodeAll(blob, make([]byte, 0, len(blob)*2))
}

// LoadRecent 加载最近 limit 条流（按时间倒序取，返回正序——最旧在前，便于追加进环形缓冲）。
// 仅元数据（不含 body）；历史流 Source 置为 capture.SourceHistory 标记。
func (w *Writer) LoadRecent(limit int) ([]*capture.Flow, error) {
	rows, err := w.db.Query(`SELECT data FROM flows ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*capture.Flow
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		f, err := unmarshalFlow([]byte(data))
		if err != nil {
			log.Printf("persist: 反序列化历史流失败，跳过: %v", err)
			continue
		}
		f.Source = capture.SourceHistory
		// 历史流不携带置顶（置顶为会话内状态，不持久化）
		f.Pinned = false
		out = append(out, f)
	}
	// 倒序取出 → 反转为正序（旧→新）
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// LoadBody 回查单条流某侧（req|resp）消息体（惰性加载：查看历史流详情时调用）。
// 不存在记录返回 (nil, nil)。
func (w *Writer) LoadBody(flowID, kind string) ([]byte, error) {
	var enc string
	var blob []byte
	var rawLen int
	err := w.db.QueryRow(`SELECT enc, raw_len, data FROM bodies WHERE flow_id=? AND kind=?`, flowID, kind).
		Scan(&enc, &rawLen, &blob)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if enc == "zstd" {
		out, err := decompressBody(blob)
		if err != nil {
			return nil, fmt.Errorf("zstd 解压: %w", err)
		}
		return out, nil
	}
	return blob, nil
}

func (w *Writer) retentionLoop() {
	defer w.wg.Done()
	// 启动 2s 后首跑（不阻塞 Start），随后周期执行；stop 关闭即退出
	first := time.NewTimer(2 * time.Second)
	defer first.Stop()
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	run := func() {
		if err := w.Retention(); err != nil {
			log.Printf("persist: 保留清理失败: %v", err)
		}
	}
	for {
		select {
		case <-first.C:
			run()
		case <-ticker.C:
			run()
		case <-w.stop:
			return
		}
	}
}

// Retention 按 retainDays / maxMB 清理过期与超限数据。retainDays>0 删超龄流；
// maxMB>0 时在超量后从最旧流删除直到 DB 文件回落至阈值以下。
// M12：两处清理均排除已打标签的流（标签 = 归档，保留策略永不删已标记流，设计 §4.6）。
func (w *Writer) Retention() error {
	if w.retainDays > 0 {
		cutoff := time.Now().Add(-time.Duration(w.retainDays) * 24 * time.Hour).UnixMilli()
		if _, err := w.db.Exec(`DELETE FROM flows WHERE started_at < ?
			AND id NOT IN (SELECT flow_id FROM flow_tags)`, cutoff); err != nil {
			return err
		}
	}
	// 清理孤儿 body（flows 已删但 bodies 残留）
	if _, err := w.db.Exec(`DELETE FROM bodies WHERE flow_id NOT IN (SELECT id FROM flows)`); err != nil {
		return err
	}
	// 顺序关键（WAL 模式）：DELETE 产生空闲页 → incremental_vacuum 把空闲页释放**写入
	// WAL** → wal_checkpoint(TRUNCATE) 把该变更并入主库并截断 WAL，主 .db 文件尾空闲页
	// 才真正归还 OS。若先 checkpoint 后 vacuum，vacuum 帧滞留 WAL，主文件不缩、WAL 反增。
	if _, err := w.db.Exec(`PRAGMA incremental_vacuum`); err != nil {
		log.Printf("persist: incremental_vacuum (age): %v", err)
	}
	if _, err := w.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("persist: wal checkpoint: %v", err)
	}
	if w.maxMB > 0 {
		if err := w.enforceSize(int64(w.maxMB) << 20); err != nil {
			return err
		}
	}
	return nil
}

// enforceSize 超 maxBytes 时从最旧流起分批删除，直到 DB 文件回落至阈值以下（保留最近数据）。
// 关键：DELETE 只在文件内产生空闲页，须 wal_checkpoint(TRUNCATE) 后再 PRAGMA
// incremental_vacuum 才能把文件尾空闲页归还给 OS、真正缩小主 .db（库已开 auto_vacuum=
// INCREMENTAL，见 migrate）。逐批删除 + 收缩，避免一次删全表；无数据可删即返回。
func (w *Writer) enforceSize(maxBytes int64) error {
	main := w.dbPath()
	if main == "" {
		return nil
	}
	size := func() int64 {
		var s int64
		if fi, err := os.Stat(main); err == nil {
			s += fi.Size()
		}
		if fi, err := os.Stat(main + "-wal"); err == nil {
			s += fi.Size()
		}
		return s
	}
	const chunk = 64 // 每批删除条数：平衡 VACUUM 次数与删除粒度
	for size() > maxBytes {
		var ids []string
		// M12：排除已打标签的流（归档流不参与容量裁剪）；全表皆标签流时返回空 → 提前返回
		rows, err := w.db.Query(`SELECT id FROM flows
			WHERE id NOT IN (SELECT flow_id FROM flow_tags)
			ORDER BY started_at ASC, id ASC LIMIT ?`, chunk)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			return nil // 无历史可删（如空闲页碎片所致），交由后续写入复用
		}
		for _, id := range ids {
			if _, err := w.db.Exec(`DELETE FROM flows WHERE id=?`, id); err != nil {
				return err
			}
			if _, err := w.db.Exec(`DELETE FROM bodies WHERE flow_id=?`, id); err != nil {
				return err
			}
		}
		// 先 incremental_vacuum（空闲页释放写入 WAL），再 wal_checkpoint(TRUNCATE)
		// 并入主库并截断 WAL，主 .db 文件尾空闲页才真正归还 OS（顺序见 Retention 注释）。
		if _, err := w.db.Exec(`PRAGMA incremental_vacuum`); err != nil {
			log.Printf("persist: incremental_vacuum: %v", err)
		}
		if _, err := w.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			log.Printf("persist: enforce checkpoint: %v", err)
		}
	}
	return nil
}

// dbPath 从连接 DSN 还原文件路径（modernc 支持 PRAGMA database_list）
func (w *Writer) dbPath() string {
	var seq int
	var name, file string
	err := w.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file)
	if err != nil {
		return ""
	}
	return file
}

// Close 停止接收、刷盘剩余、关闭 DB。未 Start（仅 Open 用于读）时直接关 DB。
func (w *Writer) Close() {
	if !w.active.Swap(false) {
		if w.db != nil {
			_ = w.db.Close()
		}
		return
	}
	close(w.stop)
	w.wg.Wait()
	_ = w.db.Close()
}

// ---------- Flow 序列化 ----------

// marshalFlow 序列化 Flow：body 字节不入 JSON（单独存 bodies 表），
// 但把 body 长度写入 Message.BodyLen，保证历史流详情能显示正文大小。
// 此处浅拷贝即可：入队前 Enqueue 已做深拷贝隔离代理热路径；出队后 f 仅被单消费者
// goroutine 只读使用，json.Marshal 不修改对象，故只需复制 Request/Response 结构体
// 以便在副本上置 Body=nil，无需再复制 Header map / body 字节（避免每条流二次深拷贝）。
func marshalFlow(f *capture.Flow) ([]byte, error) {
	cp := *f
	if f.Request != nil {
		r := *f.Request
		r.BodyLen = len(f.Request.Body)
		r.Body = nil
		cp.Request = &r
	}
	if f.Response != nil {
		r := *f.Response
		r.BodyLen = len(f.Response.Body)
		r.Body = nil
		cp.Response = &r
	}
	return json.Marshal(&cp)
}

func unmarshalFlow(data []byte) (*capture.Flow, error) {
	var f capture.Flow
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// cloneFlow 深拷贝 Flow（含 Header map 与 body 字节切片），
// 使入队对象与代理热路径原地写入的 Flow 完全隔离，规避并发读写。
func cloneFlow(f *capture.Flow) *capture.Flow {
	cp := *f
	if f.Process != nil {
		p := *f.Process
		cp.Process = &p
	}
	if f.Timing != nil {
		t := *f.Timing
		cp.Timing = &t
	}
	if f.TLS != nil {
		t := *f.TLS
		if f.TLS.PeerCerts != nil {
			t.PeerCerts = append([]capture.PeerCert(nil), f.TLS.PeerCerts...)
		}
		cp.TLS = &t
	}
	if f.Request != nil {
		r := *f.Request
		r.Header = cloneHeader(f.Request.Header)
		if f.Request.Body != nil {
			r.Body = append([]byte(nil), f.Request.Body...)
		}
		cp.Request = &r
	}
	if f.Response != nil {
		r := *f.Response
		r.Header = cloneHeader(f.Response.Header)
		if f.Response.Body != nil {
			r.Body = append([]byte(nil), f.Response.Body...)
		}
		cp.Response = &r
	}
	return &cp
}

func cloneHeader(h map[string][]string) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func requestPath(rawURL string) string {
	// 与 app.toMeta 口径一致：path[?query]；解析失败降级空串
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return ""
	}
	p := u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}
