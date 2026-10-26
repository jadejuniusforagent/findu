package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"finduai/agent"
	agentruntime "finduai/agent/runtimeadapter"
	coremodel "finduai/core/model"
	"finduai/core/runtime"
)

// RuntimeAdapterForAgent assembles the findu application adapter from the
// server's live ChatAgent and per-agent budgets.
func (s *Server) RuntimeAdapterForAgent(agentKey string) (runtime.Runtime, error) {
	ca := s.chatAgentRef()
	if ca == nil {
		return nil, fmt.Errorf("%w: chat agent is not configured", runtime.ErrUnsupported)
	}
	return s.runtimeAdapterForChatAgent(ca, agentKey, nil, nil)
}

func (s *Server) runtimeAdapterForChatAgent(ca *agent.ChatAgent, agentKey string, activitySink func(coremodel.Activity), resultSink func(string, error)) (runtime.Runtime, error) {
	if ca == nil {
		return nil, fmt.Errorf("%w: chat agent is not configured", runtime.ErrUnsupported)
	}
	webSearch := false
	maxTurns := 0
	maxDuration := time.Duration(0)
	if s.m != nil {
		maxTurns = s.agentMaxTurns(agentKey)
		maxDuration = time.Duration(s.agentRunSeconds(agentKey)) * time.Second
		if s.m.pg != nil {
			webSearch = s.webSearchFor(agentKey).Enabled
		}
	}
	return agentruntime.New(agentruntime.Config{
		Agent:        ca,
		AgentKey:     agentKey,
		MaxTurns:     maxTurns,
		MaxDuration:  maxDuration,
		WebSearch:    webSearch,
		ActivitySink: activitySink,
		ResultSink:   resultSink,
	})
}

// runtimeSPIEnabled is the opt-in cutover switch for conversation traffic.
// Default off keeps the existing ChatAgent.Chat path byte-for-byte unchanged.
func runtimeSPIEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FINDU_RUNTIME_SPI"))) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

type runtimeChatResult struct {
	text string
	err  error
}

// chatConversation is the call-site bridge: default path is ChatAgent.Chat;
// opt-in path drives the runtime-spi and forwards the original activities to
// the existing conversation activity sink.
func (s *Server) chatConversation(ctx context.Context, ca *agent.ChatAgent, agentKey, sessionID, msg string, maxTurns int, maxDuration time.Duration, webSearch bool, emit func(coremodel.Activity)) (string, error) {
	if !runtimeSPIEnabled() {
		return ca.Chat(ctx, agentKey, sessionID, msg, maxTurns, maxDuration, webSearch, emit)
	}
	return s.chatWithRuntime(ctx, ca, agentKey, sessionID, msg, maxTurns, maxDuration, webSearch, emit)
}

// chatWithRuntime forces the findu runtime path; used by the findu chat bridge
// which must not depend on the global opt-in switch.
func (s *Server) chatWithRuntime(ctx context.Context, ca *agent.ChatAgent, agentKey, sessionID, msg string, maxTurns int, maxDuration time.Duration, webSearch bool, emit func(coremodel.Activity)) (string, error) {
	resultCh := make(chan runtimeChatResult, 1)
	rt, err := s.runtimeAdapterForChatAgent(ca, agentKey, emit, func(text string, err error) {
		resultCh <- runtimeChatResult{text: text, err: err}
	})
	if err != nil {
		return "", err
	}
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Options: map[string]any{"session_id": sessionID}})
	if err != nil {
		return "", err
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: msg}); err != nil {
		return "", err
	}
	ch, err := rt.StreamEvents(ctx, sid, 0)
	if err != nil {
		return "", err
	}
	for range ch {
		// Events are drained to let the adapter run to completion. Activities
		// (the existing DB/UI contract) arrive through ActivitySink.
	}
	r := <-resultCh
	return r.text, r.err
}
