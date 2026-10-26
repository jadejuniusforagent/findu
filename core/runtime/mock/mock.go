// Package mock 提供 runtime-spi 的内存实现，供契约测试与本地演练。
//
// 它不模拟真实 agent 的异步规划或模型延迟：CreateSession/AppendTurn/InvokeModel/
// CallTool/Plan 同步完成并记录事件，StreamEvents 以确定性的方式重放事件。
// 契约测试通过它证明「换 adapter 不换 core」。
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"finduai/core/eventbus"
	"finduai/core/runtime"
)

// Runtime 是内存 runtime-spi 实现。所有方法都可并发调用。
type Runtime struct {
	mu          sync.Mutex
	nextSession int64
	nextTurn    int64
	nextCall    int64
	nextIntent  int64
	nextEvent   int64
	sessions    map[runtime.SessionID]runtime.SessionSpec
	turns       map[runtime.SessionID][]runtime.Turn
	events      []runtime.Event
	snapshots   map[runtime.SessionID]runtime.State
}

// NewRuntime 创建空 mock。
func NewRuntime() *Runtime {
	return &Runtime{
		sessions:  map[runtime.SessionID]runtime.SessionSpec{},
		turns:     map[runtime.SessionID][]runtime.Turn{},
		snapshots: map[runtime.SessionID]runtime.State{},
	}
}

var agentEventTypes = []eventbus.Type{
	eventbus.TypeSessionCreated,
	eventbus.TypeSessionClosed,
	eventbus.TypeTurnCreated,
	eventbus.TypeTokenDelta,
	eventbus.TypeToolCallStarted,
	eventbus.TypeToolCallFinish,
	eventbus.TypePlanUpdated,
	eventbus.TypeArtifactCreated,
	eventbus.TypeError,
}

func errCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (m *Runtime) recordLocked(typ eventbus.Type, sessionID runtime.SessionID, turnID runtime.TurnID, payload map[string]any) {
	m.nextEvent++
	m.events = append(m.events, runtime.Event{
		ID:        fmt.Sprintf("evt-%d", m.nextEvent),
		Type:      typ,
		TS:        time.Now().UTC(),
		Seq:       m.nextEvent,
		SessionID: string(sessionID),
		TurnID:    string(turnID),
		Delivery:  eventbus.AtLeastOnce,
		Payload:   payload,
	})
}

func (m *Runtime) CreateSession(ctx context.Context, spec runtime.SessionSpec) (runtime.SessionID, error) {
	if err := errCtx(ctx); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSession++
	id := runtime.SessionID(fmt.Sprintf("sess-%d", m.nextSession))
	m.sessions[id] = spec
	m.recordLocked(eventbus.TypeSessionCreated, id, "", map[string]any{
		"title":      spec.Title,
		"project_id": spec.ProjectID,
	})
	return id, nil
}

func (m *Runtime) AppendTurn(ctx context.Context, sessionID runtime.SessionID, turn runtime.Turn) (runtime.TurnID, error) {
	if err := errCtx(ctx); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sessionID]; !ok {
		return "", fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	if !turn.Role.Valid() {
		return "", fmt.Errorf("%w: role %q", runtime.ErrInvalid, turn.Role)
	}
	if turn.Content == "" {
		return "", fmt.Errorf("%w: turn content 不能为空", runtime.ErrInvalid)
	}
	m.nextTurn++
	id := runtime.TurnID(fmt.Sprintf("turn-%d", m.nextTurn))
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	m.turns[sessionID] = append(m.turns[sessionID], turn)
	m.recordLocked(eventbus.TypeTurnCreated, sessionID, id, map[string]any{
		"role":    string(turn.Role),
		"content": turn.Content,
	})
	return id, nil
}

func (m *Runtime) StreamEvents(ctx context.Context, sessionID runtime.SessionID, fromSeq int64) (<-chan runtime.Event, error) {
	if err := errCtx(ctx); err != nil {
		return nil, err
	}
	if fromSeq < 0 {
		return nil, fmt.Errorf("%w: from_seq 不能为负", runtime.ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sessionID]; !ok {
		return nil, fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	out := make([]runtime.Event, 0, len(m.events))
	for _, e := range m.events {
		if e.Seq > fromSeq && e.SessionID == string(sessionID) {
			out = append(out, e)
		}
	}
	ch := make(chan runtime.Event, len(out))
	for _, e := range out {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (m *Runtime) InvokeModel(ctx context.Context, req runtime.ModelRequest) (runtime.ModelResponse, error) {
	if err := errCtx(ctx); err != nil {
		return runtime.ModelResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[req.SessionID]; !ok {
		return runtime.ModelResponse{}, fmt.Errorf("%w: session %s", runtime.ErrNotFound, req.SessionID)
	}
	if len(req.Messages) == 0 {
		return runtime.ModelResponse{}, fmt.Errorf("%w: messages 不能为空", runtime.ErrInvalid)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Content == "" {
		return runtime.ModelResponse{}, fmt.Errorf("%w: message content 不能为空", runtime.ErrInvalid)
	}
	content := "mock: " + last.Content
	m.recordLocked(eventbus.TypeTokenDelta, req.SessionID, req.TurnID, map[string]any{
		"text":  content,
		"index": 0,
	})
	return runtime.ModelResponse{
		Message:      runtime.Message{Role: runtime.RoleAssistant, Content: content},
		Usage:        runtime.Usage{PromptTokens: len(last.Content), CompletionTokens: len(content), TotalTokens: len(last.Content) + len(content)},
		FinishReason: "stop",
	}, nil
}

func (m *Runtime) CallTool(ctx context.Context, call runtime.ToolCall) (runtime.ToolResult, error) {
	if err := errCtx(ctx); err != nil {
		return runtime.ToolResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[call.SessionID]; !ok {
		return runtime.ToolResult{}, fmt.Errorf("%w: session %s", runtime.ErrNotFound, call.SessionID)
	}
	if call.Name == "" {
		return runtime.ToolResult{}, fmt.Errorf("%w: tool name 不能为空", runtime.ErrInvalid)
	}
	if call.ID == "" {
		m.nextCall++
		call.ID = fmt.Sprintf("call-%d", m.nextCall)
	}
	m.recordLocked(eventbus.TypeToolCallStarted, call.SessionID, call.TurnID, map[string]any{
		"call_id": call.ID,
		"name":    call.Name,
		"args":    string(call.Arguments),
	})
	res := runtime.ToolResult{CallID: call.ID, Content: "mock: " + call.Name}
	m.recordLocked(eventbus.TypeToolCallFinish, call.SessionID, call.TurnID, map[string]any{
		"call_id":  call.ID,
		"name":     call.Name,
		"is_error": res.IsError,
	})
	return res, nil
}

func (m *Runtime) Plan(ctx context.Context, goal runtime.Goal, state runtime.State) ([]runtime.Intent, error) {
	if err := errCtx(ctx); err != nil {
		return nil, err
	}
	if goal.Text == "" {
		return nil, fmt.Errorf("%w: goal 不能为空", runtime.ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if state.SessionID != "" {
		if _, ok := m.sessions[state.SessionID]; !ok {
			return nil, fmt.Errorf("%w: session %s", runtime.ErrNotFound, state.SessionID)
		}
	}
	m.nextIntent++
	intent := runtime.Intent{
		ID:     fmt.Sprintf("intent-%d", m.nextIntent),
		Kind:   "single",
		Goal:   goal.Text,
		Inputs: goal.Metadata,
	}
	if state.SessionID != "" {
		m.recordLocked(eventbus.TypePlanUpdated, state.SessionID, "", map[string]any{
			"intents": []map[string]any{{"id": intent.ID, "kind": intent.Kind, "goal": intent.Goal}},
		})
	}
	return []runtime.Intent{intent}, nil
}

func (m *Runtime) Snapshot(ctx context.Context, sessionID runtime.SessionID) (runtime.State, error) {
	if err := errCtx(ctx); err != nil {
		return runtime.State{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sessionID]; !ok {
		return runtime.State{}, fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	data, _ := json.Marshal(map[string]any{"turns": len(m.turns[sessionID])})
	state := runtime.State{SessionID: sessionID, Version: 1, Seq: m.nextEvent, Data: data}
	m.snapshots[sessionID] = state
	return cloneState(state), nil
}

func (m *Runtime) Restore(ctx context.Context, sessionID runtime.SessionID, state runtime.State) error {
	if err := errCtx(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sessionID]; !ok {
		return fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	if state.SessionID != "" && state.SessionID != sessionID {
		return fmt.Errorf("%w: state session %s != %s", runtime.ErrInvalid, state.SessionID, sessionID)
	}
	state.SessionID = sessionID
	m.snapshots[sessionID] = cloneState(state)
	return nil
}

func (m *Runtime) Health(ctx context.Context) (runtime.Capabilities, error) {
	if err := errCtx(ctx); err != nil {
		return runtime.Capabilities{}, err
	}
	return runtime.Capabilities{
		Streaming:       true,
		ParallelWorkers: true,
		Snapshot:        true,
		EventTypes:      append([]eventbus.Type(nil), agentEventTypes...),
	}, nil
}

func cloneState(s runtime.State) runtime.State {
	if s.Data != nil {
		s.Data = append(json.RawMessage(nil), s.Data...)
	}
	return s
}
