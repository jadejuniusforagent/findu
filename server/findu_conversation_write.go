package server

import (
	"net/http"
	"strings"
)

// FinduUpdateConversation handles findu's PUT /api/conversations/{id} title
// update against the shared PG row.
func (s *Server) FinduUpdateConversation(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Title string `json:"title"`
	}
	if !decodeConversationRequest(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "标题不能为空")
		return
	}
	res, err := s.m.pg.Exec(`UPDATE conversations SET title=$2, updated_at=now() WHERE id=$1`, id, req.Title)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	s.FinduGetConversation(w, r)
}

// FinduUpdateConversationPinned handles PUT /api/conversations/{id}/pinned.
func (s *Server) FinduUpdateConversationPinned(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Pinned bool `json:"pinned"`
	}
	if !decodeConversationRequest(w, r, &req) {
		return
	}
	res, err := s.m.pg.Exec(`UPDATE conversations SET
		pinned_at = CASE WHEN $2 THEN COALESCE(pinned_at, now()) ELSE NULL END,
		updated_at = now() WHERE id=$1`, id, req.Pinned)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "更新成功"})
}

// FinduUpdateConversationProject handles PUT /api/conversations/{id}/project.
func (s *Server) FinduUpdateConversationProject(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		ProjectID string `json:"projectId"`
	}
	if !decodeConversationRequest(w, r, &req) {
		return
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID != "" {
		var one int
		if err := s.m.pg.QueryRow(`SELECT 1 FROM projects WHERE id=$1`, projectID).Scan(&one); err != nil {
			writeErr(w, http.StatusBadRequest, "项目不存在")
			return
		}
	}
	res, err := s.m.pg.Exec(`UPDATE conversations SET project_id=$2, updated_at=now() WHERE id=$1`, id, nullIfEmpty(projectID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "projectId": projectID})
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
