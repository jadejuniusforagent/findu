package db

// Step 6d Phase B5：workflow_definitions / workflow_runs / workflow_node_runs
// 的原生读投影与 CRUD 层（共享 PG，与 findu database 包同 schema 同语义）。
//
// 行为对齐 findu internal/database/workflow.go：
//   - List 默认 WHERE enabled=1，includeDisabled=true 时不带过滤；ORDER BY
//     updated_at DESC；空列表保持 nil（JSON "workflows":null，与 findu 同）；
//   - Upsert 版本语义：新建取 max(1, version)；已存在取 max(existing+1, version)；
//   - Delete 直接 DELETE definitions（runs 无 FK 挂 definitions，不级联）；
//   - Run/NodeRun/awaiting_hitl 投影列序与 findu 完全一致，NULL→空串，
//     finished_at NULL→nil 指针。
//
// 图编译校验/缓存失效（workflowrunner.ValidateGraphJSON /
// InvalidateCompiledCache）与包导出（workflowpkg.Export）为 finduai/internal/
// workflow 包级纯函数/单例函数，宿主与 findu 同进程，由 server 层直接 import
// 复用（引擎独立于 findu app 组成，findu 移除后随工作流功能归宿主所有，见交接表）。
//
// RBAC：workflowRunAllowed / workflowConversationAllowed 的会话可见性过滤同
// B1/B7 记录的既有缺口（宿主 Bearer 等价 scope=all 管理员）。

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// WorkflowStore 对共享 PG 的 workflow 三表操作。
type WorkflowStore struct{ db *DB }

// Workflows returns the workflow store.
func (d *DB) Workflows() *WorkflowStore { return &WorkflowStore{db: d} }

// WorkflowDefinition 镜像 findu database.WorkflowDefinition（snake_case JSON）。
type WorkflowDefinition struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Version     int       `json:"version"`
	GraphJSON   string    `json:"graph_json"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkflowRun 镜像 findu database.WorkflowRun。
type WorkflowRun struct {
	ID                string     `json:"id"`
	WorkflowID        string     `json:"workflow_id"`
	WorkflowVersion   int        `json:"workflow_version"`
	ConversationID    string     `json:"conversation_id,omitempty"`
	ProjectID         string     `json:"project_id,omitempty"`
	RoleID            string     `json:"role_id,omitempty"`
	Status            string     `json:"status"`
	InputJSON         string     `json:"input_json,omitempty"`
	OutputJSON        string     `json:"output_json,omitempty"`
	Error             string     `json:"error,omitempty"`
	PendingHITLNodeID string     `json:"pending_hitl_node_id,omitempty"`
	PendingHITLJSON   string     `json:"pending_hitl_json,omitempty"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

// WorkflowNodeRun 镜像 findu database.WorkflowNodeRun。
type WorkflowNodeRun struct {
	ID         string     `json:"id"`
	RunID      string     `json:"run_id"`
	NodeID     string     `json:"node_id"`
	Status     string     `json:"status"`
	InputJSON  string     `json:"input_json,omitempty"`
	OutputJSON string     `json:"output_json,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// workflowRunColumns 列序镜像 findu workflowRunColumns。
const workflowRunColumns = `id, workflow_id, workflow_version, conversation_id, project_id, role_id, status, input_json, output_json, error, pending_hitl_node_id, pending_hitl_json, started_at, finished_at`

// WorkflowDefinitions 镜像 ListWorkflowDefinitions（空列表保持 nil）。
func (s *WorkflowStore) WorkflowDefinitions(ctx context.Context, includeDisabled bool) ([]*WorkflowDefinition, error) {
	query := `SELECT id, name, description, version, graph_json, enabled, created_at, updated_at FROM workflow_definitions`
	if !includeDisabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询工作流列表失败: %w", err)
	}
	defer rows.Close()
	var out []*WorkflowDefinition
	for rows.Next() {
		wf, err := scanWorkflowDefinition(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描工作流失败: %w", err)
		}
		out = append(out, wf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("扫描工作流失败: %w", err)
	}
	return out, nil
}

// WorkflowDefinitionByID 镜像 GetWorkflowDefinition（缺失返回 nil,nil → handler 404）。
func (s *WorkflowStore) WorkflowDefinitionByID(ctx context.Context, id string) (*WorkflowDefinition, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, version, graph_json, enabled, created_at, updated_at FROM workflow_definitions WHERE id = $1`, id)
	wf, err := scanWorkflowDefinition(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询工作流失败: %w", err)
	}
	return wf, nil
}

// WorkflowDefinitionUpsert 镜像 UpsertWorkflowDefinition（含版本推进语义）。
func (s *WorkflowStore) WorkflowDefinitionUpsert(ctx context.Context, wf *WorkflowDefinition) error {
	if wf == nil {
		return fmt.Errorf("工作流为空")
	}
	wf.ID = strings.TrimSpace(wf.ID)
	wf.Name = strings.TrimSpace(wf.Name)
	if wf.ID == "" || wf.Name == "" {
		return fmt.Errorf("工作流 id 和 name 不能为空")
	}
	if strings.TrimSpace(wf.GraphJSON) == "" {
		wf.GraphJSON = `{"nodes":[],"edges":[],"config":{}}`
	}
	if wf.Version <= 0 {
		wf.Version = 1
	}
	now := time.Now()
	existing, err := s.WorkflowDefinitionByID(ctx, wf.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO workflow_definitions (id, name, description, version, graph_json, enabled, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			wf.ID, wf.Name, wf.Description, wf.Version, wf.GraphJSON, boolToInt(wf.Enabled), now, now,
		)
	} else {
		nextVersion := existing.Version + 1
		if wf.Version > existing.Version {
			nextVersion = wf.Version
		}
		_, err = s.db.ExecContext(ctx,
			`UPDATE workflow_definitions
			 SET name = $1, description = $2, version = $3, graph_json = $4, enabled = $5, updated_at = $6
			 WHERE id = $7`,
			wf.Name, wf.Description, nextVersion, wf.GraphJSON, boolToInt(wf.Enabled), now, wf.ID,
		)
	}
	if err != nil {
		return fmt.Errorf("保存工作流失败: %w", err)
	}
	return nil
}

// WorkflowDefinitionDelete 镜像 DeleteWorkflowDefinition（不级联 runs）。
func (s *WorkflowStore) WorkflowDefinitionDelete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("工作流 id 不能为空")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM workflow_definitions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("删除工作流失败: %w", err)
	}
	return nil
}

// WorkflowRunByID 镜像 GetWorkflowRun（缺失返回 nil,nil → handler 404）。
func (s *WorkflowStore) WorkflowRunByID(ctx context.Context, runID string) (*WorkflowRun, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id = $1`, runID)
	run, err := scanWorkflowRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询工作流运行失败: %w", err)
	}
	return run, nil
}

// WorkflowNodeRuns 镜像 ListWorkflowNodeRuns（started_at ASC）。
func (s *WorkflowStore) WorkflowNodeRuns(ctx context.Context, runID string) ([]*WorkflowNodeRun, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("工作流运行 id 不能为空")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, node_id, status, input_json, output_json, error, started_at, finished_at
		 FROM workflow_node_runs WHERE run_id = $1 ORDER BY started_at ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("查询工作流节点运行失败: %w", err)
	}
	defer rows.Close()
	var out []*WorkflowNodeRun
	for rows.Next() {
		row, err := scanWorkflowNodeRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// WorkflowRunsAwaitingHITL 镜像 ListWorkflowRunsAwaitingHITLFiltered（lim 默认 50）。
func (s *WorkflowStore) WorkflowRunsAwaitingHITL(ctx context.Context, conversationID string, limit int) ([]*WorkflowRun, error) {
	if limit <= 0 {
		limit = 50
	}
	conversationID = strings.TrimSpace(conversationID)
	var (
		rows *sql.Rows
		err  error
	)
	if conversationID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE status = 'awaiting_hitl' AND conversation_id = $1 ORDER BY started_at DESC LIMIT $2`,
			conversationID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE status = 'awaiting_hitl' ORDER BY started_at DESC LIMIT $1`,
			limit)
	}
	if err != nil {
		return nil, fmt.Errorf("查询等待审批的工作流运行失败: %w", err)
	}
	defer rows.Close()
	var out []*WorkflowRun
	for rows.Next() {
		row, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// boolToInt 镜像 findu boolToInt（enabled 列为 INTEGER）。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanWorkflowDefinition(scanner interface{ Scan(dest ...any) error }) (*WorkflowDefinition, error) {
	var row WorkflowDefinition
	var desc sql.NullString
	var enabled int
	if err := scanner.Scan(&row.ID, &row.Name, &desc, &row.Version, &row.GraphJSON, &enabled, &row.CreatedAt, &row.UpdatedAt); err != nil {
		return nil, err
	}
	row.Description = desc.String
	row.Enabled = enabled != 0
	return &row, nil
}

func scanWorkflowRun(scanner interface{ Scan(dest ...any) error }) (*WorkflowRun, error) {
	var row WorkflowRun
	var convID, projectID, roleID, inputJSON, outputJSON, errText, pendingNode, pendingJSON sql.NullString
	var finishedAt sql.NullTime
	if err := scanner.Scan(
		&row.ID, &row.WorkflowID, &row.WorkflowVersion,
		&convID, &projectID, &roleID, &row.Status,
		&inputJSON, &outputJSON, &errText,
		&pendingNode, &pendingJSON,
		&row.StartedAt, &finishedAt,
	); err != nil {
		return nil, err
	}
	row.ConversationID = convID.String
	row.ProjectID = projectID.String
	row.RoleID = roleID.String
	row.InputJSON = inputJSON.String
	row.OutputJSON = outputJSON.String
	row.Error = errText.String
	row.PendingHITLNodeID = pendingNode.String
	row.PendingHITLJSON = pendingJSON.String
	if finishedAt.Valid {
		t := finishedAt.Time
		row.FinishedAt = &t
	}
	return &row, nil
}

func scanWorkflowNodeRun(scanner interface{ Scan(dest ...any) error }) (*WorkflowNodeRun, error) {
	var row WorkflowNodeRun
	var inputJSON, outputJSON, errText sql.NullString
	var finishedAt sql.NullTime
	if err := scanner.Scan(&row.ID, &row.RunID, &row.NodeID, &row.Status, &inputJSON, &outputJSON, &errText, &row.StartedAt, &finishedAt); err != nil {
		return nil, err
	}
	row.InputJSON = inputJSON.String
	row.OutputJSON = outputJSON.String
	row.Error = errText.String
	if finishedAt.Valid {
		t := finishedAt.Time
		row.FinishedAt = &t
	}
	return &row, nil
}
