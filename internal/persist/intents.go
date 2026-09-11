package persist

import (
	"fmt"
	"strings"
	"time"
)

// FlowIntent AI 意图解析结果（M13 §7）：intent 模式解析完成后按流持久化到归档库，
// 复盘列表/详情回读展示（路径列下意图概要，点击进详细分析）。
// 每流一行，重新解析覆盖旧值。
type FlowIntent struct {
	FlowID     string
	Seq        int    // 流序号（对应送审 [#n]）
	Intent     string // 意图概要（一句话）
	Confidence string // high|medium|low（AI 输出异常值原样保存，前端降级展示）
	NeedsBody  bool   // 是否需要正文才能精判
	UpdatedAt  int64  // unix 毫秒
}

// UpsertFlowIntents 批量写入/覆盖意图（单事务；空切片 no-op，空 FlowID 跳过）。
// 须在可写归档 Writer 上调用（App 层经 acquireArchive 保证）。
func (w *Writer) UpsertFlowIntents(items []FlowIntent) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO flow_intents(flow_id,seq,intent,confidence,needs_body,updated_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(flow_id) DO UPDATE SET
seq=excluded.seq,intent=excluded.intent,confidence=excluded.confidence,
needs_body=excluded.needs_body,updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		if it.FlowID == "" {
			continue
		}
		nb := 0
		if it.NeedsBody {
			nb = 1
		}
		if _, err := stmt.Exec(it.FlowID, it.Seq, it.Intent, it.Confidence, nb, now); err != nil {
			return fmt.Errorf("upsert intent %s: %w", it.FlowID, err)
		}
	}
	return tx.Commit()
}

// IntentsForFlows 批量查询多个流的意图，返回 flowID → FlowIntent（无意图的流不在 map 中）。
// 旧只读库无表时返回空 map、不报错（读侧容错，同 ListReviewIgnores）。
func (w *Writer) IntentsForFlows(flowIDs []string) (map[string]FlowIntent, error) {
	out := make(map[string]FlowIntent)
	if len(flowIDs) == 0 {
		return out, nil
	}
	// 分块查询，避免 IN 参数过多（同 TagsForFlows 口径 500/批）
	const chunk = 500
	for start := 0; start < len(flowIDs); start += chunk {
		end := start + chunk
		if end > len(flowIDs) {
			end = len(flowIDs)
		}
		batch := flowIDs[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT flow_id, seq, intent, confidence, needs_body, updated_at FROM flow_intents
			WHERE flow_id IN (` + placeholders + `)`
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := w.db.Query(q, args...)
		if err != nil {
			if isNoSuchTable(err) {
				return out, nil
			}
			return nil, err
		}
		for rows.Next() {
			var it FlowIntent
			var nb int
			if err := rows.Scan(&it.FlowID, &it.Seq, &it.Intent, &it.Confidence, &nb, &it.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			it.NeedsBody = nb != 0
			out[it.FlowID] = it
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
