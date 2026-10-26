package server

// 会话发消息业务编排（批 4 第八刀自 conversations.go pgSendConversationMessage 下沉）。
//
// handler 只做会话解析（404/400）、JSON 解码与响应编码；本文件承载内容校验、
// 引用展开、Agent 可用性、忙标记、用户轮落库、自动标题、附件清单与异步启动。
// 返回 (HTTP 状态, 错误文案)：errMsg=="" 表示成功（202）。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"finduai/core/log"
	"finduai/db"
	"finduai/domain/conversation"
)

// conversationMessageInput 是 POST /api/conversations/{id}/messages 的请求体。
type conversationMessageInput struct {
	Message     string           `json:"message"`
	Attachments []chatAttachment `json:"attachments,omitempty"` // 方式1 上传的文件(路径相对会话工作目录)
}

func (s *Server) sendConversationMessage(ctx context.Context, pg *db.DB, c *conversation.Conversation, in conversationMessageInput) (int, string) {
	msg := strings.TrimSpace(in.Message)
	if msg == "" && len(in.Attachments) == 0 {
		return 400, "消息不能为空"
	}
	agentMessage, err := composeChatMentionMessage(s.m.pg, msg)
	if err != nil {
		status := 500
		var inputErr *chatMentionInputError
		if errors.As(err, &inputErr) {
			status = 400
		}
		return status, err.Error()
	}
	// Same resolution as the background runner: honour the agent binding / this
	// conversation's chosen profile before falling back to the global config, so a
	// conversation that picked a valid LLM is not rejected just because no global
	// config is active.
	if s.resolveChatAgent(c) == nil {
		return 503, s.chatUnavailableReason()
	}

	busyKey := s.convBusyKey(c.ID)
	s.chatMu.Lock()
	if s.chatBusy[busyKey] {
		s.chatMu.Unlock()
		return 409, "该会话正在处理上一条消息，请稍候"
	}
	s.chatBusy[busyKey] = true
	s.chatMu.Unlock()

	// persist + float the human turn; auto-title the thread from the first message.
	// Worker is the agent key (not "user") so the transcript stays a single lane
	// (no worker chips) — the kind='user' already right-aligns it as a human bubble.
	// With attachments, Detail carries {text, attachments} JSON so the transcript
	// renders attachment cards; without, Detail is the full text (Summary is truncated,
	// so the transcript lazy-loads Detail to show the message untruncated).
	ua := userActivityWithAttachments(c.AgentKey, msg, in.Attachments)
	ua.Summary = firstLine(msg, 200)
	if ua.Detail == "" {
		ua.Detail = msg
	}
	if _, err := pg.AppendConvActivity(c.ID, ua); err != nil {
		log.Printf("[conv %s] append user msg failed: %v", c.ID, err)
	}
	if c.Title == "" || c.Title == "新对话" {
		title := firstLine(msg, 40)
		if title == "" {
			title = "附件消息"
		}
		_ = pg.RenameConversation(c.ID, title)
	}
	_ = pg.TouchConversation(c.ID)

	// Append the uploaded files' ABSOLUTE paths so the ChatAgent opens them with its
	// Read/Bash tools. baseDir = the agent's per-session CWD (<workDir>/sessions/
	// conv-<id>/), matching chatUpload's landing dir and agent/chat.go's sessionWorkDir
	// — busyKey == convBusyKey(c.ID) == "conv-<id>" == that session id.
	baseDir := filepath.Join(s.m.dir, "sessions", busyKey)
	s.runConversation(c, composeAgentMessage(agentMessage, in.Attachments, baseDir), busyKey, msg)
	return 202, ""
}
