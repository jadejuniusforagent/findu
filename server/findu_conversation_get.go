package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type finduConversationDTO struct {
	ID                   string         `json:"id"`
	Title                string         `json:"title"`
	ProjectID            string         `json:"projectId,omitempty"`
	RoleName             string         `json:"roleName,omitempty"`
	AgentMode            string         `json:"agentMode,omitempty"`
	AgentKey             string         `json:"agent_key,omitempty"`
	LLMProfileID         *int64         `json:"llm_profile_id,omitempty"`
	WebshellConnectionID string         `json:"webshellConnectionId,omitempty"`
	Pinned               bool           `json:"pinned"`
	PinnedAt             *time.Time     `json:"pinned_at,omitempty"`
	Running              bool           `json:"running,omitempty"`
	CreatedAt            time.Time      `json:"createdAt"`
	UpdatedAt            time.Time      `json:"updatedAt"`
	CreatedAtSnake       time.Time      `json:"created_at"`
	UpdatedAtSnake       time.Time      `json:"updated_at"`
	Messages             []finduMessage `json:"messages,omitempty"`
}

type finduMessage struct {
	ID               string                   `json:"id"`
	ConversationID   string                   `json:"conversationId"`
	Role             string                   `json:"role"`
	Content          string                   `json:"content"`
	ReasoningContent string                   `json:"reasoningContent,omitempty"`
	ProcessDetails   []map[string]interface{} `json:"processDetails,omitempty"`
	CreatedAt        time.Time                `json:"createdAt"`
	UpdatedAt        time.Time                `json:"updatedAt"`
}

// FinduGetConversation serves findu's GET /api/conversations/{id} from the
// shared PG conversation/messages/process_details tables.
func (s *Server) FinduGetConversation(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "bad conversation id")
		return
	}
	include := r.URL.Query().Get("include_process_details")
	withDetails := include == "1" || strings.EqualFold(include, "true") || strings.EqualFold(include, "yes")

	c := finduConversationDTO{}
	var pinnedAt sql.NullTime
	err := s.m.pg.QueryRow(`
		SELECT id, COALESCE(title,''), COALESCE(project_id,''), COALESCE(role_name,''),
		       COALESCE(agent_mode,''), COALESCE(agent_key,'findu_default'), llm_profile_id,
		       COALESCE(webshell_connection_id,''), pinned_at, created_at, updated_at
		FROM conversations WHERE id=$1`, id).Scan(
		&c.ID, &c.Title, &c.ProjectID, &c.RoleName, &c.AgentMode, &c.AgentKey, &c.LLMProfileID,
		&c.WebshellConnectionID, &pinnedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		writeErr(w, http.StatusNotFound, "对话不存在")
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
	if !withDetails {
		writeJSON(w, http.StatusOK, c)
		return
	}

	rows, err := s.m.pg.Query(`
		SELECT id, conversation_id, role, content, COALESCE(reasoning_content,''), created_at, updated_at
		FROM messages WHERE conversation_id=$1 ORDER BY created_at ASC, id ASC`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for rows.Next() {
		var m finduMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.ReasoningContent, &m.CreatedAt, &m.UpdatedAt); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		details, err := s.m.pg.Query(`
			SELECT id, event_type, message, COALESCE(data,''), created_at FROM process_details
			WHERE message_id=$1 ORDER BY created_at ASC, id ASC`, m.ID)
		if err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for details.Next() {
			var detailID, eventType, message, raw string
			var createdAt time.Time
			if err := details.Scan(&detailID, &eventType, &message, &raw, &createdAt); err != nil {
				details.Close()
				rows.Close()
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			entry := map[string]interface{}{
				"id": detailID, "eventType": eventType, "message": message, "createdAt": createdAt,
			}
			if raw != "" {
				var data interface{}
				if json.Unmarshal([]byte(raw), &data) == nil {
					entry["data"] = data
				}
			}
			m.ProcessDetails = append(m.ProcessDetails, entry)
		}
		details.Close()
		c.Messages = append(c.Messages, m)
	}
	rows.Close()
	writeJSON(w, http.StatusOK, c)
}
