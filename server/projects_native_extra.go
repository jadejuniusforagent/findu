package server

// Step 6d Phase A：projects delete/facts/stats/dashboard-summary/conversations
// 原生 handler。facts 的 links/edges 与 fact-graph/promote-attack-chain 留 findu
// 兼容（见 db/projects_facts.go 头注与 STEP6D-AB-HANDOFF）。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"finduai/db"
)

func (s *Server) projectStore() *db.ProjectStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.Projects()
}

// deleteProjectNative serves DELETE /api/projects/{id} (A5).
func (s *Server) deleteProjectNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := ps.DeleteProjectNative(r.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "项目不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// listProjectFactsNative serves GET /api/projects/{id}/facts (A6).
func (s *Server) listProjectFactsNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	projectID := r.PathValue("id")
	if key := strings.TrimSpace(r.URL.Query().Get("fact_key")); key != "" {
		f, err := ps.GetProjectFactByKey(projectID, key)
		if err != nil {
			writeErr(w, http.StatusNotFound, "事实不存在")
			return
		}
		writeJSON(w, http.StatusOK, f)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	q := r.URL.Query()
	filter := db.ProjectFactListFilter{
		Category:               q.Get("category"),
		Confidence:             q.Get("confidence"),
		Search:                 q.Get("search"),
		RelatedVulnerabilityID: q.Get("related_vulnerability_id"),
	}
	if q.Get("exclude_deprecated") == "1" || q.Get("exclude_deprecated") == "true" {
		filter.ExcludeDeprecated = true
	}
	list, err := ps.ListProjectFacts(projectID, filter, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []*db.ProjectFact{}
	}
	writeJSON(w, http.StatusOK, list)
}

type projectFactInput struct {
	FactKey                string `json:"fact_key"`
	Category               string `json:"category"`
	Summary                string `json:"summary"`
	Body                   string `json:"body"`
	Confidence             string `json:"confidence"`
	Pinned                 bool   `json:"pinned"`
	RelatedVulnerabilityID string `json:"related_vulnerability_id"`
}

// upsertProjectFactNative serves POST /api/projects/{id}/facts (A6)。
// links/links_text 与 body 内链接解析属 edges 域，仍走 findu 兼容路径。
func (s *Server) upsertProjectFactNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req projectFactInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Summary) == "" {
		writeErr(w, http.StatusBadRequest, "summary 必填")
		return
	}
	f := &db.ProjectFact{
		ProjectID:              r.PathValue("id"),
		FactKey:                req.FactKey,
		Category:               req.Category,
		Summary:                req.Summary,
		Body:                   req.Body,
		Confidence:             req.Confidence,
		Pinned:                 req.Pinned,
		RelatedVulnerabilityID: req.RelatedVulnerabilityID,
	}
	created, err := ps.UpsertProjectFact(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, created)
}

// updateProjectFactNative serves PUT /api/projects/{id}/facts/{factId} (A6)。
func (s *Server) updateProjectFactNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	projectID := r.PathValue("id")
	existing, err := ps.GetProjectFact(r.PathValue("factId"))
	if err != nil || existing.ProjectID != projectID {
		writeErr(w, http.StatusNotFound, "事实不存在")
		return
	}
	var req struct {
		FactKey                *string `json:"fact_key"`
		Category               *string `json:"category"`
		Summary                *string `json:"summary"`
		Body                   *string `json:"body"`
		ClearBody              bool    `json:"clear_body"`
		Confidence             *string `json:"confidence"`
		Pinned                 *bool   `json:"pinned"`
		RelatedVulnerabilityID *string `json:"related_vulnerability_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.FactKey != nil {
		if k := strings.TrimSpace(*req.FactKey); k != "" {
			existing.FactKey = k
		}
	}
	if req.Category != nil && strings.TrimSpace(*req.Category) != "" {
		existing.Category = *req.Category
	}
	if req.Summary != nil && strings.TrimSpace(*req.Summary) != "" {
		existing.Summary = *req.Summary
	}
	if req.ClearBody {
		existing.Body = ""
	} else if req.Body != nil {
		existing.Body = *req.Body
	}
	if req.Confidence != nil && strings.TrimSpace(*req.Confidence) != "" {
		existing.Confidence = *req.Confidence
	}
	if req.Pinned != nil {
		existing.Pinned = *req.Pinned
	}
	if req.RelatedVulnerabilityID != nil {
		existing.RelatedVulnerabilityID = *req.RelatedVulnerabilityID
	}
	updated, err := ps.UpsertProjectFact(existing)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteProjectFactNative serves DELETE /api/projects/{id}/facts/{factId} (A6)。
func (s *Server) deleteProjectFactNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	existing, err := ps.GetProjectFact(r.PathValue("factId"))
	if err != nil || existing.ProjectID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "事实不存在")
		return
	}
	if err := ps.DeleteProjectFactNative(existing.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// deprecateProjectFactNative serves POST /api/projects/{id}/facts/deprecate (A6)。
func (s *Server) deprecateProjectFactNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		FactKey string `json:"fact_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.FactKey) == "" {
		writeErr(w, http.StatusBadRequest, "fact_key 必填")
		return
	}
	if err := ps.DeprecateProjectFactNative(r.PathValue("id"), req.FactKey); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// restoreProjectFactNative serves POST /api/projects/{id}/facts/restore (A6)。
func (s *Server) restoreProjectFactNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		FactKey    string `json:"fact_key"`
		Confidence string `json:"confidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.FactKey) == "" {
		writeErr(w, http.StatusBadRequest, "fact_key 必填")
		return
	}
	if err := ps.RestoreProjectFactNative(r.PathValue("id"), req.FactKey, req.Confidence); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// getProjectStatsNative serves GET /api/projects/{id}/stats (A7)。
func (s *Server) getProjectStatsNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	stats, err := ps.GetProjectStatsCounts(r.PathValue("id"))
	if err != nil {
		if strings.Contains(err.Error(), "项目不存在") {
			writeErr(w, http.StatusNotFound, "项目不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// sparse 判定与 findu project.IsSparseFactBody 同义：待补全字段为空。
	facts, err := ps.ListProjectFactsForSparseCheck(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, f := range facts {
		if isSparseFact(f.Category, f.FactKey, f.Body) {
			stats.SparseFactCount++
		}
	}
	writeJSON(w, http.StatusOK, stats)
}

// isSparseFact 判定待补全事实（对齐 findu project.IsSparseFactBody 的核心规则）。
func isSparseFact(category, factKey, body string) bool {
	if strings.TrimSpace(body) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "credential", "service", "network", "note", "asset", "":
		return true
	}
	return false
}

// projectDashboardSummaryNative serves GET /api/projects/dashboard-summary (A8)。
func (s *Server) projectDashboardSummaryNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("fact_limit")))
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}
	summary, err := ps.GetProjectDashboardSummary(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// listProjectConversationsNative serves GET /api/projects/{id}/conversations。
func (s *Server) listProjectConversationsNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectStore()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	projectID := r.PathValue("id")
	if _, err := ps.Get(projectID); err != nil {
		writeErr(w, http.StatusNotFound, "项目不存在")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	list, err := ps.ListProjectConversations(projectID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []*db.ProjectConversationRow{}
	}
	total, _ := ps.CountProjectConversations(projectID)
	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": list,
		"total":         total,
		"limit":         limit,
		"offset":        offset,
	})
}
