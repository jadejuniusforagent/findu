package database

import (
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newDraftTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "skill_draft.db"), zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSaveSkillDraftAndGet(t *testing.T) {
	db := newDraftTestDB(t)
	if err := db.UpsertSkillCandidate(&SkillCandidate{
		SkillName: "api-recon", Source: SkillCandidateSourceMiss, Score: 5, Occurrences: 3,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := db.LoadSkillCandidates(false)
	if err != nil || len(list) != 1 {
		t.Fatalf("want 1 candidate, got %d err=%v", len(list), err)
	}
	id := list[0].ID

	if err := db.SaveSkillDraft(id, "对目标做 API 侦察", "会话 c1 / 技能缺口", 66, 80, "# API Recon\n\n步骤…", "deepseek-chat"); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	got, err := db.GetSkillCandidate(id)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != SkillCandidateStatusDrafted {
		t.Fatalf("status = %q, want drafted", got.Status)
	}
	if got.Capability != "对目标做 API 侦察" || got.Priority != 66 || got.Completion != 80 {
		t.Fatalf("draft fields not persisted: %+v", got)
	}
	if !strings.Contains(got.DraftBody, "API Recon") {
		t.Fatalf("draft body missing: %q", got.DraftBody)
	}
	if got.DraftModel != "deepseek-chat" {
		t.Fatalf("draft model = %q", got.DraftModel)
	}
}

func TestUpsertDoesNotClobberDraft(t *testing.T) {
	db := newDraftTestDB(t)
	if err := db.UpsertSkillCandidate(&SkillCandidate{SkillName: "x", Source: SkillCandidateSourceMiss, Occurrences: 1, Score: 1}); err != nil {
		t.Fatal(err)
	}
	list, _ := db.LoadSkillCandidates(false)
	id := list[0].ID
	if err := db.SaveSkillDraft(id, "cap", "ref", 50, 60, "body", "m"); err != nil {
		t.Fatal(err)
	}
	// 再来一轮蒸馏：只累加计数，不得清掉草稿字段
	if err := db.UpsertSkillCandidate(&SkillCandidate{SkillName: "x", Source: SkillCandidateSourceMiss, Occurrences: 1, Score: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetSkillCandidate(id)
	if got.DraftBody != "body" || got.Capability != "cap" || got.Status != SkillCandidateStatusDrafted {
		t.Fatalf("draft clobbered by upsert: %+v", got)
	}
	if got.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2", got.Occurrences)
	}
}
