package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuditLog is the host-side projection of a findu/shared-PG audit row.
type AuditLog struct {
	ID           string                 `json:"id"`
	CreatedAt    time.Time              `json:"createdAt"`
	Level        string                 `json:"level"`
	Category     string                 `json:"category"`
	Action       string                 `json:"action"`
	Result       string                 `json:"result"`
	Actor        string                 `json:"actor"`
	SessionHint  string                 `json:"sessionHint,omitempty"`
	ClientIP     string                 `json:"clientIp,omitempty"`
	UserAgent    string                 `json:"userAgent,omitempty"`
	ResourceType string                 `json:"resourceType,omitempty"`
	ResourceID   string                 `json:"resourceId,omitempty"`
	Message      string                 `json:"message"`
	Detail       map[string]interface{} `json:"detail,omitempty"`
}

// AuditFilter is a query filter for audit logs.
type AuditFilter struct {
	Actor        string
	Level        string
	Category     string
	Action       string
	Result       string
	Query        string
	ResourceType string
	ResourceID   string
	Since        *time.Time
	Until        *time.Time
	Limit        int
	Offset       int
}

type AuditStore struct{ db *DB }

func (d *DB) AuditLogs() *AuditStore { return &AuditStore{db: d} }

func auditWhere(filter AuditFilter) (string, []any) {
	conds := []string{"1=1"}
	args := []any{}
	add := func(expr string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(expr, "?", "$"+strconv.Itoa(len(args))))
	}
	if v := strings.TrimSpace(filter.Actor); v != "" {
		add("actor = ?", v)
	}
	if v := strings.TrimSpace(filter.Level); v != "" {
		add("level = ?", v)
	}
	if v := strings.TrimSpace(filter.Category); v != "" {
		add("category = ?", v)
	}
	if v := strings.TrimSpace(filter.Action); v != "" {
		add("action = ?", v)
	}
	if v := strings.TrimSpace(filter.Result); v != "" {
		add("result = ?", v)
	}
	if v := strings.TrimSpace(filter.ResourceType); v != "" {
		add("resource_type = ?", v)
	}
	if v := strings.TrimSpace(filter.ResourceID); v != "" {
		add("resource_id = ?", v)
	}
	if filter.Since != nil {
		add("created_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		add("created_at <= ?", *filter.Until)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		args = append(args, "%"+q+"%")
		n := "$" + strconv.Itoa(len(args))
		conds = append(conds, "(message ILIKE "+n+" OR resource_id ILIKE "+n+" OR action ILIKE "+n+" OR category ILIKE "+n+" OR detail_json ILIKE "+n+")")
	}
	return strings.Join(conds, " AND "), args
}

func scanAuditLog(row interface {
	Scan(...any) error
}) (*AuditLog, error) {
	var l AuditLog
	var session, ip, ua, rt, rid, detail sql.NullString
	if err := row.Scan(&l.ID, &l.CreatedAt, &l.Level, &l.Category, &l.Action, &l.Result,
		&l.Actor, &session, &ip, &ua, &rt, &rid, &l.Message, &detail); err != nil {
		return nil, err
	}
	l.SessionHint, l.ClientIP, l.UserAgent = session.String, ip.String, ua.String
	l.ResourceType, l.ResourceID = rt.String, rid.String
	if detail.Valid && detail.String != "" {
		_ = json.Unmarshal([]byte(detail.String), &l.Detail)
	}
	return &l, nil
}

const auditSelectCols = `id, created_at, level, category, action, result, actor,
	COALESCE(session_hint,''), COALESCE(client_ip,''), COALESCE(user_agent,''),
	COALESCE(resource_type,''), COALESCE(resource_id,''), message, COALESCE(detail_json,'')`

func (s *AuditStore) List(filter AuditFilter) ([]*AuditLog, int, error) {
	where, args := auditWhere(filter)
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 5000 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.db.Query(`SELECT `+auditSelectCols+` FROM audit_logs WHERE `+where+`
		ORDER BY created_at DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*AuditLog{}
	for rows.Next() {
		l, err := scanAuditLog(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

func (s *AuditStore) Get(id string) (*AuditLog, error) {
	return scanAuditLog(s.db.QueryRow(`SELECT `+auditSelectCols+` FROM audit_logs WHERE id=$1`, strings.TrimSpace(id)))
}

// Append inserts one audit row (PG native). Mirrors findu AppendAuditLog:
// caller supplies category/action/result/resource fields; missing level
// defaults to info, missing id to a fresh uuid, zero time to now (UTC).
func (s *AuditStore) Append(row *AuditLog) error {
	if row == nil {
		return fmt.Errorf("audit log is nil")
	}
	if strings.TrimSpace(row.ID) == "" {
		row.ID = uuid.NewString()
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	} else {
		row.CreatedAt = row.CreatedAt.UTC()
	}
	if strings.TrimSpace(row.Level) == "" {
		row.Level = "info"
	}
	detailJSON := ""
	if len(row.Detail) > 0 {
		if b, err := json.Marshal(row.Detail); err == nil {
			detailJSON = string(b)
		}
	}
	_, err := s.db.Exec(`INSERT INTO audit_logs (
		id, created_at, level, category, action, result, actor, session_hint,
		client_ip, user_agent, resource_type, resource_id, message, detail_json
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		row.ID, row.CreatedAt, row.Level, row.Category, row.Action, row.Result,
		row.Actor, strings.TrimSpace(row.SessionHint), strings.TrimSpace(row.ClientIP), strings.TrimSpace(row.UserAgent),
		strings.TrimSpace(row.ResourceType), strings.TrimSpace(row.ResourceID), row.Message, detailJSON)
	return err
}

func (s *AuditStore) Summary(filter AuditFilter) (int64, int64, int64, error) {
	where, args := auditWhere(filter)
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE `+where, args...).Scan(&total); err != nil {
		return 0, 0, 0, err
	}
	failWhere := filter
	failWhere.Result = "failure"
	fwhere, fargs := auditWhere(failWhere)
	var failures int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE `+fwhere, fargs...).Scan(&failures); err != nil {
		return 0, 0, 0, err
	}
	since := time.Now().AddDate(0, 0, -7)
	recentWhere := filter
	recentWhere.Since = &since
	rwhere, rargs := auditWhere(recentWhere)
	var recent int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE `+rwhere, rargs...).Scan(&recent); err != nil {
		return 0, 0, 0, err
	}
	return total, failures, recent, nil
}
