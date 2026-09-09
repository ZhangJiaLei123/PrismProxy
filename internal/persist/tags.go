// tags.go M12 标签与归档（标签 = 归档，设计 §4.1/§4.3/§4.4）。
//
// tags 表为标签字典（id=hash、name=展示名），flow_tags 为流↔标签多对多关联。
// 标签方法仅在归档 Writer（OpenArchive，retainDays/maxMB=0）上调用；但表由共享
// migrate() 创建，录制 Writer 单独打开旧库时也会幂等补建（retention 排除子查询
// 依赖 flow_tags 存在）。
package persist

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"

	"prismproxy/internal/capture"
)

// TagInfo 标签字典项（复盘侧栏/弹窗历史标签下拉用）。
type TagInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Count      int    `json:"count"`       // 关联流数
	CreatedAt  int64  `json:"createdAt"`   // unix 毫秒
	LastUsedAt int64  `json:"lastUsedAt"`  // unix 毫秒
}

// validTagID 标签 id 格式：t_ + 12 位 hex（碰撞升级为 16 位时可选后 4 位）。
var validTagID = regexp.MustCompile(`^t_[0-9a-f]{12}([0-9a-f]{4})?$`)

// ValidTagID 校验标签 id 形态（hash id 后路由段无中文/空格/保留字冲突面）。
func ValidTagID(id string) bool { return validTagID.MatchString(id) }

// NormalizeTagName 展示归一化：trim + 内部空白折叠为单空格。
// 用于 name 展示与 hash 输入；空串/纯空白返回 ""（由调用方拒绝）。
func NormalizeTagName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// hashTagName 由归一化名计算标签 id：去全部空白 + lower 后 sha256，取前 hexLen 位 hex。
// 去空白 + lower 使「Login/login」「登录 流程」与「登录流程」归一为同一 id。
func hashTagName(norm string, hexLen int) string {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.ToLower(norm))
	sum := sha256.Sum256([]byte(compact))
	return "t_" + hex.EncodeToString(sum[:])[:hexLen]
}

// TagID 对外暴露的标签 id 计算（默认 12 位；碰撞场景由 UpsertTag 内部升级 16 位）。
func TagID(name string) string { return hashTagName(NormalizeTagName(name), 12) }

// tagIDForName 按归一化名解析库内真实标签 id（M12 审计修复 L1）。
// 必须 id 与 name 双匹配：仅按 id 计数时，12 位 hash 若被异名标签（16 位升级行）
// 占用，会误返回他标签的 id。12 位优先、16 位兜底；查不到返回 ""。
func (w *Writer) tagIDForName(tx *sql.Tx, norm string) (string, error) {
	for _, n := range []int{12, 16} {
		id := hashTagName(norm, n)
		var occupyName string
		err := tx.QueryRow(`SELECT name FROM tags WHERE id=?`, id).Scan(&occupyName)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return "", err
		}
		if NormalizeTagName(occupyName) == norm {
			return id, nil
		}
		// 同 id 被异名占用：继续尝试更长的 hash
	}
	return "", nil
}

// resolveNewTagID 为「新插入/改名」的标签选一个未被异名占用的 id（L1 碰撞升级）。
// 12 位 id 未被占用直接用；被异名占用升级 16 位；16 位也碰撞返回错误（理论概率 ~2^-64）。
// 调用方须已确认该 norm 在库中不存在（tagIDForName 返回 ""）。
func resolveNewTagID(tx *sql.Tx, norm string) (string, error) {
	for _, n := range []int{12, 16} {
		id := hashTagName(norm, n)
		var occupyName string
		err := tx.QueryRow(`SELECT name FROM tags WHERE id=?`, id).Scan(&occupyName)
		if err == sql.ErrNoRows {
			return id, nil
		}
		if err != nil {
			return "", err
		}
		if NormalizeTagName(occupyName) == norm {
			// 同名行已存在（理论上 tagIDForName 已先命中），直接复用
			return id, nil
		}
		// 异名碰撞：升级更长 hash
	}
	return "", fmt.Errorf("标签 id 冲突（12/16 位均碰撞），请更换标签名后重试")
}

// UpsertTag 新建标签（已存在则仅刷新 last_used_at），返回标签字典项。
// name 为空返回错误；长度上限（1–40 字符）由调用方（App 层）校验。
// 碰撞防线：12 位 id 若被不同归一化名占用，升级 16 位 hex 重试一次。
func (w *Writer) UpsertTag(name string) (*TagInfo, error) {
	norm := NormalizeTagName(name)
	if norm == "" {
		return nil, fmt.Errorf("标签名不能为空")
	}
	now := time.Now().UnixMilli()
	var out *TagInfo
	err := w.withTx(func(tx *sql.Tx) error {
		id, err := w.tagIDForName(tx, norm)
		if err != nil {
			return err
		}
		if id == "" {
			// 碰撞防线：12 位 id 被异名占用升级 16 位，再碰撞报错（L1）
			newID, nerr := resolveNewTagID(tx, norm)
			if nerr != nil {
				return nerr
			}
			id = newID
			if _, err := tx.Exec(
				`INSERT INTO tags(id,name,created_at,last_used_at) VALUES(?,?,?,?)`,
				id, norm, now, now); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE tags SET last_used_at=? WHERE id=?`, now, id); err != nil {
				return err
			}
		}
		ti, err := scanTagInfo(tx.QueryRow(`SELECT t.id,t.name,
			(SELECT COUNT(1) FROM flow_tags ft WHERE ft.tag_id=t.id),
			t.created_at,t.last_used_at FROM tags t WHERE t.id=?`, id))
		if err != nil {
			return err
		}
		out = ti
		return nil
	})
	return out, err
}

// ArchiveFlows 批量归档流（M12）：单事务 upsert 一批 flows+bodies，内置 BUSY 退避重试
// （打标按 50–100 条/批提交，压缩单次持锁窗口，设计 §4.2/§4.3）。幂等。
// 内部逐条深拷贝，与调用方持有的 store 内存流隔离。
func (w *Writer) ArchiveFlows(flows []*capture.Flow) error {
	if len(flows) == 0 {
		return nil
	}
	cps := make([]*capture.Flow, 0, len(flows))
	for _, f := range flows {
		if f != nil {
			cps = append(cps, cloneFlow(f))
		}
	}
	var lastErr error
	for attempt := 0; attempt < archiveMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(archiveRetryBackoff)
		}
		err := w.writeBatch(cps)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusy(err) {
			return err
		}
		log.Printf("persist: 批量归档遇到 BUSY（第 %d 次重试，%d 条）: %v", attempt+1, len(cps), err)
	}
	return fmt.Errorf("批量归档 %d 条重试耗尽: %w", len(cps), lastErr)
}

// AddFlowTags 幂等批量写入流↔标签关联（单事务），并刷新标签 last_used_at。
// 返回本次实际新增的关联数（已存在的关联 DO NOTHING，不计入；M12 审计修复 L4，
// Tagged 口径统一为「新增关联数」）。调用方须已先 UpsertTag 保证标签存在；
// flow 行是否已归档不影响关联写入。
func (w *Writer) AddFlowTags(flowIDs []string, tagID string, taggedAt int64) (int, error) {
	if len(flowIDs) == 0 {
		return 0, nil
	}
	added := 0
	err := w.withTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT INTO flow_tags(flow_id,tag_id,tagged_at) VALUES(?,?,?)
			 ON CONFLICT(flow_id,tag_id) DO NOTHING`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, fid := range flowIDs {
			res, err := stmt.Exec(fid, tagID, taggedAt)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				added++
			}
		}
		_, err = tx.Exec(`UPDATE tags SET last_used_at=? WHERE id=?`, taggedAt, tagID)
		return err
	})
	return added, err
}

// ListTags 返回全部标签（按最近使用倒序），含关联流计数。
func (w *Writer) ListTags() ([]TagInfo, error) {
	rows, err := w.db.Query(`SELECT t.id,t.name,
		(SELECT COUNT(1) FROM flow_tags ft WHERE ft.tag_id=t.id) AS cnt,
		t.created_at,t.last_used_at
		FROM tags t ORDER BY t.last_used_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagInfo
	for rows.Next() {
		ti, err := scanTagInfo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ti)
	}
	return out, rows.Err()
}

// scanner 兼容 *sql.Row 与 *sql.Rows。
type scanner interface {
	Scan(dest ...any) error
}

func scanTagInfo(s scanner) (*TagInfo, error) {
	var ti TagInfo
	if err := s.Scan(&ti.ID, &ti.Name, &ti.Count, &ti.CreatedAt, &ti.LastUsedAt); err != nil {
		return nil, err
	}
	return &ti, nil
}

// FlowsByTag 分页返回某标签下的流（时间倒序；tagID="all" = 全部已标记流，按最近打标倒序）。
// limit<=0 用默认 200，上限 1000；offset 为跳过条数。仅元数据（body 惰性回查），
// 复盘流以 capture.SourceHistory 标记（与启动补载口径一致）。
func (w *Writer) FlowsByTag(tagID string, limit, offset int) ([]*capture.Flow, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	var (
		rows *sql.Rows
		err  error
	)
	if tagID == "all" {
		rows, err = w.db.Query(`SELECT f.data FROM flows f
			WHERE f.id IN (SELECT flow_id FROM flow_tags)
			ORDER BY f.started_at DESC, f.id DESC LIMIT ? OFFSET ?`, limit, offset)
	} else {
		if !ValidTagID(tagID) {
			return nil, fmt.Errorf("非法标签 id")
		}
		rows, err = w.db.Query(`SELECT f.data FROM flows f
			JOIN flow_tags ft ON ft.flow_id=f.id
			WHERE ft.tag_id=?
			ORDER BY f.started_at DESC, f.id DESC LIMIT ? OFFSET ?`, tagID, limit, offset)
	}
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
			continue
		}
		f.Source = capture.SourceHistory
		f.Pinned = false
		out = append(out, f)
	}
	return out, rows.Err()
}

// CountTaggedFlows 返回全部已打标签的流总数（去重；复盘侧栏「全部已标记」计数用）。
func (w *Writer) CountTaggedFlows() (int, error) {
	var n int
	err := w.db.QueryRow(`SELECT COUNT(DISTINCT flow_id) FROM flow_tags`).Scan(&n)
	return n, err
}

// FlowIDsByTag 返回某标签下全部关联流 id（不分页；删除标签前收集受影响流用，
// 标签下流数有界——单标签量级远小于 SQL 参数上限）。tagID 须为合法 hash id。
func (w *Writer) FlowIDsByTag(tagID string) ([]string, error) {
	if !ValidTagID(tagID) {
		return nil, fmt.Errorf("非法标签 id")
	}
	rows, err := w.db.Query(`SELECT flow_id FROM flow_tags WHERE tag_id=?`, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LoadFlowByID 按 id 取单条流（元数据，不含 body）；不存在返回 (nil, nil)。
// 复盘页详情走此路径（流可能已被内存环形淘汰，不能走 store）。
func (w *Writer) LoadFlowByID(flowID string) (*capture.Flow, error) {
	var data string
	err := w.db.QueryRow(`SELECT data FROM flows WHERE id=?`, flowID).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := unmarshalFlow([]byte(data))
	if err != nil {
		return nil, err
	}
	f.Source = capture.SourceHistory
	f.Pinned = false
	return f, nil
}

// FlowExists 判断流是否已在库（flows 行存在）。打标时内存未命中用于决定 skipped 口径。
func (w *Writer) FlowExists(flowID string) (bool, error) {
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(1) FROM flows WHERE id=?`, flowID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// TagsForFlows 批量查询多个流的标签名（启动/项目切换补载后回填 App tagIndex 用）。
// 返回 flowID → 标签名列表（按标签 last_used_at 倒序）；无标签的流不在 map 中。
func (w *Writer) TagsForFlows(flowIDs []string) (map[string][]string, error) {
	out := make(map[string][]string)
	if len(flowIDs) == 0 {
		return out, nil
	}
	// 分块查询，避免 IN 参数过多（SQLITE_MAX_VARIABLE_NUMBER 现代版本 32766，保守 500/批）
	const chunk = 500
	for start := 0; start < len(flowIDs); start += chunk {
		end := start + chunk
		if end > len(flowIDs) {
			end = len(flowIDs)
		}
		batch := flowIDs[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT ft.flow_id, t.name FROM flow_tags ft
			JOIN tags t ON t.id=ft.tag_id
			WHERE ft.flow_id IN (` + placeholders + `)
			ORDER BY t.last_used_at DESC`
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := w.db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var flowID, name string
			if err := rows.Scan(&flowID, &name); err != nil {
				rows.Close()
				return nil, err
			}
			out[flowID] = append(out[flowID], name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RenameTag 重命名标签。newName 归一化后：
//   - 目标名对应的标签已存在（且不是自己）→ 合并：flow_tags 关联迁移到目标标签
//     （INSERT OR IGNORE 保旧 tagged_at），last_used_at 取 max，删旧标签行；
//   - 否则更新本行 id（hash 随名变）+ name；
//
// 返回受影响的流 id 列表（供调用方 COW 刷新 tagIndex）。
func (w *Writer) RenameTag(oldID, newName string) (affectedIDs []string, err error) {
	norm := NormalizeTagName(newName)
	if norm == "" {
		return nil, fmt.Errorf("标签名不能为空")
	}
	if !ValidTagID(oldID) {
		return nil, fmt.Errorf("非法标签 id")
	}
	now := time.Now().UnixMilli()
	err = w.withTx(func(tx *sql.Tx) error {
		var oldName string
		var oldCreated, oldUsed int64
		err := tx.QueryRow(`SELECT name,created_at,last_used_at FROM tags WHERE id=?`, oldID).
			Scan(&oldName, &oldCreated, &oldUsed)
		if err == sql.ErrNoRows {
			return fmt.Errorf("标签不存在或已被删除")
		}
		if err != nil {
			return err
		}
		// 收集受影响流（迁移/改名后这些流的标签名集合变化）
		rows, err := tx.Query(`SELECT flow_id FROM flow_tags WHERE tag_id=?`, oldID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var fid string
			if err := rows.Scan(&fid); err != nil {
				rows.Close()
				return err
			}
			affectedIDs = append(affectedIDs, fid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		targetID, err := w.tagIDForName(tx, norm)
		if err != nil {
			return err
		}
		if targetID != "" && targetID != oldID {
			// 合并到既有标签：关联迁移（已存在的关联保留旧 tagged_at），last_used_at 取 max
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO flow_tags(flow_id,tag_id,tagged_at)
				 SELECT flow_id,?,tagged_at FROM flow_tags WHERE tag_id=?`, targetID, oldID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM flow_tags WHERE tag_id=?`, oldID); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`UPDATE tags SET last_used_at=(CASE WHEN last_used_at>? THEN last_used_at ELSE ? END)
				 WHERE id=?`, oldUsed, oldUsed, targetID); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM tags WHERE id=?`, oldID); err != nil {
				return err
			}
			return nil
		}
		// 改名（hash 新 id）：先插新行再迁移关联、删旧行（L1：碰撞统一走 12→16 升级）
		newID, nerr := resolveNewTagID(tx, norm)
		if nerr != nil {
			return nerr
		}
		if newID == oldID {
			// 同名（归一化后未变）：仅刷新展示名
			if _, err := tx.Exec(`UPDATE tags SET name=?,last_used_at=? WHERE id=?`, norm, now, oldID); err != nil {
				return err
			}
			return nil
		}
		if _, err := tx.Exec(`INSERT INTO tags(id,name,created_at,last_used_at) VALUES(?,?,?,?)`,
			newID, norm, oldCreated, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE flow_tags SET tag_id=? WHERE tag_id=?`, newID, oldID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE id=?`, oldID); err != nil {
			return err
		}
		return nil
	})
	return affectedIDs, err
}

// DeleteTag 删除标签。deleteFlows=false 仅删标签与关联（流保留在库）；
// deleteFlows=true 同时删除「不再被任何标签引用」的关联流 flows 行及其 bodies 行
// （仍被其他标签引用的流保留）。返回被物理删除的流 id 列表（供 App 层 tombstone）。
func (w *Writer) DeleteTag(tagID string, deleteFlows bool) (deletedFlowIDs []string, err error) {
	if !ValidTagID(tagID) {
		return nil, fmt.Errorf("非法标签 id")
	}
	err = w.withTx(func(tx *sql.Tx) error {
		var exist int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM tags WHERE id=?`, tagID).Scan(&exist); err != nil {
			return err
		}
		if exist == 0 {
			return fmt.Errorf("标签不存在或已被删除")
		}
		if deleteFlows {
			// 该标签下、且不被任何其他标签引用的流
			rows, err := tx.Query(
				`SELECT flow_id FROM flow_tags ft1 WHERE tag_id=?
				 AND NOT EXISTS (SELECT 1 FROM flow_tags ft2
				                 WHERE ft2.flow_id=ft1.flow_id AND ft2.tag_id<>?)`,
				tagID, tagID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var fid string
				if err := rows.Scan(&fid); err != nil {
					rows.Close()
					return err
				}
				deletedFlowIDs = append(deletedFlowIDs, fid)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, fid := range deletedFlowIDs {
				if _, err := tx.Exec(`DELETE FROM flows WHERE id=?`, fid); err != nil {
					return err
				}
				if _, err := tx.Exec(`DELETE FROM bodies WHERE flow_id=?`, fid); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM flow_tags WHERE tag_id=?`, tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE id=?`, tagID); err != nil {
			return err
		}
		return nil
	})
	return deletedFlowIDs, err
}

// withTx 事务辅助：自动 Begin/Commit，Rollback 兜底。
func (w *Writer) withTx(fn func(*sql.Tx) error) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
