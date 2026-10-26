package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestAgentConfigPartialUpdate 钉住 PUT /api/agents/{key}/config 的部分更新语义：
// 未传字段保持原值、负值入库夹取为 0（响应仍回显请求值）、无效 profile 400、
// 触发三元组缺省字段用存量值回填。对应批 4 第四刀的 applyAgentConfig 下沉。
func TestAgentConfigPartialUpdate(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("database unavailable (%v) — skipping", err)
	}
	if m.pg == nil {
		t.Skip("postgres unavailable — skipping")
	}
	t.Cleanup(func() { m.Close() })
	before, err := m.pg.GetAgentByKey("planner")
	if err != nil || before == nil {
		t.Fatalf("load planner: %v", err)
	}
	t.Cleanup(func() {
		_ = m.pg.SetAgentMaxTurns(before.Key, before.MaxTurns)
		_ = m.pg.SetAgentRunSeconds(before.Key, before.RunSecs)
		_ = m.pg.SetAgentTriggerBehavior(before.Key, before.TriggerRunMode, before.TriggerMergeMode, before.TriggerMaxParallel)
	})

	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	do := func(body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("PUT", "/api/agents/planner/config", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// 1) 只改 max_turns：run_seconds 必须保持原值。
	code, out := do(map[string]any{"max_turns": 7})
	if code != 200 || out["max_turns"] != float64(7) {
		t.Fatalf("partial update: code=%d out=%v", code, out)
	}
	after, _ := m.pg.GetAgentByKey("planner")
	if after.MaxTurns != 7 {
		t.Fatalf("max_turns persisted=%d, want 7", after.MaxTurns)
	}
	if after.RunSecs != before.RunSecs {
		t.Fatalf("run_seconds changed by partial update: %d -> %d", before.RunSecs, after.RunSecs)
	}

	// 2) 负值：入库夹取为 0，响应回显请求值（既有契约）。
	code, out = do(map[string]any{"max_turns": -3})
	if code != 200 || out["max_turns"] != float64(-3) {
		t.Fatalf("negative update: code=%d out=%v", code, out)
	}
	after, _ = m.pg.GetAgentByKey("planner")
	if after.MaxTurns != 0 {
		t.Fatalf("negative max_turns should clamp to 0, got %d", after.MaxTurns)
	}

	// 3) 无效 profile → 400，且不落库。
	code, _ = do(map[string]any{"llm_profile_id": 99999999})
	if code != 400 {
		t.Fatalf("invalid llm_profile_id should be 400, got %d", code)
	}

	// 4) 触发三元组：只传一个，其余用存量值回填。
	code, _ = do(map[string]any{"trigger_max_parallel": 3})
	if code != 200 {
		t.Fatalf("trigger partial update: %d", code)
	}
	after, _ = m.pg.GetAgentByKey("planner")
	if after.TriggerMaxParallel != 3 {
		t.Fatalf("trigger_max_parallel=%d, want 3", after.TriggerMaxParallel)
	}
	if after.TriggerRunMode != before.TriggerRunMode || after.TriggerMergeMode != before.TriggerMergeMode {
		t.Fatalf("trigger modes reset by partial update: %q/%q -> %q/%q",
			before.TriggerRunMode, before.TriggerMergeMode, after.TriggerRunMode, after.TriggerMergeMode)
	}
}
