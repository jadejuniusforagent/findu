package db

import (
	"os"
	"testing"
)

// TestVulnerabilityNativeLifecycle 覆盖 B1：create（默认值/回填）→ get →
// list/filter/count → update → stats → filter-options → delete（级联清
// project_facts 关联）→ batch-delete，外加审计写入。
func TestVulnerabilityNativeLifecycle(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ensureFinduCompatFixture(t, d)
	vs := d.Vulnerabilities()

	// 建临时会话与项目，验证 conversation → project 回填与级联。
	ps := d.Projects()
	proj, err := ps.Create(&Project{Name: "step6d-b1-vuln-canary"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _ = ps.DeleteProjectNative(proj.ID) })
	convID := "step6d-b1-conv-" + proj.ID[:8]
	if _, err := d.Exec(`INSERT INTO conversations (id, title, agent_key, created_at, updated_at, project_id)
		VALUES ($1, 'b1 canary', 'step6d-b1', now(), now(), $2)`, convID, proj.ID); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM conversations WHERE id=$1`, convID) })

	// create：显式 project 不回填路径 + 默认 status
	created, err := vs.CreateVulnerabilityNative(&VulnerabilityDTO{
		ConversationID: convID, Title: "b1 canary vuln", Severity: "high",
		Description: "canary desc", Evidence: "poc", TaskTag: "b1tag",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = vs.DeleteVulnerabilityNative(created.ID) })
	if created.Status != "open" || created.ID == "" {
		t.Fatalf("create defaults: status=%q id=%q", created.Status, created.ID)
	}
	// conversation → project 回填（未显式给 project_id）
	projBackfilled, err := vs.CreateVulnerabilityNative(&VulnerabilityDTO{
		ConversationID: convID, Title: "b1 no project", Severity: "low",
	})
	if err != nil {
		t.Fatalf("create backfill: %v", err)
	}
	t.Cleanup(func() { _ = vs.DeleteVulnerabilityNative(projBackfilled.ID) })
	if projBackfilled.ProjectID != proj.ID {
		t.Fatalf("project backfill: got %q want %q", projBackfilled.ProjectID, proj.ID)
	}

	// get：形状 + 派生 task 字段（无 batch_tasks 关联 → 空）
	got, err := vs.GetVulnerabilityNative(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "b1 canary vuln" || got.Severity != "high" || got.TaskID != "" || got.TaskQueueID != "" {
		t.Fatalf("get shape: %+v", got)
	}
	// get 不存在
	if _, err := vs.GetVulnerabilityNative("no-such-vuln"); err == nil {
		t.Fatal("get missing should fail")
	}

	// list + count：按 severity 与 search 过滤
	items, err := vs.ListVulnerabilitiesNative(100, 0, VulnerabilityFilter{ConversationID: convID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("list conv rows=%d (want 2)", len(items))
	}
	items, err = vs.ListVulnerabilitiesNative(100, 0, VulnerabilityFilter{Search: "canary desc"})
	if err != nil {
		t.Fatalf("list search: %v", err)
	}
	if len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("list search rows=%d", len(items))
	}
	n, err := vs.CountVulnerabilitiesNative(VulnerabilityFilter{TaskTag: "b1tag"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("count task_tag=%d (want 1)", n)
	}

	// update：全字段覆盖 + 回读
	created.Status = "confirmed"
	created.Severity = "critical"
	if err := vs.UpdateVulnerabilityNative(created.ID, created); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := vs.GetVulnerabilityNative(created.ID)
	if err != nil || updated.Status != "confirmed" || updated.Severity != "critical" {
		t.Fatalf("updated: err=%v status=%q severity=%q", err, updated.Status, updated.Severity)
	}

	// stats：按会话过滤的严重度/状态计数
	stats, err := vs.GetVulnerabilityStatsNative(VulnerabilityFilter{ConversationID: convID})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 2 || stats.BySeverity["critical"] != 1 || stats.BySeverity["low"] != 1 ||
		stats.ByStatus["confirmed"] != 1 || stats.ByStatus["open"] != 1 {
		t.Fatalf("stats: %+v", stats)
	}

	// filter-options：形状（7 组键）
	options, err := vs.GetVulnerabilityFilterOptionsNative()
	if err != nil {
		t.Fatalf("filter options: %v", err)
	}
	for _, key := range []string{"vulnerability_ids", "conversation_ids", "project_ids", "task_ids", "queue_ids", "conversation_tags", "task_tags"} {
		if _, ok := options[key]; !ok {
			t.Fatalf("filter options missing %q: %v", key, options)
		}
	}
	if len(options["vulnerability_ids"]) < 2 {
		t.Fatalf("filter options ids=%v", options["vulnerability_ids"])
	}

	// delete：project_facts.related_vulnerability_id 级联清空
	fact, err := ps.UpsertProjectFact(&ProjectFact{
		ProjectID: proj.ID, FactKey: "b1.linked", Summary: "linked",
		RelatedVulnerabilityID: created.ID,
	})
	if err != nil {
		t.Fatalf("upsert fact: %v", err)
	}
	t.Cleanup(func() { _ = ps.DeleteProjectFactNative(fact.ID) })
	if err := vs.DeleteVulnerabilityNative(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var rid string
	if err := d.QueryRow(`SELECT COALESCE(related_vulnerability_id,'') FROM project_facts WHERE id=$1`, fact.ID).Scan(&rid); err != nil {
		t.Fatalf("fact after vuln delete: %v", err)
	}
	if rid != "" {
		t.Fatalf("related_vulnerability_id not cleared: %q", rid)
	}
	if _, err := vs.GetVulnerabilityNative(created.ID); err == nil {
		t.Fatal("vuln still exists after delete")
	}

	// batch-delete：按 tag 删剩余一条
	deleted, err := vs.DeleteVulnerabilitiesByFilterNative(VulnerabilityFilter{ConversationID: convID})
	if err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("batch deleted=%d (want 1)", deleted)
	}

	// 审计 Append：写一条并回读
	as := d.AuditLogs()
	if err := as.Append(&AuditLog{Category: "vulnerability", Action: "create", Result: "success",
		ResourceType: "vulnerability", ResourceID: "b1-audit-probe", Message: "b1 audit probe"}); err != nil {
		t.Fatalf("audit append: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM audit_logs WHERE resource_id='b1-audit-probe'`) })
	var auditCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE resource_id='b1-audit-probe'`).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit rows=%d (want 1)", auditCount)
	}
}
