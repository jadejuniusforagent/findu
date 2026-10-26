package db

// 复测存储层已迁 domain/finding（Step 3 批 3 第七刀）。投件：本文件保留
// CreateFindingRetest / FinishFindingRetest 两个跨域函数于装配层——
//   - CreateFindingRetest：一个事务锁 findings 行 + 读 assets/task_constraints/tasks
//     拼快照 + 新建复测会话（conversations/conversation_activities，会话域）+ 写流水；
//   - FinishFindingRetest：终态联动漏洞状态 + 推送事件（SetFindingStatusTx，
//     通知域）。
// 其余（单表读写、状态机）转发 domain/finding；ScanFindingRetest/FindingRetestCols
// 从域包导出，列序单一来源。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	dconv "finduai/domain/conversation"

	dfe "finduai/domain/finding"
)

func (d *DB) ListActiveFindingRetests(ctx context.Context) ([]dfe.ActiveFindingRetest, error) {
	return dfe.ListActiveFindingRetests(ctx, d)
}

func (d *DB) ListFindingRetests(findingID int64) ([]*dfe.FindingRetest, error) {
	return dfe.ListFindingRetests(d, findingID)
}

func (d *DB) FindingRetestForConversation(ctx context.Context, conversationID string) (*dfe.FindingRetest, error) {
	return dfe.FindingRetestForConversation(ctx, d, conversationID)
}

func (d *DB) FailPendingRetestForConversation(conversationID string, reason string) error {
	return dfe.FailPendingRetestForConversation(d, conversationID, reason)
}

func (d *DB) StartFindingRetest(ctx context.Context, id int64) (bool, error) {
	return dfe.StartFindingRetest(ctx, d, id)
}

func (d *DB) RecordFindingRetestResult(ctx context.Context, conversationID string, verdict, summary, evidence string) error {
	return dfe.RecordFindingRetestResult(ctx, d, conversationID, verdict, summary, evidence)
}

func (d *DB) RecoverFindingRetests() error {
	return dfe.RecoverFindingRetests(d)
}

// ---- 跨域函数，留在装配层（见文件头）----

// CreateFindingRetest atomically snapshots the source, creates its conversation
// and persists the first message. A finding row lock deduplicates simultaneous
// clicks across clients; an existing active run is returned without dispatching.
func (d *DB) CreateFindingRetest(ctx context.Context, findingID int64, notes string) (*dfe.FindingRetest, *dconv.Conversation, bool, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var title string
	var snapshot []byte
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(f.name,''), NULLIF(f.vulnclass,''), '未分类'),
	jsonb_build_object('finding', to_jsonb(f),
	 'assets', COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM assets a WHERE f.asset_ids @> to_jsonb(ARRAY[a.id])), '[]'::jsonb),
	 'constraints', COALESCE((SELECT jsonb_agg(to_jsonb(c)) FROM task_constraints c JOIN tasks t ON t.exploration_id=c.exploration_id WHERE t.id=f.task_id), '[]'::jsonb))
	FROM findings f WHERE f.id=$1 FOR UPDATE OF f`, findingID).Scan(&title, &snapshot)
	if err != nil {
		return nil, nil, false, err
	}
	r, err := dfe.ScanFindingRetest(tx.QueryRowContext(ctx, `SELECT `+dfe.FindingRetestCols+` FROM finding_retests WHERE finding_id=$1 AND status IN ('pending','running')`, findingID))
	if err != nil {
		return nil, nil, false, err
	}
	if r != nil {
		return r, nil, false, nil
	}
	// Keep the title within the same limit as ordinary conversations.
	if runes := []rune(title); len(runes) > 100 {
		title = string(runes[:100])
	}
	c, err := dconv.ScanConversation(tx.QueryRowContext(ctx, `INSERT INTO conversations(agent_key,title) VALUES ($1,$2) RETURNING `+dconv.ConversationCols,
		dfe.FindingRetestAgentKey, fmt.Sprintf("复测 #%d · %s", findingID, title)))
	if err != nil {
		return nil, nil, false, err
	}
	r, err = dfe.ScanFindingRetest(tx.QueryRowContext(ctx, `INSERT INTO finding_retests(finding_id,conversation_id,notes,snapshot) VALUES ($1,$2,$3,$4) RETURNING `+dfe.FindingRetestCols,
		findingID, c.ID, strings.TrimSpace(notes), snapshot))
	if err != nil {
		return nil, nil, false, err
	}
	msg := r.InitialMessage()
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_activities(conversation_id,worker,kind,summary,detail) VALUES ($1,$2,'user',$3,$4)`,
		c.ID, dfe.FindingRetestAgentKey, fmt.Sprintf("请复测漏洞 #%d", findingID), msg)
	if err != nil {
		return nil, nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return r, c, true, nil
}

// FinishFindingRetest seals the result. Cancellation/failure takes precedence
// over a staged verdict so an interrupted test cannot appear successfully fixed.
// Only a newly completed fixed verdict updates triage, in the same transaction.
func (d *DB) FinishFindingRetest(id int64, status, reason string) error {
	if status != "completed" && status != "failed" && status != "stopped" {
		return errors.New("invalid terminal retest status")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the finding before the retest, matching creation and cascading deletion.
	var findingID int64
	err = tx.QueryRow(`SELECT f.id FROM findings f WHERE f.id=(SELECT finding_id FROM finding_retests WHERE id=$1) FOR UPDATE OF f`, id).Scan(&findingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Finding/retest already deleted.
	}
	if err != nil {
		return err
	}
	var finalStatus, verdict string
	err = tx.QueryRow(`UPDATE finding_retests SET
	status=CASE WHEN $2='completed' AND verdict='' THEN 'failed' ELSE $2 END,
	error=CASE WHEN $2='completed' AND verdict='' THEN 'Agent 未保存复测结论，请查看会话后重新复测' ELSE $3 END,
	finished_at=now() WHERE id=$1 AND status IN ('pending','running') RETURNING status,verdict`, id, status, reason).Scan(&finalStatus, &verdict)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // A replay must not overwrite a later manual triage decision.
	}
	if err != nil {
		return err
	}
	if finalStatus == "completed" && verdict == "fixed" {
		// 走带通知的版本，与人工在详情页改状态共用同一套语义。
		//
		// 此前这里是裸的 UPDATE：复测判「已修复」时状态确实变了，但配了
		// on_status_change 的渠道完全收不到推送——状态在界面上悄悄变了，
		// 运维要打开平台才知道。状态更新与推送事件必须一起落库，
		// SetFindingStatusTx 内部处理了「状态没变就不登记」等细节。
		// 用 context.Background()：本函数整条都是无 ctx 的旧风格（d.Begin()/
		// tx.QueryRow/tx.Exec），没有可传递的取消信号，硬加一个 ctx 参数会
		// 牵动 server 侧调用点与多处测试，超出本次改动的范围。
		if _, _, _, _, err := SetFindingStatusTx(context.Background(), tx, findingID, dfe.FindingFixed); err != nil {
			return err
		}
	}
	return tx.Commit()
}
