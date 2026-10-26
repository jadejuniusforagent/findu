package db

// 漏洞域存储层已迁到 domain/finding（Step 3 批 3 第七刀）。本文件保留：
//  1. 类型/常量别名 —— 调用点（server/agent/evidence）零改动；
//  2. 转发 —— findings 单表读写委托 domain/finding；
//  3. 跨域函数留在装配层 —— 列表/分组/统计/导出（JOIN tasks 展示 + 资产树
//     解析读 assets/companies）、DeleteFinding（取证锁×探索节点）、
//     setFindingCol 家族（findings×exploration_nodes 双写）、
//     AddFindingFollowUpIntent（探索域存储）。
// 另：finding_assets.go（按资产树）、finding_retests.go 的两个跨域函数、
// recordFindingTx 证据管线跨域函数见各自文件。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"finduai/core/model"
	"finduai/core/store"
	"finduai/domain/exploration"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	dfe "finduai/domain/finding"
)

// ---- 漏洞状态 / 严重度常量（原名保持，供 server/agent 引用）----

// SetFindingReportByNodeID sets the Markdown report on the standalone finding row
// whose node_id matches — report_finding returns that node id, so an agent tool
// can address the finding it just created. Returns rows affected (0 when no row).
func (d *DB) SetFindingReportByNodeID(nodeID int64, report string) (int64, error) {
	return d.SetFindingReportVersionByNodeID(context.Background(), nodeID, report, nil)
}

// ---- findings 单表 func 转发 ----

func (d *DB) AddFinding(taskID, nodeID int64, vulnclass, name, severity, summary, evidence, worker string, assetIDs []string) (int64, error) {
	return dfe.AddFinding(d, taskID, nodeID, vulnclass, name, severity, summary, evidence, worker, assetIDs)
}

func (d *DB) SetFindingStatus(id int64, status string) (int64, error) {
	return dfe.SetFindingStatus(d, id, status)
}

func (d *DB) SetFindingReproduction(nodeID int64, repro json.RawMessage) error {
	return dfe.SetFindingReproduction(d, nodeID, repro)
}

func (d *DB) GetFindingByNodeID(nodeID int64) (*dfe.DBFinding, error) {
	return dfe.GetFindingByNodeID(d, nodeID)
}

func (d *DB) FindingMetaByNodeID(taskID int64) (map[int64]dfe.FindingMeta, error) {
	return dfe.FindingMetaByNodeID(d, taskID)
}

// FindingMetaByNodeID maps a task's finding node ids to their standalone-row
// metadata via the asset store, so callers holding only an AssetStore (e.g. the
// agent ToolSet) can reach it without a raw *DB.
func (a *AssetStore) FindingMetaByNodeID(taskID int64) (map[int64]dfe.FindingMeta, error) {
	return a.db.FindingMetaByNodeID(taskID)
}

// ---- 以下均为跨域函数，留在装配层（见文件头第 3 点）----

func (d *DB) DeleteFindingsByTask(taskID int64) (int64, error) {
	return dfe.DeleteFindingsByTask(d, taskID)
}

// findingSelectCols is the column list (with task_description join) every finding
// list query selects, so scanFinding stays in sync across callers.
const findingSelectCols = `f.id, f.task_id, f.node_id, f.vulnclass, COALESCE(f.name, ''), f.severity, f.summary,
	       f.evidence, f.worker, f.asset_ids, COALESCE(f.status, 'pending'), f.created_at,
	       COALESCE(t.description, '') AS task_description, f.evidence_version, f.report_evidence_version,
 (SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=f.id)`

// scanFindings materializes rows selected via findingSelectCols.
func scanFindings(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*dfe.DBFinding, error) {
	var out []*dfe.DBFinding
	for rows.Next() {
		f := &dfe.DBFinding{}
		var aidsJSON string
		if err := rows.Scan(&f.ID, &f.TaskID, &f.NodeID, &f.VulnClass, &f.Name, &f.Severity,
			&f.Summary, &f.Evidence, &f.Worker, &aidsJSON, &f.Status, &f.CreatedAt, &f.TaskDescription, &f.EvidenceVersion, &f.ReportEvidenceVersion, &f.TrafficCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(aidsJSON), &f.AssetIDs)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListFindings returns all findings (newest first), joined with task description.
// Kept for the dashboard's summary; the paginated 发现 page uses ListFindingsPage.
func (d *DB) ListFindings(limit int) ([]*dfe.DBFinding, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := d.Query(`
		SELECT `+findingSelectCols+`
		FROM findings f
		LEFT JOIN tasks t ON f.task_id = t.id
		ORDER BY f.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

// FindingFilter narrows a paginated findings query. Empty-string fields mean "no
// filter on that column". Sort is "severity" (severity desc, then newest) or
// anything else (newest first).
type FindingFilter struct {
	Severity  string // high | medium | low
	Status    string // pending | false_positive | ignored | resolved
	VulnClass string
	TaskID    string // 任务 id(字符串形式;空/非法 = 不按任务筛选)
	Query     string // 名称/类型/摘要/证据/报告正文的模糊检索关键词
	Sort      string // "severity" | "time"
	// AssetScope 是资产树的节点 key(a:<id> / c:<id> / r:<domain> / __none__),
	// 选中一个节点等于选中它的整棵子树。空 = 不按资产筛选。
	AssetScope string

	// 下面三个由 applyAssetScope 从 AssetScope 解析而来,调用方不用设置。
	assetIDs  []string // 子树里所有资产 id
	assetNone bool     // 只要「未关联资产」的发现
	assetMiss bool     // 选中的节点在当前筛选下不存在 → 结果恒空
}

// FindingUnassignedTask is the task filter sentinel for findings whose task is
// absent. That includes rows created without a task and rows retained after their
// originating task was deleted (the findings FK is ON DELETE SET NULL).
const FindingUnassignedTask = "__unassigned__"

// where builds the WHERE clause (shared by the page and count queries) plus its
// positional args. All values are parameterized; Query also escapes ILIKE
// wildcards so user input is always matched literally.
func (f FindingFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(col, val string) {
		if val == "" {
			return
		}
		args = append(args, val)
		conds = append(conds, fmt.Sprintf("f.%s = $%d", col, len(args)))
	}
	add("severity", f.Severity)
	add("status", f.Status)
	add("vulnclass", f.VulnClass)
	// task_id 是 bigint 列,按整数比较(不能走上面的文本 add);空/非法值忽略。
	if f.TaskID == FindingUnassignedTask {
		conds = append(conds, "(f.task_id IS NULL OR t.id IS NULL)")
	} else if tid, err := strconv.ParseInt(f.TaskID, 10, 64); err == nil && tid > 0 {
		args = append(args, tid)
		conds = append(conds, fmt.Sprintf("f.task_id = $%d", len(args)))
	}
	// 资产筛选:asset_ids 是 jsonb 数组,@> ANY(...) 能走 idx_findings_asset_ids。
	switch {
	case f.assetMiss:
		conds = append(conds, "FALSE")
	case f.assetNone:
		// 「未关联资产」= asset_ids 为空,或者里面的 id 一个都不在 assets 表里
		// (资产已被删除)。两类都进资产树的未关联桶,这里必须同样收下,否则桶上
		// 的计数会大于点开后能查到的条数。
		conds = append(conds, `(
			jsonb_array_length(COALESCE(f.asset_ids, '[]'::jsonb)) = 0
			OR NOT EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(f.asset_ids) e(v)
				JOIN assets a ON a.id = e.v
			)
		)`)
	case len(f.assetIDs) > 0:
		args = append(args, assetIDContainments(f.assetIDs))
		conds = append(conds, fmt.Sprintf("f.asset_ids @> ANY($%d::jsonb[])", len(args)))
	}
	if query := strings.TrimSpace(f.Query); query != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
		args = append(args, "%"+escaped+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		conds = append(conds, fmt.Sprintf(`(
			COALESCE(f.name, '') ILIKE %s ESCAPE '\' OR
			f.vulnclass ILIKE %s ESCAPE '\' OR
			f.summary ILIKE %s ESCAPE '\' OR
			f.evidence ILIKE %s ESCAPE '\' OR
			COALESCE(f.report, '') ILIKE %s ESCAPE '\'
		)`, placeholder, placeholder, placeholder, placeholder, placeholder))
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListFindingsPage returns one page of findings matching the filter, plus the
// total count of matching rows (for the frontend pager). page is 1-based.
func (d *DB) ListFindingsPage(f FindingFilter, page, pageSize int) ([]*dfe.DBFinding, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	f, err := d.applyAssetScope(f)
	if err != nil {
		return nil, 0, err
	}
	where, args := f.where()

	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM findings f LEFT JOIN tasks t ON f.task_id=t.id`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	// Avoid overflowing (page-1)*pageSize for an arbitrarily large page number.
	// Once the requested page is beyond the exact count, no data query is needed.
	if total == 0 || page > (total-1)/pageSize+1 {
		return []*dfe.DBFinding{}, total, nil
	}

	order := "f.created_at DESC, f.id DESC"
	if f.Sort == "severity" {
		// critical > high > medium > low > 其它, then newest first.
		order = `CASE f.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END DESC, f.created_at DESC, f.id DESC`
	}
	pageArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	q := fmt.Sprintf(`
		SELECT %s
		FROM findings f
		LEFT JOIN tasks t ON f.task_id = t.id%s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`, findingSelectCols, where, order, len(args)+1, len(args)+2)
	rows, err := d.Query(q, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanFindings(rows)
	return out, total, err
}

// FindingGroup is one task-level bucket in the global findings view. TaskID is
// nil for both findings that never had a task and findings retained after task
// deletion; those records intentionally share one "unassigned/deleted" bucket.
type FindingGroup struct {
	TaskID          *int64    `json:"task_id"`
	TaskName        string    `json:"task_name"` // 可选任务名称;空=未命名
	TaskDescription string    `json:"task_description"`
	TaskStatus      string    `json:"task_status"`
	Count           int       `json:"count"`
	Critical        int       `json:"critical"`
	High            int       `json:"high"`
	Medium          int       `json:"medium"`
	Low             int       `json:"low"`
	LastFoundAt     time.Time `json:"last_found_at"`
}

// ListFindingGroups returns a page of task groups matching the same filters as
// ListFindingsPage. The group count and finding count are independent totals so
// clients can page groups without losing the exact export/selection count.
func (d *DB) ListFindingGroups(f FindingFilter, page, pageSize int) ([]FindingGroup, int, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	f, err := d.applyAssetScope(f)
	if err != nil {
		return nil, 0, 0, err
	}
	where, args := f.where()
	grouped := ` FROM findings f LEFT JOIN tasks t ON f.task_id=t.id` + where +
		` GROUP BY t.id, t.name, t.description, t.status, t.paused, t.queued`

	var groupTotal, findingTotal int
	countQuery := `SELECT COUNT(*), COALESCE(SUM(finding_count),0) FROM (` +
		`SELECT COUNT(*) AS finding_count` + grouped + `) grouped_findings`
	if err := d.QueryRow(countQuery, args...).Scan(&groupTotal, &findingTotal); err != nil {
		return nil, 0, 0, err
	}
	if groupTotal == 0 || page > (groupTotal-1)/pageSize+1 {
		return []FindingGroup{}, groupTotal, findingTotal, nil
	}

	order := "MAX(f.created_at) DESC, t.id DESC NULLS LAST"
	if f.Sort == "severity" {
		order = `MAX(CASE f.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END) DESC, MAX(f.created_at) DESC, t.id DESC NULLS LAST`
	}
	pageArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	query := fmt.Sprintf(`SELECT t.id, COALESCE(t.name,''), COALESCE(t.description,''), COALESCE(
		CASE
			WHEN t.status IN ('done','failed','timeout') THEN t.status
			WHEN t.queued THEN 'queued'
			WHEN t.paused THEN 'paused'
			ELSE t.status
		END, ''),
		COUNT(*),
		COUNT(*) FILTER (WHERE f.severity='critical'),
		COUNT(*) FILTER (WHERE f.severity='high'),
		COUNT(*) FILTER (WHERE f.severity='medium'),
		COUNT(*) FILTER (WHERE f.severity='low'),
		MAX(f.created_at)%s
		ORDER BY %s LIMIT $%d OFFSET $%d`, grouped, order, len(args)+1, len(args)+2)
	rows, err := d.Query(query, pageArgs...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	groups := []FindingGroup{}
	for rows.Next() {
		var group FindingGroup
		var taskID sql.NullInt64
		if err := rows.Scan(&taskID, &group.TaskName, &group.TaskDescription, &group.TaskStatus, &group.Count,
			&group.Critical, &group.High, &group.Medium, &group.Low, &group.LastFoundAt); err != nil {
			return nil, 0, 0, err
		}
		if taskID.Valid {
			id := taskID.Int64
			group.TaskID = &id
		}
		groups = append(groups, group)
	}
	return groups, groupTotal, findingTotal, rows.Err()
}

// ErrFindingOriginUnavailable means a retained finding no longer has a live
// owning task and finding node from which a follow-up intent can be derived.
var ErrFindingOriginUnavailable = errors.New("finding origin is no longer available")

// AddFindingFollowUpIntent atomically creates a priority-10 human intent from a
// live finding node, copies that finding's asset anchors, records the
// finding --derived_from--> intent lineage edge, and persists its audit activity.
// The returned activity is the committed row and can be broadcast as-is without
// calling AppendActivity again.
//
// 留在装配层：一个事务写探索节点/锚点/边/活动 + 锁 findings 行，属探索域×漏洞域。
func (s *ExplorationStore) AddFindingFollowUpIntent(findingID, findingNodeID int64, description string, audit model.Activity) (int64, model.Activity, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, model.Activity{}, err
	}
	defer tx.Rollback()

	var liveNodeID int64
	err = tx.QueryRow(`SELECT n.id
		FROM findings f
		JOIN tasks t ON t.id=f.task_id
		JOIN exploration_nodes n ON n.id=f.node_id AND n.exploration_id=t.exploration_id
		WHERE f.id=$1 AND f.node_id=$2 AND t.exploration_id=$3 AND n.kind='finding'
		FOR SHARE OF f, t, n`, findingID, findingNodeID, s.expID).Scan(&liveNodeID)
	if err == sql.ErrNoRows {
		return 0, model.Activity{}, ErrFindingOriginUnavailable
	}
	if err != nil {
		return 0, model.Activity{}, err
	}

	anchors := []string{}
	anchorRows, err := tx.Query(`SELECT asset_id FROM exploration_anchors WHERE node_id=$1 ORDER BY asset_id`, liveNodeID)
	if err != nil {
		return 0, model.Activity{}, err
	}
	for anchorRows.Next() {
		var assetID string
		if err := anchorRows.Scan(&assetID); err != nil {
			anchorRows.Close()
			return 0, model.Activity{}, err
		}
		anchors = append(anchors, assetID)
	}
	if err := anchorRows.Err(); err != nil {
		anchorRows.Close()
		return 0, model.Activity{}, err
	}
	if err := anchorRows.Close(); err != nil {
		return 0, model.Activity{}, err
	}

	payload := map[string]any{
		"summary":                description,
		"source_finding_id":      findingID,
		"source_finding_node_id": liveNodeID,
	}
	if len(anchors) > 0 {
		payload["asset_ids"] = anchors
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, model.Activity{}, err
	}
	var intentID int64
	if err := tx.QueryRow(`INSERT INTO exploration_nodes(exploration_id,kind,payload,priority,state,origin)
		VALUES ($1,'intent',$2,10,'open','human') RETURNING id`, s.expID, raw).Scan(&intentID); err != nil {
		return 0, model.Activity{}, err
	}
	if _, err := tx.Exec(`INSERT INTO exploration_anchors(node_id,asset_id)
		SELECT $1, asset_id FROM exploration_anchors WHERE node_id=$2
		ON CONFLICT DO NOTHING`, intentID, liveNodeID); err != nil {
		return 0, model.Activity{}, err
	}
	if _, err := tx.Exec(`INSERT INTO exploration_edges(exploration_id,src_id,rel,dst_id)
		VALUES ($1,$2,$3,$4)`, s.expID, liveNodeID, exploration.RelDerivedFrom, intentID); err != nil {
		return 0, model.Activity{}, err
	}

	audit.NodeID = &intentID
	if summary := strings.TrimSpace(audit.Summary); summary != "" {
		audit.Summary = fmt.Sprintf("%s #%d", summary, intentID)
	} else {
		audit.Summary = ""
	}
	metadata := audit.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if err := tx.QueryRow(`
INSERT INTO activity(exploration_id, node_id, worker, kind, tool, tool_use_id, is_error, summary, detail, metadata, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14)
RETURNING id, created_at`, s.expID, audit.NodeID, store.UTF8Clean(audit.Worker), store.UTF8Clean(audit.Kind), store.UTF8Clean(audit.Tool), store.UTF8Clean(audit.ToolUseID), audit.IsError,
		store.UTF8Clean(audit.Summary), store.UTF8Clean(audit.Detail), metadata, audit.InputTokens, audit.OutputTokens, audit.CacheReadTokens, audit.CacheWriteTokens).
		Scan(&audit.ID, &audit.CreatedAt); err != nil {
		return 0, model.Activity{}, err
	}
	audit.Metadata = metadata
	if err := tx.Commit(); err != nil {
		return 0, model.Activity{}, err
	}
	return intentID, audit, nil
}

// ListFindingsForExport returns findings for the 发现 page 导出功能，携带完整
// report 字段、不分页。ids 非空时按这批 finding id 精确导出(勾选导出),忽略
// filter;ids 为空时按 filter 导出(导出当前筛选/全部)。结果按严重等级降序、
// 再按时间倒序,与「导出汇总报告」的分组顺序一致。
func (d *DB) ListFindingsForExport(f FindingFilter, ids []int64) ([]*dfe.DBFinding, error) {
	const order = `ORDER BY CASE f.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END DESC, f.created_at DESC`
	cols := findingSelectCols + `, COALESCE(f.report, '')`

	var q string
	var args []any
	if len(ids) > 0 {
		ph := make([]string, len(ids))
		for i, id := range ids {
			ph[i] = fmt.Sprintf("$%d", i+1)
			args = append(args, id)
		}
		q = `SELECT ` + cols + `
			FROM findings f
			LEFT JOIN tasks t ON f.task_id = t.id
			WHERE f.id IN (` + strings.Join(ph, ",") + `)
			` + order
	} else {
		scoped, err := d.applyAssetScope(f)
		if err != nil {
			return nil, err
		}
		where, wargs := scoped.where()
		q = `SELECT ` + cols + `
			FROM findings f
			LEFT JOIN tasks t ON f.task_id = t.id` + where + `
			` + order
		args = wargs
	}

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*dfe.DBFinding
	for rows.Next() {
		f := &dfe.DBFinding{}
		var aidsJSON string
		if err := rows.Scan(&f.ID, &f.TaskID, &f.NodeID, &f.VulnClass, &f.Name, &f.Severity,
			&f.Summary, &f.Evidence, &f.Worker, &aidsJSON, &f.Status, &f.CreatedAt,
			&f.TaskDescription, &f.EvidenceVersion, &f.ReportEvidenceVersion, &f.TrafficCount, &f.Report); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(aidsJSON), &f.AssetIDs)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FindingStats is the whole-table aggregate powering the 发现 page's stat cards
// and vuln-class filter — computed server-side so it stays exact regardless of
// pagination.
type FindingStats struct {
	Total       int                 `json:"total"`
	Pending     int                 `json:"pending"`
	Critical    int                 `json:"critical"`
	High        int                 `json:"high"`
	Medium      int                 `json:"medium"`
	Low         int                 `json:"low"`
	VulnClasses []string            `json:"vulnclasses"`
	Tasks       []FindingTaskOption `json:"tasks"` // 有漏洞的任务(供「按任务」下拉)
}

// FindingTaskOption is one entry in the 发现 page's 任务 filter: a task that has at
// least one finding, with its description and finding count. Description is empty when
// the task has since been deleted (finding rows persist), so the frontend falls back to
// the id.
type FindingTaskOption struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"` // 可选任务名称;空=未命名
	Description string `json:"description"`
	Count       int    `json:"count"`
}

// FindingStats returns whole-table counts (by severity + pending) and the sorted
// set of distinct vuln classes.
//
// 留在装配层：读 tasks 展示下拉、叠加 task_archives 冷数据聚合（任务域）。
func (d *DB) FindingStats() (*FindingStats, error) {
	st := &FindingStats{VulnClasses: []string{}, Tasks: []FindingTaskOption{}}
	err := d.QueryRow(`SELECT
		COUNT(*),
		COUNT(*) FILTER (WHERE status = 'pending'),
		COUNT(*) FILTER (WHERE severity = 'critical'),
		COUNT(*) FILTER (WHERE severity = 'high'),
		COUNT(*) FILTER (WHERE severity = 'medium'),
		COUNT(*) FILTER (WHERE severity = 'low')
		FROM findings`).Scan(&st.Total, &st.Pending, &st.Critical, &st.High, &st.Medium, &st.Low)
	if err != nil {
		return nil, err
	}
	rows, err := d.Query(`SELECT DISTINCT vulnclass FROM findings WHERE vulnclass <> '' ORDER BY vulnclass`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var vc string
		if err := rows.Scan(&vc); err != nil {
			return nil, err
		}
		st.VulnClasses = append(st.VulnClasses, vc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 任务下拉:有漏洞的任务,带描述(任务删除后为空,前端回退 id)和条数,最新有漏洞的排前。
	trows, err := d.Query(`SELECT f.task_id, COALESCE(t.name, ''), COALESCE(t.description, ''), COUNT(*)
		FROM findings f
		LEFT JOIN tasks t ON f.task_id = t.id
		WHERE f.task_id IS NOT NULL
		GROUP BY f.task_id, t.name, t.description
		ORDER BY MAX(f.created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var opt FindingTaskOption
		if err := trows.Scan(&opt.ID, &opt.Name, &opt.Description, &opt.Count); err != nil {
			return nil, err
		}
		st.Tasks = append(st.Tasks, opt)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	archived, err := d.archivedTaskAggregates()
	if err != nil {
		return nil, err
	}
	vulnclasses := make(map[string]bool, len(st.VulnClasses))
	for _, vulnclass := range st.VulnClasses {
		vulnclasses[vulnclass] = true
	}
	for _, aggregate := range archived {
		cold := aggregate.FindingStats
		st.Total += cold.Total
		st.Pending += cold.Pending
		st.Critical += cold.Critical
		st.High += cold.High
		st.Medium += cold.Medium
		st.Low += cold.Low
		for _, vulnclass := range cold.VulnClasses {
			if vulnclass != "" {
				vulnclasses[vulnclass] = true
			}
		}
	}
	st.VulnClasses = st.VulnClasses[:0]
	for vulnclass := range vulnclasses {
		st.VulnClasses = append(st.VulnClasses, vulnclass)
	}
	sort.Strings(st.VulnClasses)
	return st, nil
}

// GetFinding returns a single finding row (with task_description joined and the
// full Markdown report), or nil when no row has that id. Unlike the list queries
// it also selects `report` — that column is only needed on the detail page.
//
// 留在装配层：LEFT JOIN tasks 取任务描述（展示跨域），并与列表共用列清单。
func (d *DB) GetFinding(id int64) (*dfe.DBFinding, error) {
	f := &dfe.DBFinding{}
	var aidsJSON string
	err := d.QueryRow(`SELECT `+findingSelectCols+`, COALESCE(f.report, '')
		FROM findings f
		LEFT JOIN tasks t ON f.task_id = t.id
		WHERE f.id = $1`, id).Scan(
		&f.ID, &f.TaskID, &f.NodeID, &f.VulnClass, &f.Name, &f.Severity,
		&f.Summary, &f.Evidence, &f.Worker, &aidsJSON, &f.Status, &f.CreatedAt,
		&f.TaskDescription, &f.EvidenceVersion, &f.ReportEvidenceVersion, &f.TrafficCount, &f.Report)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(aidsJSON), &f.AssetIDs)
	return f, nil
}

// DeleteFinding removes a finding entirely: the standalone findings row and its
// originating exploration node (kind='finding'), so it disappears from the findings
// list, the per-task 发现 Tab, and the exploration graph alike. Deleting the node
// cascades its edges + node_assets and nulls any activity referencing it. Returns
// rows affected (0 = no finding with that id).
//
// 留在装配层：取证锁协议（advisory 锁×task_archives 状态机）+ 探索节点级联。
func (d *DB) DeleteFinding(id int64) (n int64, err error) {
	err = d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
		if err := LockFindingEvidenceTx(tx, id, nil); err != nil {
			if errors.Is(err, dfe.ErrFindingNotFound) {
				return nil
			}
			return err
		}
		var nodeID sql.NullInt64
		if err := tx.QueryRow(`DELETE FROM findings WHERE id=$1 RETURNING node_id`, id).Scan(&nodeID); err != nil {
			return err
		}
		if nodeID.Valid {
			if _, err := tx.Exec(`DELETE FROM exploration_nodes WHERE id=$1 AND kind='finding'`, nodeID.Int64); err != nil {
				return err
			}
		}
		n = 1
		return nil
	})
	return
}

// setFindingCol updates one text column on the standalone finding row AND mirrors
// the new value into the originating exploration node's payload under jsonKey, so the
// per-task 发现 Tab (which reads the node payload, not this table) stays in sync.
// Returns rows affected (0 when no finding has that id); the node sync is best-effort.
// col and jsonKey MUST be trusted constants (they are interpolated into SQL) — never
// pass user input.
//
// 留在装配层：本域列更新 × 探索节点 payload 双写。
func (d *DB) setFindingCol(id int64, col, jsonKey, val string) (int64, error) {
	var nodeID *int64
	err := d.QueryRow(`UPDATE findings SET `+col+`=$1 WHERE id=$2 RETURNING node_id`, val, id).Scan(&nodeID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if nodeID != nil {
		_, _ = d.Exec(`UPDATE exploration_nodes
			SET payload = jsonb_set(payload, '{`+jsonKey+`}', to_jsonb($1::text))
			WHERE id = $2`, val, *nodeID)
	}
	return 1, nil
}

// SetFindingSeverity updates one finding's severity (+ node payload sync). Returns
// rows affected (0 when no finding has that id).
func (d *DB) SetFindingSeverity(id int64, severity string) (int64, error) {
	return d.setFindingCol(id, "severity", "severity", severity)
}

// SetFindingName updates one finding's 漏洞名称 (+ node payload sync). Empty name is
// allowed — the frontend falls back to the vuln class for display.
func (d *DB) SetFindingName(id int64, name string) (int64, error) {
	return d.setFindingCol(id, "name", "name", name)
}

// SetFindingVulnClass updates one finding's 漏洞类别 (+ node payload sync).
func (d *DB) SetFindingVulnClass(id int64, vulnclass string) (int64, error) {
	return d.setFindingCol(id, "vulnclass", "vulnclass", vulnclass)
}
