package finding

// finding_retests 表（人工/agent 复测流水）的存储层。
//
// 迁移边界（Step 3 批 3 第七刀）：CreateFindingRetest（快照跨 findings×assets×
// tasks×task_constraints＋新建复测会话）与 FinishFindingRetest（终态要联动漏洞
// 状态与推送事件）留在 db 装配层；本文件收单表读写与状态机。装配层复用本包导出的
// FindingRetestCols / ScanFindingRetest 保持同一列序。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"finduai/core/store"
)

// FindingRetestAgentKey 是复测会话绑定的 agent key（与 GetFindingRetestContext /
// record_finding_retest_result 工具的归属校验配套）。
const FindingRetestAgentKey = "retester"

var ErrRetestNotRunning = errors.New("本次复测已结束或尚未开始，请从漏洞详情发起新的复测")

// FindingRetest is an immutable historical test once its conversation turn ends.
// Snapshot is only loaded for the agent, never sent with the history list.
type FindingRetest struct {
	ID             int64           `json:"id"`
	FindingID      int64           `json:"finding_id"`
	ConversationID *string         `json:"conversation_id"`
	Status         string          `json:"status"`
	Verdict        string          `json:"verdict"`
	Notes          string          `json:"notes"`
	Snapshot       json.RawMessage `json:"snapshot,omitempty"`
	Summary        string          `json:"summary"`
	Evidence       string          `json:"evidence"`
	Error          string          `json:"error"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

const retestCols = `id, finding_id, conversation_id, status, verdict, notes, summary, evidence, error, created_at, started_at, finished_at`

// FindingRetestCols 导出复测列清单，供装配层的 CreateFindingRetest 拼 RETURNING。
const FindingRetestCols = retestCols

func scanRetest(row interface{ Scan(...any) error }) (*FindingRetest, error) {
	r := &FindingRetest{}
	err := row.Scan(&r.ID, &r.FindingID, &r.ConversationID, &r.Status, &r.Verdict, &r.Notes,
		&r.Summary, &r.Evidence, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// ScanFindingRetest 把一行扫描成 *FindingRetest，导出给装配层（CreateFindingRetest）。
func ScanFindingRetest(row interface{ Scan(...any) error }) (*FindingRetest, error) {
	return scanRetest(row)
}

// ActiveFindingRetest is the small status payload polled by the findings list.
// Finding IDs use the same string representation as the findings API.
type ActiveFindingRetest struct {
	ID             int64  `json:"id"`
	FindingID      int64  `json:"finding_id,string"`
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
}

func ListActiveFindingRetests(ctx context.Context, q store.ContextQuerier) ([]ActiveFindingRetest, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, finding_id, conversation_id, status FROM finding_retests
	WHERE status IN ('pending','running') AND conversation_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ActiveFindingRetest{}
	for rows.Next() {
		var item ActiveFindingRetest
		if err := rows.Scan(&item.ID, &item.FindingID, &item.ConversationID, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *FindingRetest) InitialMessage() string {
	msg := fmt.Sprintf("请复测漏洞 #%d。先调用 get_finding_retest_context 读取本会话关联的原始证据与约束，再执行针对性验证，最后调用 record_finding_retest_result 保存结论。", r.FindingID)
	if r.Notes != "" {
		msg += "\n\n本次复测补充说明：\n" + r.Notes
	}
	return msg
}

func ListFindingRetests(q store.Querier, findingID int64) ([]*FindingRetest, error) {
	rows, err := q.Query(`SELECT `+retestCols+` FROM finding_retests WHERE finding_id=$1 ORDER BY id DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FindingRetest{}
	for rows.Next() {
		r, err := scanRetest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func FindingRetestForConversation(ctx context.Context, q store.ContextQuerier, conversationID string) (*FindingRetest, error) {
	r, err := scanRetest(q.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE conversation_id=$1`, conversationID))
	if err != nil || r == nil {
		return r, err
	}
	err = q.QueryRowContext(ctx, `SELECT snapshot FROM finding_retests WHERE id=$1`, r.ID).Scan(&r.Snapshot)
	return r, err
}

// FailPendingRetestForConversation seals a conversation's unfinished retest when
// the runner could not even load it — the retest ID is unknown on that path, so
// the conversation ID is the only handle. Without it a transient read error
// leaves the row 'pending' forever: the findings list keeps showing 复测中 and
// every later 发起复测 is deduped against a run that is not happening, with only
// a process restart (RecoverFindingRetests) able to clear it.
func FailPendingRetestForConversation(q store.Querier, conversationID string, reason string) error {
	_, err := q.Exec(`UPDATE finding_retests SET status='failed', error=$2, finished_at=now()
		WHERE conversation_id=$1 AND status IN ('pending','running')`, conversationID, reason)
	return err
}

func StartFindingRetest(ctx context.Context, q store.ContextQuerier, id int64) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE finding_retests SET status='running', started_at=now() WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordFindingRetestResult never accepts a finding ID: ownership comes from the
// runtime conversation. Identical retries are safe; a second verdict is refused.
func RecordFindingRetestResult(ctx context.Context, q store.ContextQuerier, conversationID string, verdict, summary, evidence string) error {
	if verdict != "reproduced" && verdict != "fixed" && verdict != "inconclusive" {
		return errors.New("verdict 必须为 reproduced / fixed / inconclusive")
	}
	summary, evidence = strings.TrimSpace(summary), strings.TrimSpace(evidence)
	if summary == "" || evidence == "" {
		return errors.New("summary 与 evidence 不能为空；无法确认时说明实际检查及阻塞原因")
	}
	if len(summary) > 16000 || len(evidence) > 128000 {
		return errors.New("复测结论过长（summary ≤ 16KB，evidence ≤ 128KB）")
	}
	res, err := q.ExecContext(ctx, `UPDATE finding_retests SET verdict=$2,summary=$3,evidence=$4
	WHERE conversation_id=$1 AND status='running' AND (verdict='' OR (verdict=$2 AND summary=$3 AND evidence=$4))`, conversationID, verdict, summary, evidence)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrRetestNotRunning
	}
	return nil
}

func RecoverFindingRetests(q store.Querier) error {
	_, err := q.Exec(`UPDATE finding_retests SET status='stopped', error='服务重启，复测已中断，请重新发起', finished_at=now() WHERE status IN ('pending','running')`)
	return err
}
