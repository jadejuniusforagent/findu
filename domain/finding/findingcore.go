package finding

// findings 表（独立漏洞行，探索图之外的长生命周期漏洞记录）的存储层。
//
// 迁移边界（Step 3 批 3 第七刀）：列表/分组/统计/导出走 tasks/assets 的
// 展示 JOIN 或资产树解析（留在 db 装配层）；本文件只收单表读写。

import (
	"database/sql"
	"encoding/json"
	"time"

	"finduai/core/store"
)

// DBFinding is a row in the standalone findings table. It persists across task
// deletion unless the caller explicitly requests related finding cleanup.
type DBFinding struct {
	TrafficCount          int
	EvidenceVersion       int64
	ReportEvidenceVersion int64
	TrafficBindings       []FindingTrafficBinding // populated only for export

	ID              int64
	TaskID          *int64
	NodeID          *int64
	VulnClass       string
	Name            string // 漏洞名称(可读标题);为空时前端回退展示 VulnClass
	Severity        string
	Summary         string
	Evidence        string
	Worker          string
	Reproduction    json.RawMessage // agent 就地记录的结构化复现过程(可空);null=无(走事后富化兜底)
	AssetIDs        []string
	Status          string
	Report          string // 详细报告(Markdown);仅 GetFinding 填充,列表查询不带
	CreatedAt       time.Time
	TaskDescription string // populated via LEFT JOIN on tasks
}

// Finding triage states (findings.status).
const (
	FindingPending       = "pending"        // 待处理
	FindingInProgress    = "in_progress"    // 处理中
	FindingConfirmed     = "confirmed"      // 已确认(真实漏洞,未修复)
	FindingResolved      = "resolved"       // 已处理
	FindingFixed         = "fixed"          // 已修复
	FindingFalsePositive = "false_positive" // 误报
	FindingIgnored       = "ignored"        // 忽略
	FindingDuplicate     = "duplicate"      // 重复
	FindingRiskAccepted  = "risk_accepted"  // 风险接受
)

// ValidFindingStatus reports whether s is a known triage state.
func ValidFindingStatus(s string) bool {
	switch s {
	case FindingPending, FindingInProgress, FindingConfirmed, FindingResolved, FindingFixed,
		FindingFalsePositive, FindingIgnored, FindingDuplicate, FindingRiskAccepted:
		return true
	}
	return false
}

// Finding severity levels (findings.severity).
const (
	SeverityCritical = "critical" // 严重
	SeverityHigh     = "high"     // 高
	SeverityMedium   = "medium"   // 中
	SeverityLow      = "low"      // 低
)

// ValidSeverity reports whether s is a known severity level.
func ValidSeverity(s string) bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	}
	return false
}

// AddFinding inserts a finding into the standalone findings table. taskID and
// nodeID may be 0 (stored as NULL). name may be "" (frontend falls back to
// vulnclass). Returns the new finding id.
func AddFinding(q store.Querier, taskID, nodeID int64, vulnclass, name, severity, summary, evidence, worker string, assetIDs []string) (int64, error) {
	aidsJSON, _ := json.Marshal(assetIDs)
	if assetIDs == nil {
		aidsJSON = []byte("[]")
	}
	var tid, nid *int64
	if taskID > 0 {
		tid = &taskID
	}
	if nodeID > 0 {
		nid = &nodeID
	}
	var id int64
	err := q.QueryRow(
		`INSERT INTO findings (task_id, node_id, vulnclass, name, severity, summary, evidence, worker, asset_ids)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		tid, nid, vulnclass, name, severity, summary, evidence, worker, string(aidsJSON),
	).Scan(&id)
	return id, err
}

// DeleteFindingsByTask removes all findings rows of a task. The originating
// exploration finding nodes are cascade-deleted separately when the task's
// exploration subgraph is dropped. Returns rows deleted.
func DeleteFindingsByTask(q store.Querier, taskID int64) (int64, error) {
	res, err := q.Exec(`DELETE FROM findings WHERE task_id=$1`, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetFindingStatus updates one finding's triage state. Returns rows affected.
//
// 底层 setter：只改状态、不登记推送事件。生产代码改状态请走
// SetFindingStatusWithNotify —— 直接调本函数会让「状态变更推送」静默失效。
// 保留它是为了让不关心通知的用例（参数校验、复测流程）能单独驱动状态。
func SetFindingStatus(q store.Querier, id int64, status string) (int64, error) {
	res, err := q.Exec(`UPDATE findings SET status=$1 WHERE id=$2`, status, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetFindingReproduction stores the agent-captured structured reproduction recipe
// for the finding at nodeID (report_finding writes it inline). No-op on empty.
func SetFindingReproduction(q store.Querier, nodeID int64, repro json.RawMessage) error {
	if len(repro) == 0 {
		return nil
	}
	_, err := q.Exec(`UPDATE findings SET reproduction=$1 WHERE node_id=$2`, string(repro), nodeID)
	return err
}

// FindingMeta is the standalone-row data (id, triage state, anchored assets) the
// per-task view grafts onto its exploration-node findings.
type FindingMeta struct {
	TrafficCount int

	ID       int64
	Status   string
	AssetIDs []string
}

// FindingMetaByNodeID maps a task's finding node ids to their standalone-row
// metadata, so the per-task view (which reads exploration nodes) can show and
// edit the same status — and the same anchored assets — as the global 发现 page.
func FindingMetaByNodeID(q store.Querier, taskID int64) (map[int64]FindingMeta, error) {
	out := map[int64]FindingMeta{}
	if taskID <= 0 {
		return out, nil
	}
	rows, err := q.Query(`SELECT node_id, id, COALESCE(status,'pending'), asset_ids, (SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=findings.id) FROM findings
		WHERE task_id=$1 AND node_id IS NOT NULL`, taskID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var nid int64
		var m FindingMeta
		var aidsJSON string
		if err := rows.Scan(&nid, &m.ID, &m.Status, &aidsJSON, &m.TrafficCount); err != nil {
			return out, err
		}
		_ = json.Unmarshal([]byte(aidsJSON), &m.AssetIDs)
		out[nid] = m
	}
	return out, rows.Err()
}

// GetFindingByNodeID 按 exploration node id 取漏洞(报告富化/详情用;
// findings.node_id 与节点一一对应)。
func GetFindingByNodeID(q store.Querier, nodeID int64) (*DBFinding, error) {
	var f DBFinding
	var taskID, nid sql.NullInt64
	var repro []byte
	err := q.QueryRow(`SELECT id, task_id, node_id, vulnclass, name, severity, summary,
	       evidence, report, worker, status, created_at, reproduction
	FROM findings WHERE node_id = $1 LIMIT 1`, nodeID,
	).Scan(&f.ID, &taskID, &nid, &f.VulnClass, &f.Name, &f.Severity, &f.Summary,
		&f.Evidence, &f.Report, &f.Worker, &f.Status, &f.CreatedAt, &repro)
	if repro != nil {
		f.Reproduction = json.RawMessage(repro)
	}
	if err != nil {
		return nil, err
	}
	if taskID.Valid {
		t := taskID.Int64
		f.TaskID = &t
	}
	if nid.Valid {
		n := nid.Int64
		f.NodeID = &n
	}
	return &f, nil
}

// FindingIDByNodeID returns the standalone finding row id for a node id.
func FindingIDByNodeID(q store.Querier, nodeID int64) (id int64, err error) {
	err = q.QueryRow(`SELECT id FROM findings WHERE node_id=$1`, nodeID).Scan(&id)
	return
}
