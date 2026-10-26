package database

import (
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func newSkillCandidatesTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "skill_candidates.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUpsertSkillCandidateAccumulates(t *testing.T) {
	db := newSkillCandidatesTestDB(t)

	if err := db.UpsertSkillCandidate(&SkillCandidate{
		SkillName:      "api-recon",
		Source:         SkillCandidateSourceMiss,
		ConversationID: "conv-1",
		ProjectID:      "proj-1",
		Evidence:       "第一次缺口",
		Occurrences:    1,
		Score:          1,
	}); err != nil {
		t.Fatalf("upsert #1: %v", err)
	}
	if err := db.UpsertSkillCandidate(&SkillCandidate{
		SkillName:      "api-recon",
		Source:         SkillCandidateSourceMiss,
		ConversationID: "conv-2",
		Evidence:       "第二次缺口",
		Occurrences:    1,
		Score:          5,
	}); err != nil {
		t.Fatalf("upsert #2: %v", err)
	}

	list, err := db.LoadSkillCandidates(false)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(list))
	}
	got := list[0]
	if got.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2", got.Occurrences)
	}
	if got.Score != 5 {
		t.Fatalf("score = %d, want 5 (max)", got.Score)
	}
	if got.ConversationID != "conv-2" {
		t.Fatalf("conversation_id = %q, want conv-2 (latest)", got.ConversationID)
	}
	if got.ProjectID != "proj-1" {
		t.Fatalf("project_id = %q, want proj-1 (keep on empty)", got.ProjectID)
	}
}

func TestLoadSkillCandidatesExcludesRejected(t *testing.T) {
	db := newSkillCandidatesTestDB(t)

	if err := db.UpsertSkillCandidate(&SkillCandidate{SkillName: "keep", Source: SkillCandidateSourceMiss}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSkillCandidate(&SkillCandidate{SkillName: "drop", Source: SkillCandidateSourceMiss}); err != nil {
		t.Fatal(err)
	}
	list, _ := db.LoadSkillCandidates(true)
	if len(list) != 2 {
		t.Fatalf("want 2 before edit, got %d", len(list))
	}
	var dropID string
	for _, c := range list {
		if c.SkillName == "drop" {
			dropID = c.ID
		}
	}
	if err := db.UpdateSkillCandidateStatus(dropID, SkillCandidateStatusRejected, "tester"); err != nil {
		t.Fatal(err)
	}
	visible, _ := db.LoadSkillCandidates(false)
	if len(visible) != 1 || visible[0].SkillName != "keep" {
		t.Fatalf("rejected candidate should be hidden, got %+v", visible)
	}
}

func TestLoadConversationToolFailures(t *testing.T) {
	db := newSkillCandidatesTestDB(t)
	now := time.Now()

	ins := func(id, tool, status string, at time.Time) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO tool_executions (id, tool_name, arguments, status, start_time, conversation_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, tool, "{}", status, at, "convX", at,
		); err != nil {
			t.Fatalf("insert exec %s: %v", id, err)
		}
	}
	ins("e1", "nmap", "failed", now.Add(-time.Minute))
	ins("e2", "nmap", "blocked", now.Add(-2*time.Minute))
	ins("e3", "nmap", "completed", now.Add(-3*time.Minute))     // 成功不计入
	ins("e4", "curl", "failed", now.Add(-4*time.Minute))        // 只有 1 次，不达阈值
	ins("e5", "dirsearch", "failed", now.Add(-180*time.Minute)) // 窗口外

	groups, err := db.LoadConversationToolFailures("convX", now.Add(-time.Hour), 2)
	if err != nil {
		t.Fatalf("load failures: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d: %+v", len(groups), groups)
	}
	if groups[0].ToolName != "nmap" || groups[0].Count != 2 {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
}

func TestClearSkillCandidates(t *testing.T) {
	db := newSkillCandidatesTestDB(t)
	if err := db.UpsertSkillCandidate(&SkillCandidate{SkillName: "x", Source: SkillCandidateSourceMiss}); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearSkillCandidates(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	list, _ := db.LoadSkillCandidates(true)
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}
