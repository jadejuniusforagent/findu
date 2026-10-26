package db

// Step 6d Phase B6：HITL 中断表（hitl_interrupts）的原生只读/清理层。
//
// 数据面：共享 PG public schema 的 hitl_interrupts（findu `EnsureSchema` 创建，
// timestamptz 时间列）。行为与 findu internal/handler/hitl_logs.go 的投影逐字段
// 对齐（camelCase JSON、auditBackend/auditModel 推导、retentionDays 默认 90）。
//
// 范围取舍（见 docs/refactor/STEP6D-AB-HANDOFF.md）：
//   - 只覆盖 pending/logs/logs:{id}/logs 删除四条纯 DB 投影与清理；
//   - decision/dismiss 需要向 findu 进程内等待中的 `decideCh` 通道发信号，纯
//     DB 写会让在途会话卡到超时，故分到运行时收口批次（同 B4/WebShell exec）；
//   - config/:id、tool-whitelist×3、default-config/default-reviewer/
//     audit-strategy 的真源是 findu 进程内运行态 + 容器内 config.yaml，归
//     B3 config 批次；
//   - RBAC 细粒度会话可见性过滤暂不实现（同 B1，scope=all 管理员一致）；
//     retentionDays 固定返回 findu 缺省 90（config.yaml 未在容器外可达）。

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// HitlStore operates on the shared-PG hitl_interrupts table.
type HitlStore struct{ db *DB }

// Hitl returns the HITL store.
func (d *DB) Hitl() *HitlStore { return &HitlStore{db: d} }

// RetentionDaysDefault is findu's HitlConfig{} default (config.yaml omitted).
const RetentionDaysDefault = 90

// HitlHit mirrors findu hitlInterruptRowToMap (camelCase JSON).
type HitlHit struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversationId"`
	MessageID      string     `json:"messageId"`
	Mode           string     `json:"mode"`
	ToolName       string     `json:"toolName"`
	ToolCallID     string     `json:"toolCallId"`
	Payload        string     `json:"payload"`
	Status         string     `json:"status"`
	Reviewer       string     `json:"reviewer"`
	Decision       string     `json:"decision"`
	Comment        string     `json:"comment"`
	DecidedBy      string     `json:"decidedBy"`
	AuditBackend   string     `json:"auditBackend"`
	AuditModel     string     `json:"auditModel"`
	CreatedAt      time.Time  `json:"createdAt"`
	DecidedAt      *time.Time `json:"decidedAt"`
}

// HitlFilter mirrors findu appendHitlListFilters.
type HitlFilter struct {
	ConversationID string
	ToolName       string
	Decision       string
	DecidedBy      string
	Status         string
	Search         string
}

// hitlSelectCols 与 findu buildHitlListQuery 逐列对齐。
const hitlSelectCols = `id, conversation_id, message_id, mode, tool_name, tool_call_id, ` +
	`payload, status, COALESCE(reviewer,'human'), decision, decision_comment, ` +
	`COALESCE(decided_by,'human'), created_at, decided_at`

func hitlScan(rows *sql.Rows) ([]*HitlHit, error) {
	items := []*HitlHit{}
	for rows.Next() {
		h := &HitlHit{}
		var messageID, decision, comment sql.NullString
		var decidedAt sql.NullTime
		if err := rows.Scan(&h.ID, &h.ConversationID, &messageID, &h.Mode, &h.ToolName,
			&h.ToolCallID, &h.Payload, &h.Status, &h.Reviewer, &decision, &comment,
			&h.DecidedBy, &h.CreatedAt, &decidedAt); err != nil {
			return nil, err
		}
		h.MessageID = messageID.String
		h.Decision = decision.String
		h.Comment = comment.String
		if decidedAt.Valid {
			t := decidedAt.Time
			h.DecidedAt = &t
		}
		hitlFillAudit(h)
		items = append(items, h)
	}
	return items, rows.Err()
}

// hitlFillAudit 复刻 findu hitlAuditBackendFromRecord：仅 decided_by=audit_agent
// 的行推导 auditBackend/auditModel（优先 payload.hitlApproval，其次 comment）。
func hitlFillAudit(h *HitlHit) {
	h.DecidedBy = hitlNormalizeDecidedBy(h.DecidedBy)
	if h.DecidedBy != "audit_agent" {
		return
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(h.Payload), &root); err == nil {
		if appr, ok := root["hitlApproval"].(map[string]any); ok {
			if raw := hitlJSONString(appr["auditBackend"]); raw != "" {
				h.AuditBackend = hitlEffectiveBackend(raw)
			}
			h.AuditModel = hitlJSONString(appr["auditModel"])
		}
	}
	if h.AuditBackend == "" {
		h.AuditBackend = hitlInferBackendFromComment(h.Comment)
	}
	if h.AuditBackend == "" {
		h.AuditBackend = "openai"
	}
}

func hitlJSONString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(b))
}

// hitlEffectiveBackend 对齐 findu config.HitlConfig.EffectiveAuditBackend。
func hitlEffectiveBackend(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "typesafe", "jev", "type-safe", "typesafe-ai":
		return "typesafe"
	default:
		return "openai"
	}
}

// hitlInferBackendFromComment 对齐 findu inferHitlAuditBackendFromComment。
func hitlInferBackendFromComment(comment string) string {
	c := strings.ToLower(comment)
	if strings.Contains(comment, "TypeSafe") || strings.Contains(comment, "破坏分") ||
		strings.Contains(c, "choice=") || strings.Contains(comment, "Jev") {
		return "typesafe"
	}
	if strings.TrimSpace(comment) == "" {
		return ""
	}
	return "openai"
}

// hitlNormalizeDecidedBy 对齐 findu normalizeHitlDecidedBy。
func hitlNormalizeDecidedBy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "audit_agent", "agent", "ai":
		return "audit_agent"
	case "system", "timeout":
		return "system"
	case "manual":
		return "manual"
	default:
		return "human"
	}
}

// hitlConds 组装与 findu 相同的过滤条件（`?` 占位转 $n）。
func hitlConds(filter HitlFilter) (string, []any) {
	conds := []string{"1=1"}
	args := []any{}
	add := func(expr string, vs ...any) {
		n := 0
		for range vs {
			n++
			expr = strings.Replace(expr, "?", "$"+strconv.Itoa(len(args)+n), 1)
		}
		args = append(args, vs...)
		conds = append(conds, expr)
	}
	if v := strings.TrimSpace(filter.ConversationID); v != "" {
		add("conversation_id = ?", v)
	}
	if v := strings.TrimSpace(filter.ToolName); v != "" {
		add("tool_name LIKE ?", "%"+v+"%")
	}
	if v := strings.TrimSpace(filter.Decision); v != "" && v != "all" {
		add("decision = ?", v)
	}
	if v := strings.TrimSpace(filter.DecidedBy); v != "" && v != "all" {
		add("COALESCE(decided_by,'human') = ?", hitlNormalizeDecidedBy(v))
	}
	if v := strings.TrimSpace(filter.Status); v != "" && v != "all" {
		add("status = ?", v)
	}
	if v := strings.TrimSpace(filter.Search); v != "" {
		like := "%" + v + "%"
		add("(id LIKE ? OR conversation_id LIKE ? OR tool_name LIKE ? OR payload LIKE ? OR COALESCE(decision_comment,'') LIKE ?)",
			like, like, like, like, like)
	}
	return strings.Join(conds, " AND "), args
}

// hitlPageLimit 归一化 page/pageSize（findu：page>=1，1<=pageSize<=200，缺省 20）。
func hitlPageLimit(limit int) int {
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	return limit
}

// ListPending only returns human-pending interrupts（对齐 findu「真正等待用户
// 操作的人工审批」口径：agent 自审中的行不触发弹窗，也不进列表）。
func (s *HitlStore) ListPending(ctx context.Context, filter HitlFilter, limit, offset int) ([]*HitlHit, int, error) {
	conds, args := hitlConds(filter)
	where := "WHERE status = 'pending' AND COALESCE(reviewer,'human') = 'human' AND " + conds
	return s.hitlPage(ctx, where, args, "created_at DESC", limit, offset)
}

// ListLogs returns decided (non-pending) interrupts, newest decision first,
// aligned to findu ListHITLLogs order COALESCE(decided_at, created_at) DESC.
func (s *HitlStore) ListLogs(ctx context.Context, filter HitlFilter, limit, offset int) ([]*HitlHit, int, error) {
	conds, args := hitlConds(filter)
	where := "WHERE status != 'pending' AND " + conds
	return s.hitlPage(ctx, where, args, "COALESCE(decided_at, created_at) DESC", limit, offset)
}

func (s *HitlStore) hitlPage(ctx context.Context, where string, args []any, order string, limit, offset int) ([]*HitlHit, int, error) {
	total := 0
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM hitl_interrupts "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 20
	}
	limit = hitlPageLimit(limit)
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	lim, off := "$"+strconv.Itoa(len(args)-1), "$"+strconv.Itoa(len(args))
	q := "SELECT " + hitlSelectCols + " FROM hitl_interrupts " + where +
		" ORDER BY " + order + " LIMIT " + lim + " OFFSET " + off
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := hitlScan(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Get 按主键取一行（对应 findu GET /hitl/logs/:id）。
func (s *HitlStore) Get(ctx context.Context, id string) (*HitlHit, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+hitlSelectCols+" FROM hitl_interrupts WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := hitlScan(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, sql.ErrNoRows
	}
	return items[0], nil
}

// DeleteByIDs deletes decided (non-pending) logs by id; pending rows skipped
// （对齐 findu DeleteHitlInterruptLogsByIDs）。
func (s *HitlStore) DeleteByIDs(ctx context.Context, ids []string) (int64, error) {
	clean := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return 0, nil
	}
	ph := make([]string, len(clean))
	args := make([]any, len(clean))
	for i, id := range clean {
		ph[i] = "$" + strconv.Itoa(i+1)
		args[i] = id
	}
	q := "DELETE FROM hitl_interrupts WHERE status != 'pending' AND id IN (" + strings.Join(ph, ",") + ")"
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteWhere deletes decided logs by filter (findu all=true path).
func (s *HitlStore) DeleteWhere(ctx context.Context, filter HitlFilter) (int64, error) {
	conds, args := hitlConds(filter)
	q := "DELETE FROM hitl_interrupts WHERE status != 'pending' AND " + conds
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
