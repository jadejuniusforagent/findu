package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestWorkflowStoreNative 覆盖 Phase B5 的 definitions CRUD（版本推进语义）、
// runs/node_runs 投影与 awaiting_hitl 列表（PG 方言与 findu
// internal/database/workflow.go 对齐）。
func TestWorkflowStoreNative(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	ensureFinduCompatFixture(t, d)
	ctx := context.Background()
	s := d.Workflows()

	// 幂等清理（仅测试库；canary 形态 id 均带前缀）。
	cleanup := func() {
		d.Exec(`DELETE FROM workflow_node_runs WHERE run_id LIKE 'wfc-%'`)
		d.Exec(`DELETE FROM workflow_runs WHERE id LIKE 'wfc-%'`)
		d.Exec(`DELETE FROM workflow_definitions WHERE id LIKE 'wfc-%'`)
	}
	cleanup()
	t.Cleanup(cleanup)

	// List 空态保持 nil（JSON workflows:null）。
	if items, err := s.WorkflowDefinitions(ctx, false); err != nil || items != nil {
		t.Fatalf("empty list: %v %v", items, err)
	}

	// 新建：version<=0 → 1。
	wf := &WorkflowDefinition{ID: "wfc-demo", Name: "演示流程", Description: "canary", Enabled: true,
		GraphJSON: `{"nodes":[],"edges":[],"config":{}}`}
	if err := s.WorkflowDefinitionUpsert(ctx, wf); err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	saved, err := s.WorkflowDefinitionByID(ctx, "wfc-demo")
	if err != nil || saved == nil || saved.Version != 1 || !saved.Enabled || saved.CreatedAt.IsZero() {
		t.Fatalf("get new: %+v err=%v", saved, err)
	}

	// 更新：existing.Version+1。
	saved.Name = "演示流程 v2"
	if err := s.WorkflowDefinitionUpsert(ctx, saved); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	again, _ := s.WorkflowDefinitionByID(ctx, "wfc-demo")
	if again.Version != 2 || again.Name != "演示流程 v2" {
		t.Fatalf("version bump: %+v", again)
	}

	// 显式更高版本被尊重。
	again2 := *again
	again2.Version = 5
	_ = s.WorkflowDefinitionUpsert(ctx, &again2)
	third, _ := s.WorkflowDefinitionByID(ctx, "wfc-demo")
	if third.Version != 5 {
		t.Fatalf("explicit version: %d", third.Version)
	}

	// 禁用工作流默认列表不可见（enabled=0）。
	if _, err := d.Exec(`UPDATE workflow_definitions SET enabled = 0 WHERE id = 'wfc-demo'`); err != nil {
		t.Fatal(err)
	}
	if items, err := s.WorkflowDefinitions(ctx, false); err != nil || len(items) != 0 {
		t.Fatalf("disabled hidden: %v err=%v", items, err)
	}
	items, err := s.WorkflowDefinitions(ctx, true)
	if err != nil || len(items) != 1 {
		t.Fatalf("includeDisabled: %v err=%v", len(items), err)
	}

	// 缺失 → nil,nil（handler 404 语义）。
	if got, err := s.WorkflowDefinitionByID(ctx, "no-such"); err != nil || got != nil {
		t.Fatalf("missing: %v err=%v", got, err)
	}

	// Run + NodeRuns 投影（NULL → 空串/nil 指针；finished_at 有值带指针）。
	started := time.Now().Add(-2 * time.Hour)
	if _, err := d.Exec(
		`INSERT INTO workflow_runs (id, workflow_id, workflow_version, conversation_id, project_id, role_id, status, input_json, output_json, error, pending_hitl_node_id, pending_hitl_json, started_at, finished_at)
		 VALUES ('wfc-run1','wfc-demo',5,NULL,'proj-a','role-a','completed','{"in":1}','{"out":2}',NULL,NULL,NULL,$1,$2)`,
		started, started.Add(time.Hour)); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	run, err := s.WorkflowRunByID(ctx, "wfc-run1")
	if err != nil || run == nil || run.WorkflowID != "wfc-demo" || run.WorkflowVersion != 5 || run.ConversationID != "" ||
		run.ProjectID != "proj-a" || run.Status != "completed" || run.InputJSON != `{"in":1}` || run.Error != "" ||
		run.PendingHITLNodeID != "" || run.FinishedAt == nil {
		t.Fatalf("run: %+v err=%v", run, err)
	}
	if _, err := s.WorkflowRunByID(ctx, "no-such"); err != nil {
		t.Fatalf("missing run should be nil,nil not error: %v", err)
	}

	// 节点运行（乱序 started_at 断言 ASC 排序 + JSON 可序列化）。
	t0 := started.Add(5 * time.Minute)
	for _, nr := range []struct {
		id, node string
		at       time.Time
	}{
		{"wfc-n2", "node-b", t0.Add(time.Minute)},
		{"wfc-n1", "node-a", t0},
	} {
		if _, err := d.Exec(
			`INSERT INTO workflow_node_runs (id, run_id, node_id, status, input_json, output_json, error, started_at, finished_at)
			 VALUES ($1,'wfc-run1',$2,'success','{}','{}',NULL,$3,$3)`, nr.id, nr.node, nr.at); err != nil {
			t.Fatalf("seed node run: %v", err)
		}
	}
	nodes, err := s.WorkflowNodeRuns(ctx, "wfc-run1")
	if err != nil || len(nodes) != 2 || nodes[0].ID != "wfc-n1" || nodes[1].ID != "wfc-n2" {
		t.Fatalf("node runs: %+v err=%v", nodes, err)
	}
	out, jerr := json.Marshal(nodes)
	if jerr != nil || len(out) == 0 {
		t.Fatalf("node runs json: %v", jerr)
	}

	// awaiting_hitl：仅命中目标状态。
	if _, err := d.Exec(
		`INSERT INTO workflow_runs (id, workflow_id, workflow_version, status, started_at)
		 VALUES ('wfc-pending','wfc-demo',5,'awaiting_hitl',$1)`, started); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	pending, err := s.WorkflowRunsAwaitingHITL(ctx, "", 50)
	if err != nil || len(pending) != 1 || pending[0].ID != "wfc-pending" {
		t.Fatalf("awaiting list: %+v err=%v", pending, err)
	}
	pendingConv, err := s.WorkflowRunsAwaitingHITL(ctx, "conv-not-match", 50)
	if err != nil || len(pendingConv) != 0 {
		t.Fatalf("awaiting filtered: %+v err=%v", pendingConv, err)
	}

	// Delete：不落 runs（runs 无 definitions FK）。
	if err := s.WorkflowDefinitionDelete(ctx, "wfc-demo"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := s.WorkflowDefinitionByID(ctx, "wfc-demo"); got != nil {
		t.Fatalf("definition should be gone")
	}
	var runCnt int
	if err := d.QueryRow(`SELECT COUNT(*) FROM workflow_runs WHERE id LIKE 'wfc-%'`).Scan(&runCnt); err != nil {
		t.Fatal(err)
	}
	if runCnt != 2 {
		t.Fatalf("runs must survive definition delete: %d", runCnt)
	}
	if err := s.WorkflowDefinitionDelete(ctx, ""); err == nil {
		t.Fatalf("empty id should error")
	}
}
