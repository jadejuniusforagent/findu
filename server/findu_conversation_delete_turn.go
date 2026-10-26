package server

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

func pgPlaceholders(start, count int) string {
	var b strings.Builder
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "$%d", start+i)
	}
	return b.String()
}

// FinduDeleteConversationTurn implements findu's POST
// /api/conversations/{id}/delete-turn. It removes the anchor turn (from its
// user message up to the next user message) from the shared messages table and
// clears the legacy last_react_* cache.
func (s *Server) FinduDeleteConversationTurn(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	conversationID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		MessageID string `json:"messageId"`
	}
	if !decodeConversationRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.MessageID) == "" {
		writeErr(w, http.StatusBadRequest, "messageId required")
		return
	}
	var exists int
	if err := s.m.pg.QueryRow(`SELECT 1 FROM conversations WHERE id=$1`, conversationID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			writeErr(w, http.StatusNotFound, "对话不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, err := s.m.pg.Query(`SELECT id, role FROM messages WHERE conversation_id=$1 ORDER BY created_at ASC, id ASC`, conversationID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type msg struct {
		id, role string
	}
	var msgs []msg
	for rows.Next() {
		var m msg
		if err := rows.Scan(&m.id, &m.role); err != nil {
			rows.Close()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	idx := -1
	for i := range msgs {
		if msgs[i].id == req.MessageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeErr(w, http.StatusBadRequest, "message not found")
		return
	}
	start := idx
	for start > 0 && msgs[start].role != "user" {
		start--
	}
	if start < len(msgs) && msgs[start].role != "user" {
		start = 0
	}
	end := len(msgs)
	for i := start + 1; i < len(msgs); i++ {
		if msgs[i].role == "user" {
			end = i
			break
		}
	}
	if start >= end {
		writeErr(w, http.StatusBadRequest, "empty turn range")
		return
	}
	deleted := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		deleted = append(deleted, msgs[i].id)
	}

	tx, err := s.m.pg.Begin()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	placeholders := pgPlaceholders(2, len(deleted))
	args := make([]any, 0, len(deleted)+1)
	args = append(args, conversationID)
	for _, id := range deleted {
		args = append(args, id)
	}
	res, err := tx.Exec(`DELETE FROM messages WHERE conversation_id=$1 AND id IN (`+placeholders+`)`, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); int(n) != len(deleted) {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("deleted count mismatch: %d/%d", n, len(deleted)))
		return
	}
	// process_details has no FK in the findu schema; remove them explicitly too.
	pdArgs := make([]any, 0, len(deleted))
	for _, id := range deleted {
		pdArgs = append(pdArgs, id)
	}
	if _, err := tx.Exec(`DELETE FROM process_details WHERE message_id IN (`+pgPlaceholders(1, len(deleted))+`)`, pdArgs...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.Exec(`UPDATE conversations SET last_react_input=NULL, last_react_output=NULL, updated_at=now() WHERE id=$1`, conversationID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deletedMessageIds": deleted,
		"message":           "ok",
	})
}
