package persist

import (
	"path/filepath"
	"testing"
)

// TestFlowIntentsCRUD 意图写入/批量查询/覆盖语义。
func TestFlowIntentsCRUD(t *testing.T) {
	w := archiveWriter(t)

	// 空库批量查询不报错、无结果
	if got, err := w.IntentsForFlows([]string{"f1", "f2"}); err != nil || len(got) != 0 {
		t.Fatalf("空库查询 = %d, %v", len(got), err)
	}
	// 空切片 upsert 为 no-op
	if err := w.UpsertFlowIntents(nil); err != nil {
		t.Fatalf("空切片 upsert: %v", err)
	}

	// 批量写入
	in := []FlowIntent{
		{FlowID: "f1", Seq: 1, Intent: "提交登录认证", Confidence: "high", NeedsBody: true},
		{FlowID: "f2", Seq: 2, Intent: "拉取用户列表", Confidence: "medium"},
		{FlowID: "", Seq: 3, Intent: "空 FlowID 应被跳过", Confidence: "low"},
	}
	if err := w.UpsertFlowIntents(in); err != nil {
		t.Fatalf("UpsertFlowIntents: %v", err)
	}

	got, err := w.IntentsForFlows([]string{"f1", "f2", "f3", ""})
	if err != nil {
		t.Fatalf("IntentsForFlows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应命中 2 条，得 %d", len(got))
	}
	f1 := got["f1"]
	if f1.Intent != "提交登录认证" || f1.Confidence != "high" || !f1.NeedsBody || f1.Seq != 1 || f1.UpdatedAt == 0 {
		t.Fatalf("f1 = %+v", f1)
	}
	if f2 := got["f2"]; f2.Intent != "拉取用户列表" || f2.Confidence != "medium" || f2.NeedsBody {
		t.Fatalf("f2 = %+v", f2)
	}

	// 覆盖：同 flow_id 重新解析更新为新值
	if err := w.UpsertFlowIntents([]FlowIntent{{FlowID: "f1", Seq: 9, Intent: "重新解析后的意图", Confidence: "low", NeedsBody: false}}); err != nil {
		t.Fatalf("覆盖 upsert: %v", err)
	}
	got, err = w.IntentsForFlows([]string{"f1"})
	if err != nil {
		t.Fatalf("覆盖后查询: %v", err)
	}
	if f1 := got["f1"]; f1.Intent != "重新解析后的意图" || f1.Seq != 9 || f1.Confidence != "low" || f1.NeedsBody {
		t.Fatalf("覆盖后 f1 = %+v", f1)
	}
}

// TestFlowIntentsLegacyReadOnly 旧库（无 flow_intents 表）只读查询容错为空、不报错。
func TestFlowIntentsLegacyReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	w, err := OpenArchive(path)
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	// 模拟旧库：删掉 flow_intents 表后关闭
	if _, err := w.db.Exec(`DROP TABLE flow_intents`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	w.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()
	got, err := ro.IntentsForFlows([]string{"f1"})
	if err != nil || len(got) != 0 {
		t.Fatalf("旧库只读查询 = %d, %v（期望空且不报错）", len(got), err)
	}
}
