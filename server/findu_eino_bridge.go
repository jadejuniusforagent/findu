package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"finduai/acllm"
	"finduai/agent"
	coremodel "finduai/core/model"
	"finduai/domain/conversation"
	"finduai/transcript"

	"github.com/google/uuid"
)

// finduEinoRequest is the subset of findu ChatRequest needed by the bridge.
// Keeping the DTO here avoids importing internal/handler and its gin stack.
type finduAttachment struct {
	FileName   string `json:"fileName"`
	Content    string `json:"content,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	ServerPath string `json:"serverPath,omitempty"`
}

type finduEinoRequest struct {
	Message              string            `json:"message"`
	ConversationID       string            `json:"conversationId"`
	ProjectID            string            `json:"projectId"`
	Role                 string            `json:"role"`
	AgentMode            string            `json:"agentMode"`
	WebshellConnectionID string            `json:"webshellConnectionId"`
	Attachments          []finduAttachment `json:"attachments,omitempty"`
}

// EinoAgentBridge is the findu-compatible chat entry (POST /api/eino-agent and
// /api/eino-agent/stream). It keeps findu's conversation/messages/process_details
// storage and UI contract, but executes the turn through findu ChatAgent.
func (s *Server) EinoAgentBridge(streaming bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req finduEinoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求参数错误: "+err.Error())
			return
		}
		req.Message = strings.TrimSpace(req.Message)
		if req.Message == "" {
			writeErr(w, http.StatusBadRequest, "消息不能为空")
			return
		}
		if req.AgentMode != "" && req.AgentMode != "eino_single" {
			writeErr(w, http.StatusBadRequest, "当前 findu 桥接只支持 eino_single；"+req.AgentMode+" 仍由 findu 处理")
			return
		}

		c, err := s.einoBridgeConversation(req)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		ca := s.resolveChatAgent(c)
		if ca == nil {
			writeErr(w, http.StatusServiceUnavailable, s.chatUnavailableReason())
			return
		}
		s.einoBridgeSeedTranscript(ca.Transcript(), c.ID)

		userID := uuid.NewString()
		assistantID := uuid.NewString()
		if err := s.einoBridgeInsertMessage(c.ID, userID, "user", req.Message); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存用户消息失败: "+err.Error())
			return
		}
		if err := s.einoBridgeInsertMessage(c.ID, assistantID, "assistant", "处理中..."); err != nil {
			writeErr(w, http.StatusInternalServerError, "创建助手消息失败: "+err.Error())
			return
		}

		var (
			writeMu          sync.Mutex
			clientGone       bool
			assistantContent string
			assistantError   error
			sendEvent        func(typ, message string, data any)
		)
		sendEvent = func(typ, message string, data any) {
			if !streaming {
				return
			}
			payload := map[string]any{"type": typ, "message": message}
			if data != nil {
				payload["data"] = data
			}
			b, err := json.Marshal(payload)
			if err != nil {
				return
			}
			writeMu.Lock()
			defer writeMu.Unlock()
			if clientGone {
				return
			}
			select {
			case <-r.Context().Done():
				clientGone = true
				return
			default:
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				clientGone = true
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}

		if streaming {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
		}
		sendEvent("conversation", "", map[string]any{"conversationId": c.ID})
		sendEvent("message_saved", "", map[string]any{
			"conversationId": c.ID,
			"userMessageId":  userID,
		})

		activitySink := func(a coremodel.Activity) {
			text := a.Detail
			if text == "" {
				text = a.Summary
			}
			switch a.Kind {
			case "tool_use":
				s.einoBridgeProcessDetail(assistantID, c.ID, "tool_call", text, map[string]any{
					"tool": a.Tool, "toolUseId": a.ToolUseID, "input": text,
				})
				sendEvent("tool_call", text, map[string]any{
					"tool": a.Tool, "toolUseId": a.ToolUseID, "input": text,
				})
			case "tool_result":
				s.einoBridgeProcessDetail(assistantID, c.ID, "tool_result", text, map[string]any{
					"tool": a.Tool, "toolUseId": a.ToolUseID, "isError": a.IsError, "output": text,
				})
				sendEvent("tool_result", text, map[string]any{
					"tool": a.Tool, "toolUseId": a.ToolUseID, "isError": a.IsError, "output": text,
				})
			case "thinking":
				if text != "" {
					s.einoBridgeProcessDetail(assistantID, c.ID, "thinking_stream", text, map[string]any{"text": text})
				}
			case "text":
				if text != "" {
					s.einoBridgeProcessDetail(assistantID, c.ID, "response_stream", text, map[string]any{"text": text})
				}
			case "result":
				assistantContent = text
				if a.IsError {
					assistantError = fmt.Errorf("%s", text)
				}
			}
		}

		busyKey := s.convBusyKey(c.ID)
		bridgeCtx, cancel := context.WithCancelCause(r.Context())
		defer cancel(nil)
		s.chatMu.Lock()
		if s.chatBusy[busyKey] {
			s.chatMu.Unlock()
			s.einoBridgeFinishAssistant(assistantID, "该会话正在处理上一条消息，请稍候")
			sendEvent("error", "该会话正在处理上一条消息，请稍候", nil)
			sendEvent("done", "", map[string]any{"conversationId": c.ID})
			return
		}
		s.chatBusy[busyKey] = true
		if s.chatCancel == nil {
			s.chatCancel = map[string]context.CancelCauseFunc{}
		}
		s.chatCancel[busyKey] = cancel
		s.chatMu.Unlock()
		defer func() {
			s.chatMu.Lock()
			delete(s.chatBusy, busyKey)
			delete(s.chatCancel, busyKey)
			s.chatMu.Unlock()
		}()

		text, err := s.chatWithRuntime(
			bridgeCtx, ca, c.AgentKey, busyKey, composeBridgeMessage(req.Message, req.Attachments),
			s.agentMaxTurns(c.AgentKey),
			time.Duration(s.agentRunSeconds(c.AgentKey))*time.Second,
			s.einoBridgeWebSearch(c.AgentKey),
			activitySink,
		)
		if err != nil {
			assistantError = err
		}
		if text != "" {
			assistantContent = text
		}
		if assistantContent == "" {
			assistantContent = "（本次运行未产生助手文本）"
		}
		if assistantError != nil && text == "" {
			assistantContent = "执行出错: " + assistantError.Error()
		}
		stopped := errors.Is(context.Cause(bridgeCtx), agent.AbortChatStoppedByUser) ||
			(assistantError != nil && errors.Is(assistantError, context.Canceled))
		if stopped {
			assistantError = nil
			assistantContent = "已停止"
		}
		_ = s.einoBridgeFinishAssistant(assistantID, assistantContent)
		eventType := "response"
		switch {
		case stopped:
			eventType = "cancelled"
			s.einoBridgeProcessDetail(assistantID, c.ID, "cancelled", assistantContent, nil)
		case assistantError != nil:
			eventType = "error"
			s.einoBridgeProcessDetail(assistantID, c.ID, "error", assistantContent, map[string]any{"error": assistantError.Error()})
		default:
			s.einoBridgeProcessDetail(assistantID, c.ID, "response", assistantContent, map[string]any{
				"response": assistantContent, "assistantMessageId": assistantID,
			})
		}
		sendEvent(eventType, assistantContent, map[string]any{
			"conversationId":     c.ID,
			"assistantMessageId": assistantID,
		})
		sendEvent("done", "", map[string]any{"conversationId": c.ID})

		if !streaming {
			writeJSON(w, http.StatusOK, map[string]any{
				"conversationId": c.ID,
				"messageId":      assistantID,
				"response":       assistantContent,
			})
		}
	}
}

func (s *Server) einoBridgeConversation(req finduEinoRequest) (*conversation.Conversation, error) {
	if s.m == nil || s.m.pg == nil {
		return nil, fmt.Errorf("数据库未配置")
	}
	if strings.TrimSpace(req.ConversationID) != "" {
		// findu-created conversations may have a NULL agent_key. Query only the
		// bridge-relevant fields instead of the strict domain scanner.
		id := strings.TrimSpace(req.ConversationID)
		var agentKey, title string
		err := s.m.pg.QueryRow(`
			SELECT COALESCE(agent_key,'findu_default'), COALESCE(title,'')
			FROM conversations WHERE id=$1`, id).
			Scan(&agentKey, &title)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(agentKey) == "" {
			agentKey = finduRoleAgentKey(req.Role)
		}
		if strings.TrimSpace(req.WebshellConnectionID) != "" {
			_, _ = s.m.pg.Exec(`UPDATE conversations SET webshell_connection_id=$1, updated_at=now() WHERE id=$2`,
				req.WebshellConnectionID, id)
		}
		return &conversation.Conversation{ID: id, AgentKey: agentKey, Title: title}, nil
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID != "" {
		var one int
		if err := s.m.pg.QueryRow(`SELECT 1 FROM projects WHERE id=$1`, projectID).Scan(&one); err != nil {
			return nil, fmt.Errorf("项目不存在")
		}
	}
	title := strings.TrimSpace(req.Message)
	if r := []rune(title); len(r) > 50 {
		title = string(r[:50])
	}
	c, err := s.m.pg.CreateConversation(finduRoleAgentKey(req.Role), title, nil)
	if err != nil {
		return nil, err
	}
	if _, err := s.m.pg.Exec(`UPDATE conversations
		SET project_id=$2, role_name=$3, agent_mode=$4, webshell_connection_id=$5, updated_at=now()
		WHERE id=$1`,
		c.ID, nullIfEmpty(projectID), req.Role, "eino_single", nullIfEmpty(req.WebshellConnectionID)); err != nil {
		_ = s.m.pg.DeleteConversation(c.ID)
		return nil, err
	}
	return c, nil
}

func finduRoleAgentKey(role string) string {
	switch strings.TrimSpace(role) {
	case "", "默认":
		return "findu_default"
	case "渗透测试":
		return "pentest"
	case "报告撰写":
		return "reporter"
	case "漏洞复测":
		return "retester"
	case "审计取证":
		return "findu_audit"
	default:
		return "findu_default"
	}
}

func composeBridgeMessage(message string, attachments []finduAttachment) string {
	msg := strings.TrimSpace(message)
	for _, a := range attachments {
		path := strings.TrimSpace(a.ServerPath)
		if path != "" {
			name := strings.TrimSpace(a.FileName)
			if name == "" {
				name = "附件"
			}
			msg += "\n\n附件文件（" + name + "）：" + path
			continue
		}
		if strings.TrimSpace(a.Content) != "" {
			name := strings.TrimSpace(a.FileName)
			if name == "" {
				name = "附件"
			}
			msg += "\n\n附件内容（" + name + "）：\n" + a.Content
		}
	}
	return msg
}

func (s *Server) einoBridgeInsertMessage(conversationID, id, role, content string) error {
	_, err := s.m.pg.Exec(`
		INSERT INTO messages (id, conversation_id, role, content, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())`, id, conversationID, role, content)
	return err
}

func (s *Server) einoBridgeFinishAssistant(id, content string) error {
	_, err := s.m.pg.Exec(`UPDATE messages SET content=$1, updated_at=now() WHERE id=$2`, content, id)
	return err
}

func (s *Server) einoBridgeProcessDetail(messageID, conversationID, eventType, message string, data any) {
	if s.m == nil || s.m.pg == nil {
		return
	}
	raw := []byte("{}")
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			raw = b
		}
	}
	_, _ = s.m.pg.Exec(`
		INSERT INTO process_details (id, message_id, conversation_id, event_type, message, data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())`,
		uuid.NewString(), messageID, conversationID, eventType, message, string(raw))
}

// einoBridgeSeedTranscript imports findu's existing message history into the
// ChatAgent transcript on first use, so switching the executor does not drop the
// context of a conversation that was created by the findu runtime.
func (s *Server) einoBridgeSeedTranscript(tx *transcript.Store, conversationID string) {
	if tx == nil || s.m == nil || s.m.pg == nil {
		return
	}
	path := tx.MainPath("conv-" + conversationID)
	if recs, err := tx.Load(path); err == nil && len(recs) > 0 {
		return
	}
	rows, err := s.m.pg.Query(`SELECT role, content FROM messages WHERE conversation_id=$1 ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return
	}
	defer rows.Close()
	var recs []transcript.Record
	for rows.Next() {
		var role, content string
		if rows.Scan(&role, &content) != nil || strings.TrimSpace(content) == "" || content == "处理中..." {
			continue
		}
		var msg acllm.Message
		switch strings.ToLower(role) {
		case "user":
			msg = acllm.UserText(content)
		case "assistant":
			msg = acllm.Message{Role: acllm.RoleAssistant, Content: []acllm.ContentBlock{acllm.TextBlock(content)}}
		default:
			continue
		}
		recs = append(recs, transcript.Record{
			Type:      "message",
			SessionID: "conv-" + conversationID,
			Message:   &msg,
		})
	}
	if len(recs) > 0 {
		_ = tx.Replace(path, recs)
	}
}

func (s *Server) einoBridgeWebSearch(agentKey string) bool {
	if s.m == nil || s.m.pg == nil {
		return false
	}
	return s.webSearchFor(agentKey).Enabled
}
