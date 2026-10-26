package server

import (
	"database/sql"
	"finduai/core/log"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	finduConvTitleMax = maxConversationTitleRunes
	finduConvLimitMax = 1000
)

// FinduListConversations serves findu's GET /api/conversations with the shared
// PG rows. The DTO carries both findu camelCase and findu snake_case fields so
// both UIs can consume the same response.
func (s *Server) FinduListConversations(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	// findu always sends an explicit limit; the findu console requests the list
	// without one and expects the whole thread set, so keep a high default.
	limit := atoiDefault(r.URL.Query().Get("limit"), finduConvLimitMax)
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)
	if limit <= 0 {
		limit = finduConvLimitMax
	}
	if limit > finduConvLimitMax {
		limit = finduConvLimitMax
	}
	if offset < 0 {
		offset = 0
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	sortBy := strings.TrimSpace(r.URL.Query().Get("sort_by"))
	order := "updated_at"
	if strings.EqualFold(sortBy, "created_at") {
		order = "created_at"
	}
	where := []string{"1=1"}
	args := []any{}
	if projectID != "" {
		args = append(args, projectID)
		where = append(where, fmt.Sprintf("project_id=$%d", len(args)))
	}
	if search != "" {
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern)
		where = append(where, fmt.Sprintf("(title ILIKE $%d OR EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id=conversations.id AND m.content ILIKE $%d))", len(args)-1, len(args)))
	}
	whereSQL := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.m.pg.QueryRow(`SELECT count(*) FROM conversations`+whereSQL, args...).Scan(&total); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.m.pg.Query(fmt.Sprintf(`
		SELECT id, COALESCE(title,''), COALESCE(project_id,''), COALESCE(role_name,''),
		       COALESCE(agent_mode,''), COALESCE(agent_key,'findu_default'), llm_profile_id,
		       pinned_at, created_at, updated_at
		FROM conversations%s ORDER BY %s DESC, id DESC LIMIT $%d OFFSET $%d`,
		whereSQL, order, len(args)+1, len(args)+2), queryArgs...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	items := make([]finduConversationDTO, 0)
	for rows.Next() {
		var c finduConversationDTO
		var pinnedAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.Title, &c.ProjectID, &c.RoleName, &c.AgentMode,
			&c.AgentKey, &c.LLMProfileID, &pinnedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if pinnedAt.Valid {
			t := pinnedAt.Time
			c.PinnedAt = &t
			c.Pinned = true
		}
		c.CreatedAtSnake = c.CreatedAt
		c.UpdatedAtSnake = c.UpdatedAt
		s.chatMu.Lock()
		c.Running = s.chatBusy[s.convBusyKey(c.ID)]
		s.chatMu.Unlock()
		items = append(items, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": items, "total": total, "limit": limit, "offset": offset,
	})
}

// FinduCreateConversation serves findu's POST /api/conversations.
func (s *Server) FinduCreateConversation(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	var req struct {
		Title        string `json:"title"`
		ProjectID    string `json:"projectId"`
		RoleName     string `json:"roleName"`
		AgentMode    string `json:"agentMode"`
		AgentKey     string `json:"agent_key"`
		LLMProfileID *int64 `json:"llm_profile_id"`
	}
	if !decodeConversationRequest(w, r, &req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "新对话"
	}
	if utf8.RuneCountInString(title) > finduConvTitleMax {
		writeErr(w, http.StatusBadRequest, "标题过长")
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
	agentKey := strings.TrimSpace(req.AgentKey)
	if agentKey == "" {
		agentKey = finduRoleAgentKey(req.RoleName)
	}
	c, err := s.m.pg.CreateConversation(agentKey, title, req.LLMProfileID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	roleName := strings.TrimSpace(req.RoleName)
	agentMode := strings.TrimSpace(req.AgentMode)
	if agentMode == "" {
		agentMode = "eino_single"
	}
	if _, err := s.m.pg.Exec(`UPDATE conversations SET project_id=$2, role_name=$3, agent_mode=$4, updated_at=now() WHERE id=$1`,
		c.ID, nullIfEmpty(projectID), roleName, agentMode); err != nil {
		_ = s.m.pg.DeleteConversation(c.ID)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := finduConversationDTO{
		ID: c.ID, Title: title, ProjectID: projectID, RoleName: roleName,
		AgentMode: agentMode, AgentKey: agentKey, LLMProfileID: req.LLMProfileID,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		CreatedAtSnake: c.CreatedAt, UpdatedAtSnake: c.UpdatedAt,
	}
	writeJSON(w, http.StatusOK, out)
}

// FinduDeleteConversation serves findu's DELETE /api/conversations/{id},
// cancelling an active bridge run and removing dependent rows in one transaction.
func (s *Server) FinduDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	s.cancelConversation(id)
	tx, err := s.m.pg.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM process_details WHERE conversation_id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE conversation_id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.Exec(`DELETE FROM conversation_activities WHERE conversation_id=$1`, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := tx.Exec(`DELETE FROM conversations WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.removeConversationWorkdir(id)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": 1, "message": "删除成功"})
}

// removeConversationWorkdir 删除会话工作目录 <dataDir>/sessions/conv-<id>/，
// 包含 chatUpload（scope=session）落盘的 uploads/ 与 agent 会话产物；
// 同时清理 skills/.eino/plantask/<id>/（多代理任务板按会话分目录）。
// 会话 id 是服务端生成的 UUID，先做段名安全化再删除，防路径穿越。
func (s *Server) removeConversationWorkdir(id string) {
	if s.m == nil {
		return
	}
	seg := sanitizeConvSegment(id)
	if seg == "" {
		return
	}
	dirs := []string{filepath.Join(s.m.dir, "sessions", "conv-"+seg)}
	if s.skillDir != "" {
		dirs = append(dirs, filepath.Join(s.skillDir, ".eino", "plantask", seg))
	}
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("[conversation] 删除会话工作目录失败: %s: %v", dir, err)
		}
	}
}

// sanitizeConvSegment 只保留会话 id 的合法字符（UUID 字母数字连字符），
// 其它全部替换为 '-'，防止工作目录清理路径穿越。
func sanitizeConvSegment(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
