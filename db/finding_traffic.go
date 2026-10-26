package db

// 流量证据管线的跨域部分留在装配层（Step 3 批 3 第七刀）：
//   - LockTaskEvidenceTx / LockFindingEvidenceTx —— 取证锁跨 findings×tasks×
//     task_archives 状态机；
//   - RecordFindingTx / RecordFinding（ExplorationStore）—— 一个事务跨探索节点/
//     锚点/边 + findings + 推送事件登记；
//   - EditFindingTraffic / SetFindingReportVersionByNodeID —— 取证版本锁协议；
//   - PopulateFindingTrafficIDs —— 探索域节点富化。
//
// 其余（快照值对象、InsertEvidenceSnapshotTx/AddFindingTrafficTx/FindingTrafficTx、
// WithEvidenceTx、GetFindingTraffic）已迁 domain/finding，本文件留别名与转发，
// evidence 包等调用点零改动。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"finduai/domain/exploration"
	dnotify "finduai/domain/notify"
	"fmt"
	"strings"

	"finduai/notify"

	dfe "finduai/domain/finding"
)

func (d *DB) WithEvidenceTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return dfe.WithEvidenceTx(ctx, d, fn)
}

// This lock covers the evidence filesystem as well as its SQL references. All
// processes sharing the database use it, including readers, exports and GC.

// LockTaskEvidenceTx serializes writes with archive queueing (which locks the
// same task row). Once queued, its snapshot must not acquire new evidence.
//
// 留在装配层：tasks 行锁 × task_archives 状态机（任务域）。
func LockTaskEvidenceTx(tx *sql.Tx, taskID int64) error {
	if taskID == 0 {
		return nil
	}
	var deleted sql.NullTime
	if err := tx.QueryRow(`SELECT deleted_at FROM tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&deleted); err != nil {
		return err
	}
	if deleted.Valid {
		return ErrTaskArchiveState
	}
	var state string
	err := tx.QueryRow(`SELECT state FROM task_archives WHERE task_id=$1`, taskID).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && state != ArchiveFailed {
		return ErrTaskArchiveState
	}
	return nil
}

// LockFindingEvidenceTx deduplicates/validates an evidence write against the
// finding's own lock and its task's archive state. 留在装配层：链上一个 findings
// 读 + 任务域的 LockTaskEvidenceTx。
func LockFindingEvidenceTx(tx *sql.Tx, findingID int64, version *int64) error {
	var taskID sql.NullInt64
	if err := tx.QueryRow(`SELECT task_id FROM findings WHERE id=$1`, findingID).Scan(&taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dfe.ErrFindingNotFound
		}
		return err
	}
	if err := LockTaskEvidenceTx(tx, taskID.Int64); err != nil {
		return err
	}
	var current int64
	if err := tx.QueryRow(`SELECT evidence_version FROM findings WHERE id=$1 FOR UPDATE`, findingID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dfe.ErrFindingNotFound
		}
		return err
	}
	if version != nil && *version != current {
		return dfe.ErrEvidenceConflict
	}
	return nil
}

func (d *DB) GetFindingTraffic(ctx context.Context, findingID int64) (out *dfe.FindingTraffic, err error) {
	return dfe.GetFindingTraffic(ctx, d, findingID)
}

// evidence 包在事务里直接调用这三个包级函数，保留薄转发（函数本体已迁 domain/finding）。

func (d *DB) EditFindingTraffic(ctx context.Context, findingID, bindingID, version int64, role, note *string, remove bool, order []int64) error {
	if role != nil && !dfe.ValidTrafficRole(*role) {
		return errors.New("无效的流量用途")
	}
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		if err := LockFindingEvidenceTx(tx, findingID, &version); err != nil {
			return err
		}
		if order != nil {
			current, err := dfe.FindingTrafficTx(tx, findingID)
			if err != nil {
				return err
			}
			if len(order) != len(current.Bindings) {
				return dfe.ErrEvidenceConflict
			}
			ids := map[int64]bool{}
			for _, b := range current.Bindings {
				ids[b.ID] = true
			}
			for position, id := range order {
				if !ids[id] {
					return dfe.ErrEvidenceConflict
				}
				delete(ids, id)
				if _, err := tx.Exec(`UPDATE finding_traffic_bindings SET position=$1 WHERE finding_id=$2 AND id=$3`, position, findingID, id); err != nil {
					return err
				}
			}
		} else {
			var res sql.Result
			var err error
			if remove {
				res, err = tx.Exec(`DELETE FROM finding_traffic_bindings WHERE finding_id=$1 AND id=$2`, findingID, bindingID)
			} else {
				res, err = tx.Exec(`UPDATE finding_traffic_bindings SET role=COALESCE($3,role),note=COALESCE($4,note) WHERE finding_id=$1 AND id=$2`, findingID, bindingID, role, note)
			}
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return dfe.ErrEvidenceNotFound
			}
		}
		return dfe.BumpEvidenceVersionTx(tx, findingID)
	})
}

type RecordFindingInput struct {
	TaskID, ExplorationID, IntentID                      int64
	VulnClass, Name, Severity, Summary, Evidence, Worker string
	AssetIDs                                             []string
}

type RecordedFinding struct {
	FindingID int64               `json:"finding_id,string"`
	NodeID    int64               `json:"finding_node_id,string"`
	Traffic   *dfe.FindingTraffic `json:"traffic"`
}

// ctx 由调用方传入本次事务所用的上下文（而非在内部取 context.Background）：
// 事务内新加的推送事件写入同样应受调用方的取消与超时约束。
//
// 留在装配层：一个事务写探索节点/锚点/边 + findings + 推送事件 + 流量绑定。
func RecordFindingTx(ctx context.Context, tx *sql.Tx, in RecordFindingInput, prepared []dfe.PreparedTrafficEvidence) (*RecordedFinding, error) {
	if err := LockTaskEvidenceTx(tx, in.TaskID); err != nil {
		return nil, err
	}
	if in.TaskID > 0 {
		var expID int64
		if err := tx.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, in.TaskID).Scan(&expID); err != nil {
			return nil, err
		}
		if expID != in.ExplorationID {
			return nil, errors.New("漏洞所属任务与探索记录不匹配")
		}
	}
	if in.IntentID > 0 {
		var ok bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM exploration_nodes WHERE id=$1 AND exploration_id=$2 AND kind='intent')`, in.IntentID, in.ExplorationID).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("intent_id 必须是本任务的意图（关联任务意图只读）")
		}
	}
	payload, _ := json.Marshal(map[string]any{"vulnclass": in.VulnClass, "name": in.Name, "severity": in.Severity, "summary": in.Summary, "evidence": map[string]any{"by": in.Worker, "poc": in.Evidence}})
	out := &RecordedFinding{}
	if err := tx.QueryRow(`INSERT INTO exploration_nodes(exploration_id,kind,payload,priority,state,origin)
VALUES($1,'finding',$2,9,'confirmed',$3) RETURNING id`, in.ExplorationID, string(payload), in.Worker).Scan(&out.NodeID); err != nil {
		return nil, err
	}
	for _, asset := range in.AssetIDs {
		if _, err := tx.Exec(`INSERT INTO exploration_anchors(node_id,asset_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, out.NodeID, asset); err != nil {
			return nil, err
		}
	}
	if in.IntentID > 0 {
		if _, err := tx.Exec(`INSERT INTO exploration_edges(exploration_id,src_id,rel,dst_id) VALUES($1,$2,$3,$4)`, in.ExplorationID, in.IntentID, exploration.RelYields, out.NodeID); err != nil {
			return nil, err
		}
	}
	assets := in.AssetIDs
	if assets == nil {
		assets = []string{}
	}
	raw, _ := json.Marshal(assets)
	if err := tx.QueryRow(`INSERT INTO findings(task_id,node_id,vulnclass,name,severity,summary,evidence,worker,asset_ids)
VALUES(NULLIF($1,0),$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, in.TaskID, out.NodeID, in.VulnClass, in.Name, in.Severity, in.Summary, in.Evidence, in.Worker, string(raw)).Scan(&out.FindingID); err != nil {
		return nil, err
	}
	// 在**同一事务**里登记一条推送事件：提交即保证「漏洞落库」与「推送任务存在」
	// 原子一致，不存在提交成功却没入队、消息永久丢失的窗口。
	// 这里的失败被隔离在保存点上、不影响漏洞写入（见函数注释），因此忽略返回值。
	dnotify.RecordNotificationEventTx(ctx, tx, notify.EventFindingCreated, out.FindingID, notify.Snapshot{
		Kind:      notify.EventFindingCreated,
		FindingID: out.FindingID,
		TaskID:    in.TaskID,
		VulnClass: in.VulnClass,
		Name:      in.Name,
		Severity:  in.Severity,
		Summary:   in.Summary,
		AssetIDs:  assets,
	})
	if err := dfe.AddFindingTrafficTx(tx, out.FindingID, prepared); err != nil {
		return nil, err
	}
	var err error
	out.Traffic, err = dfe.FindingTrafficTx(tx, out.FindingID)
	return out, err
}

// RecordFinding is the atomic legacy/no-recorder path used by tools and tests.
func (s *ExplorationStore) RecordFinding(ctx context.Context, in RecordFindingInput) (out *RecordedFinding, err error) {
	in.ExplorationID = s.expID
	err = s.db.WithEvidenceTx(ctx, func(tx *sql.Tx) error { var e error; out, e = RecordFindingTx(ctx, tx, in, nil); return e })
	return
}

// PopulateFindingTrafficIDs only enriches already-visible nodes. It performs no
// discovery or ID guessing, and leaves the legacy node ID unchanged.
// 留在装配层：探索域节点富化读 findings。
func (s *ExplorationStore) PopulateFindingTrafficIDs(nodes []*exploration.Node) error {
	byID := map[int64]*exploration.Node{}
	var args []any
	var placeholders []string
	for _, n := range nodes {
		if n == nil || n.Kind != exploration.KindFinding || byID[n.ID] != nil {
			continue
		}
		byID[n.ID] = n
		args = append(args, n.ID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	if len(args) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT f.id,f.node_id,(SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=f.id) FROM findings f WHERE f.node_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var findingID, nodeID int64
		var count int
		if err := rows.Scan(&findingID, &nodeID, &count); err != nil {
			return err
		}
		n := byID[nodeID]
		n.FindingID, n.FindingNodeID, n.TrafficCount = findingID, nodeID, count
	}
	return rows.Err()
}

func (d *DB) FindingIDByNodeID(nodeID int64) (id int64, err error) {
	return dfe.FindingIDByNodeID(d, nodeID)
}

// SetFindingReportVersionByNodeID sets the Markdown report guarded by the
// evidence-version lock. 留在装配层：取证锁协议（LockFindingEvidenceTx 链）。
func (d *DB) SetFindingReportVersionByNodeID(ctx context.Context, nodeID int64, report string, version *int64) (n int64, err error) {
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRow(`SELECT id FROM findings WHERE node_id=$1`, nodeID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := LockFindingEvidenceTx(tx, id, version); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE findings SET report=$2,report_evidence_version=COALESCE($3::bigint,CASE WHEN evidence_version=0 THEN 0 ELSE -1 END) WHERE id=$1`, id, report, version)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return
}
