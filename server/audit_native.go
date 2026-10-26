package server

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finduai/db"
)

func (s *Server) auditStore() *db.AuditStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.AuditLogs()
}

func parseAuditTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t, nil
		}
	}
	return nil, errors.New("invalid time: " + raw)
}

func auditFilterFromQuery(r *http.Request) (db.AuditFilter, int, int, error) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	since, err := parseAuditTime(q.Get("since"))
	if err != nil {
		return db.AuditFilter{}, page, pageSize, err
	}
	until, err := parseAuditTime(q.Get("until"))
	if err != nil {
		return db.AuditFilter{}, page, pageSize, err
	}
	return db.AuditFilter{
		Actor:        strings.TrimSpace(q.Get("actor")),
		Level:        strings.TrimSpace(q.Get("level")),
		Category:     strings.TrimSpace(q.Get("category")),
		Action:       strings.TrimSpace(q.Get("action")),
		Result:       strings.TrimSpace(q.Get("result")),
		Query:        strings.TrimSpace(q.Get("q")),
		ResourceType: strings.TrimSpace(q.Get("resource_type")),
		ResourceID:   strings.TrimSpace(q.Get("resource_id")),
		Since:        since,
		Until:        until,
		Limit:        pageSize,
		Offset:       (page - 1) * pageSize,
	}, page, pageSize, nil
}

func (s *Server) auditMetaNative(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":           true,
		"retention_days":    15,
		"default_page_size": 20,
		"max_page_size":     100,
		"max_export":        5000,
	})
}

func (s *Server) auditSummaryNative(w http.ResponseWriter, r *http.Request) {
	as := s.auditStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	filter, _, _, err := auditFilterFromQuery(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	total, failures, recent, err := as.Summary(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasFilters := false
	for _, key := range []string{"actor", "level", "category", "action", "result", "q", "since", "until", "resource_type", "resource_id"} {
		if strings.TrimSpace(r.URL.Query().Get(key)) != "" {
			hasFilters = true
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "failures": failures, "recent_7d": recent, "has_filters": hasFilters,
	})
}

func (s *Server) auditLogsNative(w http.ResponseWriter, r *http.Request) {
	as := s.auditStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	filter, page, pageSize, err := auditFilterFromQuery(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	logs, total, err := as.List(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs": logs, "total": total, "page": page, "page_size": pageSize,
	})
}

func (s *Server) auditLogNative(w http.ResponseWriter, r *http.Request) {
	as := s.auditStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	log, err := as.Get(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "审计记录不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"log": log})
}

func (s *Server) auditExportNative(w http.ResponseWriter, r *http.Request) {
	as := s.auditStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	filter, _, _, err := auditFilterFromQuery(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.Limit = 5000
	filter.Offset = 0
	logs, _, err := as.List(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="audit-logs.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"id", "createdAt", "level", "category", "action", "result", "actor", "sessionHint", "clientIp", "userAgent", "resourceType", "resourceId", "message", "detail"})
		for _, log := range logs {
			detail, _ := json.Marshal(log.Detail)
			_ = cw.Write([]string{
				log.ID, log.CreatedAt.Format(time.RFC3339Nano), log.Level, log.Category, log.Action,
				log.Result, log.Actor, log.SessionHint, log.ClientIP, log.UserAgent,
				log.ResourceType, log.ResourceID, log.Message, string(detail),
			})
		}
		cw.Flush()
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="audit-logs.json"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"exported_at": time.Now().UTC().Format(time.RFC3339), "logs": logs,
	})
}
