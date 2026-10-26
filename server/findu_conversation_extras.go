package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// finduPlanTask mirrors the Eino plantask JSON file written under the shared
// skills directory. Keep the JSON names identical to findu's transport model.
type finduPlanTask struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Blocks      []string `json:"blocks,omitempty"`
	BlockedBy   []string `json:"blockedBy,omitempty"`
	ActiveForm  string   `json:"activeForm,omitempty"`
	Owner       string   `json:"owner,omitempty"`
}

// FinduConversationPlanTasks reads the same file-backed Eino task board that
// findu uses. When findu is not running the bridge, findu still owns this route
// so multi-agent plan boards remain visible.
func (s *Server) FinduConversationPlanTasks(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var one int
	if err := s.m.pg.QueryRow(`SELECT 1 FROM conversations WHERE id=$1`, id).Scan(&one); err != nil {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	s.chatMu.Lock()
	running := s.chatBusy[s.convBusyKey(id)]
	s.chatMu.Unlock()
	if !running {
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": []finduPlanTask{}, "total": 0, "completed": 0, "activeStep": 0, "running": false,
		})
		return
	}
	tasks, err := s.readFinduPlanTasks(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "获取任务列表失败")
		return
	}
	completed := 0
	activeStep := 0
	for i, task := range tasks {
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if status == "completed" {
			completed++
		}
		if activeStep == 0 && status == "in_progress" {
			activeStep = i + 1
		}
	}
	if activeStep == 0 {
		for i, task := range tasks {
			if strings.ToLower(strings.TrimSpace(task.Status)) != "completed" {
				activeStep = i + 1
				break
			}
		}
	}
	if activeStep == 0 && len(tasks) > 0 {
		activeStep = len(tasks)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": tasks, "total": len(tasks), "completed": completed,
		"activeStep": activeStep, "running": true,
	})
}

// readFinduPlanTasks reads skills/.eino/plantask/<conversation>/*.json and sorts
// by the numeric file name, matching findu's database reader.
func (s *Server) readFinduPlanTasks(conversationID string) ([]finduPlanTask, error) {
	out := []finduPlanTask{}
	base := strings.TrimSpace(s.skillDir)
	if base == "" || conversationID == "" || strings.ContainsAny(conversationID, `/\`) {
		return out, nil
	}
	dir := filepath.Join(base, ".eino", "plantask", conversationID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	type numbered struct {
		n    int
		task finduPlanTask
	}
	items := make([]numbered, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		idText := strings.TrimSuffix(entry.Name(), ".json")
		n, err := strconv.Atoi(idText)
		if err != nil || n < 1 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var task finduPlanTask
		if json.Unmarshal(raw, &task) != nil {
			continue
		}
		if strings.TrimSpace(task.ID) == "" {
			task.ID = idText
		}
		if strings.EqualFold(strings.TrimSpace(task.Status), "deleted") {
			continue
		}
		items = append(items, numbered{n: n, task: task})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].n < items[j].n })
	for _, item := range items {
		out = append(out, item.task)
	}
	return out, nil
}

type finduUsageSummary struct {
	Events           int64 `json:"events"`
	ModelCalls       int64 `json:"modelCalls"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	CachedTokens     int64 `json:"cachedTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
}

type finduUsageBreakdown struct {
	Key              string `json:"key"`
	Label            string `json:"label,omitempty"`
	Events           int64  `json:"events"`
	ModelCalls       int64  `json:"modelCalls"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	TotalTokens      int64  `json:"totalTokens"`
	CachedTokens     int64  `json:"cachedTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens"`
}

type finduUsageRecent struct {
	ID               string    `json:"id"`
	ProcessDetailID  string    `json:"processDetailId"`
	MessageID        string    `json:"messageId"`
	ConversationID   string    `json:"conversationId"`
	Model            string    `json:"model,omitempty"`
	Orchestration    string    `json:"orchestration,omitempty"`
	ModelCalls       int64     `json:"modelCalls"`
	PromptTokens     int64     `json:"promptTokens"`
	CompletionTokens int64     `json:"completionTokens"`
	TotalTokens      int64     `json:"totalTokens"`
	CachedTokens     int64     `json:"cachedTokens"`
	ReasoningTokens  int64     `json:"reasoningTokens"`
	CreatedAt        time.Time `json:"createdAt"`
}

// FinduConversationTokenUsage aggregates the shared PG model_token_usage rows.
func (s *Server) FinduConversationTokenUsage(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	days := atoiDefault(r.URL.Query().Get("days"), 7)
	if days <= 0 {
		days = 7
	}
	if days > 365 {
		days = 365
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 10)
	if limit <= 0 {
		limit = 10
	}
	if limit > 500 {
		limit = 500
	}
	var one int
	if err := s.m.pg.QueryRow(`SELECT 1 FROM conversations WHERE id=$1`, id).Scan(&one); err != nil {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	summary, err := s.usageSummary(`WHERE conversation_id=$1 AND created_at >= $2`, id, time.Now().AddDate(0, 0, -days+1))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	today, err := s.usageSummary(`WHERE conversation_id=$1 AND created_at >= $2`, id,
		time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byDay, err := s.usageBreakdown(`SELECT to_char(date(created_at),'YYYY-MM-DD') AS k,
		to_char(date(created_at),'YYYY-MM-DD') AS label, count(*),
		COALESCE(SUM(model_calls),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0),COALESCE(SUM(cached_tokens),0),COALESCE(SUM(reasoning_tokens),0)
		FROM model_token_usage WHERE conversation_id=$1 AND created_at >= $2
		GROUP BY date(created_at) ORDER BY k DESC LIMIT $3`, id, time.Now().AddDate(0, 0, -days+1), days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byModel, err := s.usageBreakdown(`SELECT COALESCE(NULLIF(TRIM(model),''),'unknown') AS k,
		COALESCE(NULLIF(TRIM(model),''),'Unknown') AS label, count(*),
		COALESCE(SUM(model_calls),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0),COALESCE(SUM(cached_tokens),0),COALESCE(SUM(reasoning_tokens),0)
		FROM model_token_usage WHERE conversation_id=$1 AND created_at >= $2
		GROUP BY k,label ORDER BY SUM(total_tokens) DESC LIMIT $3`, id, time.Now().AddDate(0, 0, -days+1), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byOrch, err := s.usageBreakdown(`SELECT COALESCE(NULLIF(TRIM(orchestration),''),'unknown') AS k,
		COALESCE(NULLIF(TRIM(orchestration),''),'Unknown') AS label, count(*),
		COALESCE(SUM(model_calls),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0),COALESCE(SUM(cached_tokens),0),COALESCE(SUM(reasoning_tokens),0)
		FROM model_token_usage WHERE conversation_id=$1 AND created_at >= $2
		GROUP BY k,label ORDER BY SUM(total_tokens) DESC LIMIT $3`, id, time.Now().AddDate(0, 0, -days+1), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	recent, err := s.usageRecent(id, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary, "today": today, "byDay": byDay, "byModel": byModel,
		"byOrchestration": byOrch, "recent": recent,
	})
}

func (s *Server) usageSummary(where string, args ...any) (finduUsageSummary, error) {
	var u finduUsageSummary
	err := s.m.pg.QueryRow(`SELECT count(*), COALESCE(SUM(model_calls),0), COALESCE(SUM(prompt_tokens),0),
		COALESCE(SUM(completion_tokens),0), COALESCE(SUM(total_tokens),0),
		COALESCE(SUM(cached_tokens),0), COALESCE(SUM(reasoning_tokens),0)
		FROM model_token_usage `+where, args...).Scan(
		&u.Events, &u.ModelCalls, &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens,
		&u.CachedTokens, &u.ReasoningTokens)
	return u, err
}

func (s *Server) usageBreakdown(query string, args ...any) ([]finduUsageBreakdown, error) {
	rows, err := s.m.pg.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []finduUsageBreakdown{}
	for rows.Next() {
		var u finduUsageBreakdown
		if err := rows.Scan(&u.Key, &u.Label, &u.Events, &u.ModelCalls, &u.PromptTokens,
			&u.CompletionTokens, &u.TotalTokens, &u.CachedTokens, &u.ReasoningTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Server) usageRecent(conversationID string, limit int) ([]finduUsageRecent, error) {
	rows, err := s.m.pg.Query(`SELECT id, process_detail_id, message_id, conversation_id,
		COALESCE(model,''), COALESCE(orchestration,''), model_calls, prompt_tokens, completion_tokens,
		total_tokens, cached_tokens, reasoning_tokens, created_at
		FROM model_token_usage WHERE conversation_id=$1 ORDER BY created_at DESC LIMIT $2`,
		conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []finduUsageRecent{}
	for rows.Next() {
		var u finduUsageRecent
		if err := rows.Scan(&u.ID, &u.ProcessDetailID, &u.MessageID, &u.ConversationID,
			&u.Model, &u.Orchestration, &u.ModelCalls, &u.PromptTokens, &u.CompletionTokens,
			&u.TotalTokens, &u.CachedTokens, &u.ReasoningTokens, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// FinduConversationResults returns messages and conversation-scoped
// vulnerabilities using the shared PG rows.
func (s *Server) FinduConversationResults(w http.ResponseWriter, r *http.Request) {
	if s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "数据库未配置")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var exists int
	if err := s.m.pg.QueryRow(`SELECT 1 FROM conversations WHERE id=$1`, id).Scan(&exists); err != nil {
		writeErr(w, http.StatusNotFound, "对话不存在")
		return
	}
	msgs, err := s.resultsMessages(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	vulns := []map[string]any{}
	rows, err := s.m.pg.Query(`SELECT row_to_json(v) FROM vulnerabilities v WHERE conversation_id=$1 LIMIT 1000`, id)
	if err == nil {
		for rows.Next() {
			var raw []byte
			if rows.Scan(&raw) == nil {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					// findu's Vulnerability JSON exposes vulnerability_type as type.
					if v, ok := m["vulnerability_type"]; ok {
						m["type"] = v
					}
					vulns = append(vulns, m)
				}
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversationId": id, "messages": msgs, "vulnerabilities": vulns,
		"executionResults": []any{},
	})
}

func (s *Server) resultsMessages(conversationID string) ([]map[string]any, error) {
	rows, err := s.m.pg.Query(`SELECT id, conversation_id, role, content,
		COALESCE(reasoning_content,''), created_at, updated_at
		FROM messages WHERE conversation_id=$1 ORDER BY created_at ASC, id ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, cid, role, content, reasoning string
		var created, updated time.Time
		if err := rows.Scan(&id, &cid, &role, &content, &reasoning, &created, &updated); err != nil {
			return nil, err
		}
		msg := map[string]any{
			"id": id, "conversationId": cid, "role": role, "content": content,
			"createdAt": created, "updatedAt": updated,
		}
		if reasoning != "" {
			msg["reasoningContent"] = reasoning
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}
