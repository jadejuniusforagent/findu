package db

import (
	"context"
	"testing"
	"time"
)

// TestHitlStoreNative 覆盖 Phase B6 的 pending/logs/详情/删除四条投影。
func TestHitlStoreNative(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ensureFinduCompatFixture(t, d)
	ctx := context.Background()
	hs := d.Hitl()

	// 清理并发失败运行可能遗留的固定 ID 残留（仅测试库）。
	if _, err := d.Exec(`DELETE FROM hitl_interrupts WHERE id LIKE 'hitl-%'`); err != nil {
		t.Fatalf("residue cleanup: %v", err)
	}

	// 零残留起点（fixture 每次新库）。
	if _, _, err := hs.ListPending(ctx, HitlFilter{}, 20, 0); err != nil {
		t.Fatalf("list pending: %v", err)
	}

	cid := "conv-hitl-native-" + time.Now().Format("150405.000000000")
	sfx := time.Now().Format("150405.000000000")
	ins := func(id, status, reviewer, decidedBy, decision, comment string, decidedAt *time.Time) {
		t.Helper()
		id = id + "-" + sfx
		var da any
		if decidedAt != nil {
			da = *decidedAt
		}
		if _, err := d.Exec(`INSERT INTO hitl_interrupts
			(id, conversation_id, message_id, mode, tool_name, tool_call_id, payload,
			 status, reviewer, decision, decision_comment, decided_by, created_at, decided_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),$13)`,
			id, cid, "msg-1", "approval", "execute", "call-1",
			`{"toolName":"execute","hitlApproval":{"auditModel":"glm-x"}}`,
			status, reviewer, decision, comment, decidedBy, da); err != nil {
			t.Fatalf("insert hitl_interrupts: %v", err)
		}
	}
	cleanup := func() { d.Exec(`DELETE FROM hitl_interrupts WHERE conversation_id=$1`, cid) }
	t.Cleanup(cleanup)
	ins("hitl-pending-1", "pending", "human", "", "", "", nil)
	ins("hitl-agent-pending-2", "pending", "audit_agent", "", "", "", nil) // 不进 pending 列表
	now := time.Now()
	da := now
	ins("hitl-log-3", "approved", "human", "human", "approve", "ok", &da)
	ins("hitl-log-4", "rejected", "audit_agent", "audit_agent", "reject", "audit agent: 实际操作：探测", &da)
	idOf := func(id string) string { return id + "-" + sfx }

	items, total, err := hs.ListPending(ctx, HitlFilter{}, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != idOf("hitl-pending-1") {
		t.Fatalf("pending total=%d items=%+v", total, items)
	}

	items, total, err = hs.ListLogs(ctx, HitlFilter{}, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("logs total=%d", total)
	}
	byID := map[string]*HitlHit{}
	for _, hh := range items {
		byID[hh.ID] = hh
	}
	if hh := byID[idOf("hitl-log-4")]; hh == nil || hh.AuditBackend != "openai" || hh.AuditModel != "glm-x" || hh.DecidedBy != "audit_agent" {
		t.Fatalf("log-4=%+v", hh)
	}
	if hh := byID[idOf("hitl-log-3")]; hh == nil || hh.AuditBackend != "" || hh.AuditModel != "" {
		t.Fatalf("log-3=%+v", hh)
	}

	got, err := hs.Get(ctx, idOf("hitl-log-3"))
	if err != nil || got.Decision != "approve" || got.DecidedBy != "human" {
		t.Fatalf("get log: %+v err=%v", got, err)
	}
	if got.DecidedAt == nil {
		t.Fatalf("decided_at not scanned")
	}

	n, err := hs.DeleteByIDs(ctx, []string{idOf("hitl-log-3")})
	if err != nil || n != 1 {
		t.Fatalf("delete by ids: n=%d err=%v", n, err)
	}
	n, err = hs.DeleteByIDs(ctx, []string{idOf("hitl-pending-1"), idOf("hitl-log-3")})
	if err != nil || n != 0 {
		t.Fatalf("pending rows must not be deleted: n=%d err=%v", n, err)
	}

	var cnt int
	if err := d.QueryRow(`SELECT COUNT(*) FROM hitl_interrupts WHERE conversation_id=$1`, cid).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("rows after delete=%d", cnt)
	}

	n, err = hs.DeleteWhere(ctx, HitlFilter{ConversationID: cid})
	if err != nil || n != 1 {
		t.Fatalf("delete where: n=%d err=%v", n, err)
	}
	// 已决策行删净；两条 pending 按约定保留（DeleteWhere/DeleteByIDs 均不删 pending）。
	if err := d.QueryRow(`SELECT COUNT(*) FROM hitl_interrupts WHERE conversation_id=$1`, cid).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 2 {
		t.Fatalf("rows after delete-where=%d (want 2 pending rows kept)", cnt)
	}
}
