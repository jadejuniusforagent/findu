package db

// 会话域存储下沉（批 3 第五刀）的转发链验证用例：全部经由 db 转发方法打真实
// PG，证明 dconv 链路、类型别名与实施后的 SQL 都通。
// 删除语义的细粒度用例（决策竞态、timeout 不覆盖人工决定、dead-rule 溯源、
// 旁路父会话级联）已在 intercept_*_test.go / side_questions_test.go 钉住，不重复。

import (
	"errors"
	"finduai/domain/conversation"
	"testing"
	"time"

	"finduai/sidequestion"
)

func TestConversationPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// 规则 CRUD 往返（走 db 转发）。
	rule, err := d.CreateInterceptRule("bridge", "tool_name", "string", "Bash", "deny", "msg", -1, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_rules WHERE id=$1`, rule.ID) })
	find := func(id int64) *conversation.InterceptRule {
		rules, _ := d.ListInterceptRules()
		for i, r := range rules {
			if r.ID == id {
				return &rules[i]
			}
		}
		return nil
	}
	if got := find(rule.ID); got == nil || got.Action != "deny" || got.Pattern != "Bash" {
		t.Fatalf("created rule not in list: %+v", rule)
	}
	if err = d.ToggleInterceptRule(rule.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := find(rule.ID); got == nil || got.Enabled {
		t.Fatal("toggle lost")
	}

	// 审批流水生命周期：pending → 决策 → 执行回执 → 详情可读。
	audit := &conversation.InterceptAudit{RunID: "run-bridge", ToolUseID: "call-bridge", Correlation: "exact", ExecutionStatus: "not_started"}
	id, err := d.CreateInterceptPending(rule.ID, "", "bridge-task", "agent", "Bash", []byte(`{"cmd":"ls"}`), "review", audit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE id=$1`, id) })
	pending, err := d.GetInterceptPending(id)
	if err != nil || pending == nil || pending.Status != "pending" || pending.DecisionSource != "rule" {
		t.Fatalf("pending: %+v err=%v", pending, err)
	}
	if ok, err := d.ResolveIntercept(id, "allowed", "allow", "bridge"); err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if err = d.CompleteIntercept(id, "run-bridge", "call-bridge", "succeeded", "ok", false); err != nil {
		t.Fatal(err)
	}
	detail, err := d.GetInterceptDetail(id)
	if err != nil || detail == nil || detail.Status != "allowed" ||
		detail.Audit == nil || detail.Audit.ExecutionStatus != "succeeded" || detail.RuleName != "bridge" {
		t.Fatalf("detail: %+v err=%v", detail, err)
	}

	// 旁路快照往返（走 db 转发，含 ContextBeginner 事务链）。
	c, err := d.CreateConversation("mainagent", "bridge side", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteConversation(c.ID) })
	s := sidequestion.Snapshot{
		Parent:     sidequestion.Parent{ConversationID: c.ID},
		RunID:      1,
		Version:    1,
		CapturedAt: time.Now().UTC(),
	}
	if err = d.SaveSideSnapshot(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	got, err := d.SideSnapshot(t.Context(), s.Parent.Key())
	if err != nil || got == nil || got.Parent.ConversationID != c.ID {
		t.Fatalf("side snapshot: %+v err=%v", got, err)
	}
	// 父会话删除后写入被拒（ErrSideParentGone 的桥接侧确认），快照随级联消失。
	if err = d.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	if err = d.SaveSideSnapshot(t.Context(), s); !errors.Is(err, conversation.ErrSideParentGone) {
		t.Fatalf("deleted parent: %v", err)
	}
	if got, _ = d.SideSnapshot(t.Context(), s.Parent.Key()); got != nil {
		t.Fatal("snapshot survived cascade")
	}
}
