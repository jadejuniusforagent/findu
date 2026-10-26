// 会话审批拦截的存储层（表 intercept_rules 全局规则 / intercept_pending 审批流水）。
//
// 说明：这两张表在 core/ownership 元数据里标为 plugin-hitl（HITL 插件的数据面），
// Step 4 插件化时由 plugins/* 的一侧认领；Step 3 迁移期先把 SQL 收进会话域，
// 因为审批发生在会话/任务执行链路上，调用方（server/* 与 intercept/*）都把它
// 当作会话的一部分。plugin 接手后仍复用本包，不重复实现。
package conversation

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"finduai/core/store"
)

// InterceptRule is one row of intercept_rules.
type InterceptRule struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Enabled        bool      `json:"enabled"`
	Priority       int       `json:"priority"`
	MatchTarget    string    `json:"match_target"`
	MatchType      string    `json:"match_type"`
	Pattern        string    `json:"pattern"`
	Action         string    `json:"action"`
	Message        string    `json:"message"`
	TimeoutEnabled bool      `json:"timeout_enabled"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	TimeoutAction  string    `json:"timeout_action"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// InterceptPending is one row of intercept_pending.
type InterceptPending struct {
	ID             int64           `json:"id"`
	RuleID         *int64          `json:"rule_id"`
	ConversationID *string         `json:"conversation_id"`
	TaskID         *string         `json:"task_id"`
	AgentName      string          `json:"agent_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	Status         string          `json:"status"`
	DecisionSource string          `json:"decision_source"`
	Reason         string          `json:"reason"` // 规则 message 或模型判定理由(前缀 [模型])
	DecidedAt      *time.Time      `json:"decided_at"`
	CreatedAt      time.Time       `json:"created_at"`
}

const interceptRuleCols = `id, name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action, created_at, updated_at`

func scanInterceptRule(row interface{ Scan(...any) error }) (InterceptRule, error) {
	var r InterceptRule
	err := row.Scan(&r.ID, &r.Name, &r.Enabled, &r.Priority,
		&r.MatchTarget, &r.MatchType, &r.Pattern, &r.Action, &r.Message,
		&r.TimeoutEnabled, &r.TimeoutSeconds, &r.TimeoutAction,
		&r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// ListInterceptRules returns all rules ordered by priority DESC then id.
func ListInterceptRules(q store.Querier) ([]InterceptRule, error) {
	rows, err := q.Query(`SELECT ` + interceptRuleCols + ` FROM intercept_rules ORDER BY priority DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterceptRule
	for rows.Next() {
		r, err := scanInterceptRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateInterceptRule inserts a new rule.
func CreateInterceptRule(q store.Querier, name, matchTarget, matchType, pattern, action, message string, priority int, enabled bool, timeoutEnabled bool, timeoutSeconds int, timeoutAction string) (InterceptRule, error) {
	row := q.QueryRow(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING `+interceptRuleCols,
		name, enabled, priority, matchTarget, matchType, pattern, action, message, timeoutEnabled, timeoutSeconds, timeoutAction)
	return scanInterceptRule(row)
}

// UpdateInterceptRule replaces all editable fields of an existing rule.
func UpdateInterceptRule(q store.Querier, id int64, name, matchTarget, matchType, pattern, action, message string, priority int, enabled bool, timeoutEnabled bool, timeoutSeconds int, timeoutAction string) (InterceptRule, error) {
	row := q.QueryRow(`
UPDATE intercept_rules
   SET name=$2, enabled=$3, priority=$4, match_target=$5,
       match_type=$6, pattern=$7, action=$8, message=$9,
       timeout_enabled=$10, timeout_seconds=$11, timeout_action=$12
WHERE id=$1
RETURNING `+interceptRuleCols,
		id, name, enabled, priority, matchTarget, matchType, pattern, action, message, timeoutEnabled, timeoutSeconds, timeoutAction)
	return scanInterceptRule(row)
}

// DeleteInterceptRule removes a rule.
func DeleteInterceptRule(q store.Querier, id int64) error {
	_, err := q.Exec(`DELETE FROM intercept_rules WHERE id=$1`, id)
	return err
}

// ToggleInterceptRule flips the enabled state of a rule.
func ToggleInterceptRule(q store.Querier, id int64, enabled bool) error {
	_, err := q.Exec(`UPDATE intercept_rules SET enabled=$2 WHERE id=$1`, id, enabled)
	return err
}

// CreateInterceptPending inserts a pending approval record and returns its ID.
// convID == "" → conversation_id stored as NULL (background task).
// taskID == "" → task_id stored as NULL.
func CreateInterceptPending(q store.Querier, ruleID int64, convID string, taskID, agentName, toolName string, input []byte, reason string, audits ...*InterceptAudit) (int64, error) {
	raw := json.RawMessage(input)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var convIDPtr *string
	if convID != "" {
		convIDPtr = &convID
	}
	var taskIDPtr *string
	if taskID != "" {
		taskIDPtr = &taskID
	}
	// ruleID == 0 → NULL: the LLM fallback judge has no owning rule.
	var ruleIDPtr *int64
	if ruleID != 0 {
		ruleIDPtr = &ruleID
	}
	var id int64
	err := q.QueryRow(`
INSERT INTO intercept_pending(rule_id, conversation_id, task_id, agent_name, tool_name, tool_input, reason, decision_source, audit)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ruleIDPtr, convIDPtr, taskIDPtr, agentName, toolName, raw, reason, interceptSource(ruleID, reason), firstAudit(audits)).Scan(&id)
	return id, err
}

// DecideInterceptPending updates a pending record's status (allowed/denied/timeout).
func DecideInterceptPending(q store.Querier, id int64, status string) error {
	_, err := q.Exec(`UPDATE intercept_pending SET status=$2, decided_at=NOW() WHERE id=$1`, id, status)
	return err
}

// CreateDecidedIntercept inserts an intercept_pending row ALREADY in a final state
// (status = 'allowed' | 'denied'), decided_at stamped now. Used to log allow/deny
// rule matches for observability — they don't block and need no user action, so unlike
// CreateInterceptPending (which starts 'pending') this records the outcome directly.
func CreateDecidedIntercept(q store.Querier, ruleID int64, convID string, taskID, agentName, toolName string, input []byte, status, reason string, audits ...*InterceptAudit) (int64, error) {
	raw := json.RawMessage(input)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var convIDPtr *string
	if convID != "" {
		convIDPtr = &convID
	}
	var taskIDPtr *string
	if taskID != "" {
		taskIDPtr = &taskID
	}
	// ruleID == 0 → NULL: the LLM fallback judge has no owning rule.
	var ruleIDPtr *int64
	if ruleID != 0 {
		ruleIDPtr = &ruleID
	}
	var id int64
	err := q.QueryRow(`
INSERT INTO intercept_pending(rule_id, conversation_id, task_id, agent_name, tool_name, tool_input, status, reason, decided_at, decision_source, audit)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), $9, $10) RETURNING id`,
		ruleIDPtr, convIDPtr, taskIDPtr, agentName, toolName, raw, status, reason, interceptSource(ruleID, reason), firstAudit(audits)).Scan(&id)
	return id, err
}

const interceptPendingCols = `id, rule_id, conversation_id, task_id, agent_name, tool_name, tool_input, status, reason, decided_at, created_at, decision_source`

func scanInterceptPending(s interface{ Scan(...any) error }, p *InterceptPending) error {
	return s.Scan(&p.ID, &p.RuleID, &p.ConversationID, &p.TaskID, &p.AgentName,
		&p.ToolName, &p.ToolInput, &p.Status, &p.Reason, &p.DecidedAt, &p.CreatedAt, &p.DecisionSource)
}

// ListPendingIntercepts returns all unresolved approval requests, newest first.
func ListPendingIntercepts(q store.Querier) ([]InterceptPending, error) {
	rows, err := q.Query(`SELECT ` + interceptPendingCols + ` FROM intercept_pending WHERE status='pending' ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterceptPending
	for rows.Next() {
		var p InterceptPending
		if err := scanInterceptPending(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetInterceptPending returns one pending record (nil if absent).
func GetInterceptPending(q store.Querier, id int64) (*InterceptPending, error) {
	var p InterceptPending
	err := scanInterceptPending(
		q.QueryRow(`SELECT `+interceptPendingCols+` FROM intercept_pending WHERE id=$1`, id),
		&p,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

// InterceptApprovalRow is intercept_pending enriched with conversation and rule info.
type InterceptApprovalRow struct {
	InterceptPending
	ConvTitle    string `json:"conv_title"`
	ConvAgentKey string `json:"conv_agent_key"`
	RuleName     string `json:"rule_name"`
}

func scanInterceptApprovalRow(rows interface{ Scan(...any) error }, r *InterceptApprovalRow) error {
	return rows.Scan(
		&r.ID, &r.RuleID, &r.ConversationID, &r.TaskID, &r.AgentName,
		&r.ToolName, &r.ToolInput, &r.Status, &r.Reason, &r.DecidedAt, &r.CreatedAt,
		&r.DecisionSource, &r.ConvTitle, &r.ConvAgentKey, &r.RuleName,
	)
}

// Keep legacy rows without decision_source consistent with their displayed source.
const approvalDecisionSource = `COALESCE(NULLIF(ip.decision_source,''), CASE
 WHEN ip.rule_id IS NOT NULL THEN 'rule'
 WHEN ip.reason LIKE '[模型]%' THEN 'model' ELSE 'unknown' END)`

const approvalRowColumns = `ip.id, ip.rule_id, ip.conversation_id, ip.task_id, ip.agent_name,
       ip.tool_name, ip.tool_input, ip.status, ip.reason, ip.decided_at, ip.created_at, ` + approvalDecisionSource + `,
       COALESCE(c.title,'') AS conv_title,
       COALESCE(c.agent_key,'') AS conv_agent_key,
       COALESCE(ir.name,'') AS rule_name`

const approvalRowJoins = `
FROM intercept_pending ip
LEFT JOIN conversations c ON c.id = ip.conversation_id
LEFT JOIN intercept_rules ir ON ir.id = ip.rule_id`

const approvalRowSelect = `SELECT ` + approvalRowColumns + approvalRowJoins

func interceptSource(ruleID int64, reason string) string {
	if ruleID != 0 {
		return "rule"
	}
	if strings.HasPrefix(reason, "[模型]") {
		return "model"
	}
	return "unknown"
}

func firstAudit(audits []*InterceptAudit) any {
	if len(audits) == 0 || audits[0] == nil {
		return nil
	}
	raw, err := json.Marshal(audits[0])
	if err != nil {
		return nil
	}
	return raw
}

// ListAllIntercepts returns up to limit intercept_pending rows (newest first)
// joined with conversation and rule info.
func ListAllIntercepts(q store.Querier, limit int) ([]InterceptApprovalRow, error) {
	rows, err := q.Query(approvalRowSelect+` ORDER BY ip.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterceptApprovalRow
	for rows.Next() {
		var r InterceptApprovalRow
		if err := scanInterceptApprovalRow(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InterceptApprovalFilter combines exact status and decision-source filters.
// Empty fields include all values.
type InterceptApprovalFilter struct {
	Status         string
	DecisionSource string
}

// ListAllInterceptsPage returns one 1-based page and the total matching count.
func ListAllInterceptsPage(q store.Querier, page, size int, filter InterceptApprovalFilter) ([]InterceptApprovalRow, int, error) {
	return listInterceptsPage(q, "", page, size, filter)
}

// ListTaskIntercepts returns all intercept_pending rows for a specific task (newest first).
func ListTaskIntercepts(q store.Querier, taskID string) ([]InterceptApprovalRow, error) {
	rows, err := q.Query(approvalRowSelect+` WHERE ip.task_id=$1 ORDER BY ip.created_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterceptApprovalRow
	for rows.Next() {
		var r InterceptApprovalRow
		if err := scanInterceptApprovalRow(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListTaskInterceptsPage is the paginated variant of ListTaskIntercepts.
func ListTaskInterceptsPage(q store.Querier, taskID string, page, size int, filter InterceptApprovalFilter) ([]InterceptApprovalRow, int, error) {
	return listInterceptsPage(q, taskID, page, size, filter)
}

func listInterceptsPage(q store.Querier, taskID string, page, size int, filter InterceptApprovalFilter) ([]InterceptApprovalRow, int, error) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	offset := (page - 1) * size

	conditions := []string{}
	args := []any{}
	add := func(column, value string) {
		if value != "" {
			args = append(args, value)
			conditions = append(conditions, column+"=$"+fmt.Sprint(len(args)))
		}
	}
	add("ip.task_id", taskID)
	add("ip.status", filter.Status)
	add(approvalDecisionSource, filter.DecisionSource)
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := q.QueryRow("SELECT COUNT(*) FROM intercept_pending ip"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitArg := len(args) + 1
	offsetArg := limitArg + 1
	dataQ := approvalRowSelect + where +
		" ORDER BY ip.created_at DESC, ip.id DESC LIMIT $" + fmt.Sprint(limitArg) +
		" OFFSET $" + fmt.Sprint(offsetArg)
	args = append(args, size, offset)
	rows, err := q.Query(dataQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []InterceptApprovalRow{}
	for rows.Next() {
		var r InterceptApprovalRow
		if err := scanInterceptApprovalRow(rows, &r); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
