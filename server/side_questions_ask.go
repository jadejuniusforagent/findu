package server

// 旁路提问（side questions）业务编排（批 4 第七刀自 side_questions.go
// handleSideQuestions 下沉）。
//
// handler 只做 HTTP 分支、body 上限/解码、query 解析与响应编码；本文件承载
// 历史清理、历史分页与提问准入（幂等/忙闲/并发上限/引用展开/快照落库/异步启动）。
// 返回 (响应体, HTTP 状态, 错误文案)：errMsg=="" 表示成功。

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"finduai/domain/conversation"
	"finduai/sidequestion"
)

// sideQuestionInput 是 POST /side-questions 的请求体。
type sideQuestionInput struct {
	Question string `json:"question"`
	ClientID string `json:"client_request_id"`
}

// clearSideHistory 取消该上下文的所有在跑旁路并清空历史（DELETE）。
func (s *Server) clearSideHistory(ctx context.Context, p sidequestion.Parent) (int, string) {
	key := p.Key()
	s.side.commands.Lock()
	defer s.side.commands.Unlock()
	s.side.mu.Lock()
	for _, run := range s.side.runs {
		if run.key == key {
			run.cancel()
		}
	}
	s.side.mu.Unlock()
	if err := s.m.pg.ClearSideHistory(ctx, key); err != nil {
		return 500, err.Error()
	}
	return 0, ""
}

// sideHistoryPage 读取一页历史 + 当前请求 + 快照元信息（GET）。
func (s *Server) sideHistoryPage(ctx context.Context, p sidequestion.Parent, before int64) (map[string]any, int, string) {
	key := p.Key()
	snap, err := s.sideSnapshot(ctx, p)
	if err != nil {
		return nil, 500, err.Error()
	}
	items, err := s.m.pg.SideHistory(ctx, key, before, 21)
	if err != nil {
		return nil, 500, err.Error()
	}
	var next int64
	if len(items) > 20 {
		items = items[:20]
		next = items[len(items)-1].Ordinal
	}
	current, err := s.m.pg.CurrentSideRequest(ctx, key)
	if err != nil {
		return nil, 500, err.Error()
	}
	var meta any
	if snap != nil {
		_, modelErr := s.sideProvider(snap.Model)
		reason := ""
		if modelErr != nil {
			reason = modelErr.Error()
		}
		meta = map[string]any{"captured_at": snap.CapturedAt, "model": snap.Model, "available": modelErr == nil, "reason": reason}
	}
	return map[string]any{"items": items, "current": current, "next_cursor": next, "snapshot": meta}, 0, ""
}

// askSideQuestion 执行提问准入并启动异步旁路（POST）。
// 幂等重放返回 200；新建返回 202；失败按原分类返回 400/409/429/500。
func (s *Server) askSideQuestion(ctx context.Context, p sidequestion.Parent, in sideQuestionInput) (*sidequestion.Exchange, int, string) {
	key := p.Key()
	// 快照在准入前读取（与拆分前顺序一致），避免持锁期间做多余查询。
	snap, err := s.sideSnapshot(ctx, p)
	if err != nil {
		return nil, 500, err.Error()
	}
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" || len([]rune(in.Question)) > 4000 || !validWorkerMessageRequestID(in.ClientID) {
		return nil, 400, "问题须为 1–4000 字符，并提供有效请求 ID"
	}
	s.side.commands.Lock()
	defer s.side.commands.Unlock()
	if p.TaskID > 0 && s.engine.IsDeleting(strconv.FormatInt(p.TaskID, 10)) {
		return nil, 409, "任务正在归档或删除"
	}
	existing, err := s.m.pg.ExistingSideRequest(ctx, key, in.ClientID)
	if err != nil {
		return nil, 500, err.Error()
	}
	if existing != nil {
		if existing.Question != in.Question {
			return nil, 409, "同一请求 ID 不能用于不同问题"
		}
		return existing, 200, ""
	}
	if snap == nil {
		return nil, 409, "尚无上下文快照，请先运行主 Agent"
	}
	provider, err := s.sideProvider(snap.Model)
	if err != nil {
		return nil, 409, err.Error()
	}
	s.side.mu.Lock()
	full := len(s.side.runs) >= 4
	busy := false
	for _, run := range s.side.runs {
		if run.key == key {
			busy = true
		}
	}
	s.side.mu.Unlock()
	if busy {
		return nil, 409, conversation.ErrSideBusy.Error()
	}
	if full {
		return nil, 429, "旁路请求已达并发上限，请稍后重试"
	}
	agentQuestion, err := composeChatMentionMessage(s.m.pg, in.Question)
	if err != nil {
		status := 500
		var inputErr *chatMentionInputError
		if errors.As(err, &inputErr) {
			status = 400
		}
		return nil, status, err.Error()
	}
	if err = s.m.pg.SaveSideSnapshot(ctx, *snap); err != nil {
		return nil, 500, err.Error()
	}
	e, created, err := s.m.pg.StartSideRequest(ctx, *snap, in.ClientID, in.Question)
	if err != nil {
		return nil, 409, err.Error()
	}
	if created {
		runCtx, cancel := context.WithTimeout(s.ctx, 120*time.Second)
		s.side.mu.Lock()
		s.side.runs[e.ID] = sideRun{key: key, parent: p, cancel: cancel, done: make(chan struct{})}
		s.side.mu.Unlock()
		go s.runSide(runCtx, cancel, *e, p, provider, *snap, agentQuestion)
	}
	return e, 202, ""
}
