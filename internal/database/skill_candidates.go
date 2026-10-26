package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"go.uber.org/zap"
)

// SkillCandidate 是一条「技能候选」（P0-2.1 任务结束自动蒸馏的产物）+ 其提炼草稿（2.2）。
//
// 信号来源：
//   - source=skill_miss    模型点名调用 skill 但该技能不存在（缺口信号）
//   - source=tool_failure  同一工具在本轮 run 内失败/被拦截 >= 阈值（用法信号）
//
// 生命周期 status：new（候选）→ drafted（已生成草稿）→ approved（人工审核通过，已写入 skills_dir）
//
//	| rejected（人工驳回）
type SkillCandidate struct {
	ID             string
	SkillName      string
	Source         string
	ConversationID string
	ProjectID      string
	Evidence       string
	Occurrences    int
	Score          int
	Status         string
	// 2.2 提炼草稿字段（由 LLM 生成，人工审核后转正）
	Capability string // 该 skill 能做到的功能（一句话）
	SourceRef  string // 出处：来自哪个实践会话/项目/信号
	Priority   int    // 提炼优先级（0-100，越大越优先）
	Completion int    // 当前完成度（0-100，LLM 自评草稿完整程度）
	DraftBody  string // 生成的 SKILL.md 草稿全文
	DraftModel string // 生成草稿使用的模型
	ReviewedBy string // 审核人（可选）
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const (
	SkillCandidateSourceMiss        = "skill_miss"
	SkillCandidateSourceToolFailure = "tool_failure"

	SkillCandidateStatusNew      = "new"
	SkillCandidateStatusDrafted  = "drafted"
	SkillCandidateStatusApproved = "approved"
	SkillCandidateStatusRejected = "rejected"
)

// ToolFailureGroup 是「同一工具在本窗口内的失败聚合」，供蒸馏信号使用。
type ToolFailureGroup struct {
	ToolName  string
	Count     int
	LastError string
}

func newSkillCandidateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "sc_" + time.Now().Format("20060102150405.000000000")
	}
	return "sc_" + hex.EncodeToString(b)
}

const skillCandidateColumns = `id, skill_name, source, conversation_id, project_id, evidence, occurrences, score, status,
	capability, source_ref, priority, completion, draft_body, draft_model, reviewed_by, created_at, updated_at`

func scanSkillCandidate(scan func(dest ...any) error) (*SkillCandidate, error) {
	var c SkillCandidate
	var reviewedBy sql.NullString
	if err := scan(
		&c.ID, &c.SkillName, &c.Source, &c.ConversationID, &c.ProjectID, &c.Evidence,
		&c.Occurrences, &c.Score, &c.Status,
		&c.Capability, &c.SourceRef, &c.Priority, &c.Completion, &c.DraftBody, &c.DraftModel,
		&reviewedBy, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if reviewedBy.Valid {
		c.ReviewedBy = reviewedBy.String
	}
	return &c, nil
}

// UpsertSkillCandidate 按 (skill_name, source) 去重累加：已存在则 occurrences 累加、
// score 取较大值、刷新 evidence/会话归因与 updated_at；草稿类字段不覆盖。
func (db *DB) UpsertSkillCandidate(c *SkillCandidate) error {
	if c == nil || strings.TrimSpace(c.SkillName) == "" {
		return nil
	}
	if c.Source == "" {
		c.Source = SkillCandidateSourceMiss
	}
	if c.Status == "" {
		c.Status = SkillCandidateStatusNew
	}
	if c.Occurrences <= 0 {
		c.Occurrences = 1
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now

	query := `
		INSERT INTO skill_candidates
		(id, skill_name, source, conversation_id, project_id, evidence, occurrences, score, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(skill_name, source) DO UPDATE SET
			occurrences = occurrences + excluded.occurrences,
			score = MAX(score, excluded.score),
			conversation_id = excluded.conversation_id,
			project_id = CASE WHEN excluded.project_id <> '' THEN excluded.project_id ELSE project_id END,
			evidence = excluded.evidence,
			updated_at = excluded.updated_at
	`
	_, err := db.Exec(query,
		newSkillCandidateID(), c.SkillName, c.Source, c.ConversationID, c.ProjectID,
		c.Evidence, c.Occurrences, c.Score, c.Status, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		db.logger.Error("写入技能候选失败", zap.Error(err), zap.String("skillName", c.SkillName), zap.String("source", c.Source))
		return err
	}
	return nil
}

// LoadSkillCandidates 加载候选榜（默认排除已拒绝），按 score、occurrences 降序。
func (db *DB) LoadSkillCandidates(includeRejected bool) ([]*SkillCandidate, error) {
	query := `SELECT ` + skillCandidateColumns + ` FROM skill_candidates`
	if !includeRejected {
		query += ` WHERE status <> 'rejected' `
	}
	query += ` ORDER BY score DESC, occurrences DESC, updated_at DESC `

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SkillCandidate
	for rows.Next() {
		c, err := scanSkillCandidate(rows.Scan)
		if err != nil {
			db.logger.Warn("加载技能候选失败", zap.Error(err))
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// GetSkillCandidate 取单条候选。
func (db *DB) GetSkillCandidate(id string) (*SkillCandidate, error) {
	row := db.QueryRow(`SELECT `+skillCandidateColumns+` FROM skill_candidates WHERE id = ?`, id)
	c, err := scanSkillCandidate(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

// UpdateSkillCandidateStatus 更新候选状态；reviewedBy 非空时一并写入。
func (db *DB) UpdateSkillCandidateStatus(id, status, reviewedBy string) error {
	if strings.TrimSpace(reviewedBy) != "" {
		_, err := db.Exec(`UPDATE skill_candidates SET status = ?, reviewed_by = ?, updated_at = ? WHERE id = ?`,
			status, reviewedBy, time.Now(), id)
		return err
	}
	_, err := db.Exec(`UPDATE skill_candidates SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now(), id)
	return err
}

// SaveSkillDraft 写入/刷新某条候选的提炼草稿（LLM 生成结果），状态置 drafted。
func (db *DB) SaveSkillDraft(id, capability, sourceRef string, priority, completion int, draftBody, model string) error {
	_, err := db.Exec(`
		UPDATE skill_candidates
		SET capability = ?, source_ref = ?, priority = ?, completion = ?, draft_body = ?, draft_model = ?,
		    status = ?, updated_at = ?
		WHERE id = ?`,
		capability, sourceRef, priority, completion, draftBody, model, SkillCandidateStatusDrafted, time.Now(), id,
	)
	if err != nil {
		db.logger.Error("保存技能草稿失败", zap.Error(err), zap.String("id", id))
		return err
	}
	return nil
}

// ClearSkillCandidates 清空候选榜。
func (db *DB) ClearSkillCandidates() error {
	if _, err := db.Exec(`DELETE FROM skill_candidates`); err != nil {
		db.logger.Error("清空技能候选失败", zap.Error(err))
		return err
	}
	db.logger.Info("已清空技能候选榜")
	return nil
}

// LoadConversationToolFailures 聚合某个会话在 since 之后的工具失败/拦截，按工具分组，
// 只返回失败次数 >= minCount 的工具。失败口径：status 属于 failed / blocked（不含 cancelled）。
func (db *DB) LoadConversationToolFailures(conversationID string, since time.Time, minCount int) ([]ToolFailureGroup, error) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, nil
	}
	if minCount < 1 {
		minCount = 1
	}
	query := `
		SELECT tool_name, COUNT(*) AS n, COALESCE(MAX(error), '') AS last_error
		FROM tool_executions
		WHERE conversation_id = ? AND created_at >= ? AND status IN ('failed', 'blocked')
		GROUP BY tool_name
		HAVING COUNT(*) >= ?
		ORDER BY n DESC
	`
	rows, err := db.Query(query, conversationID, since, minCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ToolFailureGroup
	for rows.Next() {
		var g ToolFailureGroup
		if err := rows.Scan(&g.ToolName, &g.Count, &g.LastError); err != nil {
			db.logger.Warn("加载会话工具失败聚合失败", zap.Error(err))
			continue
		}
		out = append(out, g)
	}
	return out, nil
}
