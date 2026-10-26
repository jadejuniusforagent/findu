// Package runtimeadapter maps the runtime-spi onto the Eino/askernel
// implementation. It is an assembly-layer package: core/runtime stays
// independent of askernel, and only this package imports both sides.
//
// A Runtime is configured with one askernel.Options template. Each
// CreateSession copies that template, lets askernel allocate a fresh session
// id, and owns the resulting *askernel.Session. Direct InvokeModel/CallTool
// calls are stateless primitives; StreamEvents is the stateful agent loop.
package runtimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"finduai/acllm"
	"finduai/askernel"
	"finduai/core/eventbus"
	"finduai/core/runtime"
	"finduai/internal/spimodel"
	"finduai/tools"
)

// Runtime is the Eino-backed runtime-spi implementation.
type Runtime struct {
	opts     askernel.Options
	registry *tools.Registry

	mu         sync.Mutex
	sessions   map[runtime.SessionID]*session
	nextTurn   int64
	nextCall   int64
	nextIntent int64
	nextEvent  int64
}

type session struct {
	id   runtime.SessionID
	spec runtime.SessionSpec
	s    *askernel.Session

	// Guarded by Runtime.mu.
	turns   []runtime.Turn
	events  []runtime.Event
	pending []pendingTurn
	running bool
	closed  bool
}

type pendingTurn struct {
	id      runtime.TurnID
	content string
}

type snapshotData struct {
	Turns    []runtime.Turn    `json:"turns,omitempty"`
	Pending  []snapshotPending `json:"pending,omitempty"`
	Messages []acllm.Message   `json:"messages,omitempty"`
	Usage    acllm.Usage       `json:"usage,omitempty"`
}

type snapshotPending struct {
	ID      runtime.TurnID `json:"id"`
	Content string         `json:"content"`
}

var (
	_ runtime.Runtime     = (*Runtime)(nil)
	_ runtime.Snapshotter = (*Runtime)(nil)
)

// New validates the kernel configuration and builds an adapter. The adapter
// requires a model because both Prompt and InvokeModel need one. Tools nil is
// normalized to tools.DefaultTools, matching askernel.NewSession.
func New(opts askernel.Options) (*Runtime, error) {
	if opts.Model == nil {
		return nil, fmt.Errorf("%w: askernel adapter requires Options.Model", runtime.ErrInvalid)
	}
	opts.SessionID = "" // the adapter allocates one id per CreateSession
	if opts.Tools == nil {
		opts.Tools = tools.DefaultTools()
	}
	return &Runtime{
		opts:     opts,
		registry: buildRegistry(opts),
		sessions: map[runtime.SessionID]*session{},
	}, nil
}

// buildRegistry mirrors the tool assembly askernel.NewSession performs, so a
// direct CallTool can reach the same tools the agent loop can.
func buildRegistry(opts askernel.Options) *tools.Registry {
	set := append([]tools.CoreTool(nil), opts.Tools...)
	if opts.Noa != nil && opts.Noa.Takeover != nil {
		set = append(set, opts.Noa.Takeover.ExtraTools()...)
	}
	if opts.EnableWebFetch {
		set = append(set, tools.NewWebFetch(opts.WebFetch))
	}
	if opts.EnableWebSearch {
		if ws, err := tools.NewWebSearch(opts.WebSearch); err == nil {
			set = append(set, ws)
		}
	}
	reg := tools.NewRegistry(set...)
	if len(opts.DeferredTools) > 0 {
		unlock := opts.Unlock
		if unlock == nil {
			unlock = tools.NewUnlockSet(opts.DeferredTools...)
		}
		reg.Add(tools.NewSearchExtraTools(reg, opts.DeferredTools))
		reg.Add(tools.NewExecuteExtraTool(reg, unlock))
	}
	return reg
}

func errCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func nonNilCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// CreateSession builds a fresh askernel.Session and records session.created.
func (r *Runtime) CreateSession(ctx context.Context, spec runtime.SessionSpec) (runtime.SessionID, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return "", err
	}
	opts := r.opts // value copy: slices are read-only here
	opts.SessionID = ""
	if v, ok := spec.Options["session_id"].(string); ok && v != "" {
		if strings.ContainsAny(v, `/\`) || v == "." || v == ".." {
			return "", fmt.Errorf("%w: invalid session_id %q", runtime.ErrInvalid, v)
		}
		opts.SessionID = v
	}
	s := askernel.NewSession(opts)
	id := runtime.SessionID(s.SessionID())

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[id]; exists {
		return "", fmt.Errorf("%w: session %s", runtime.ErrConflict, id)
	}
	sess := &session{id: id, spec: spec, s: s}
	r.sessions[id] = sess
	r.recordLocked(sess, eventbus.TypeSessionCreated, "", map[string]any{
		"title":      spec.Title,
		"project_id": spec.ProjectID,
	})
	return id, nil
}

// AppendTurn stores a canonical turn and records turn.created. A user turn is
// queued for the next StreamEvents call; assistant/system/tool turns are
// recorded only because the kernel's DTO is its own source of truth.
func (r *Runtime) AppendTurn(ctx context.Context, sessionID runtime.SessionID, turn runtime.Turn) (runtime.TurnID, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return "", err
	}
	if !turn.Role.Valid() {
		return "", fmt.Errorf("%w: role %q", runtime.ErrInvalid, turn.Role)
	}
	if turn.Content == "" {
		return "", fmt.Errorf("%w: turn content 不能为空", runtime.ErrInvalid)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.sessions[sessionID]
	if !ok {
		return "", fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	if sess.closed {
		return "", fmt.Errorf("%w: session %s closed", runtime.ErrConflict, sessionID)
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	r.nextTurn++
	turnID := runtime.TurnID(fmt.Sprintf("turn-%d", r.nextTurn))
	sess.turns = append(sess.turns, turn)
	if turn.Role == runtime.RoleUser {
		sess.pending = append(sess.pending, pendingTurn{id: turnID, content: turn.Content})
	}
	r.recordLocked(sess, eventbus.TypeTurnCreated, turnID, map[string]any{
		"role":    string(turn.Role),
		"content": turn.Content,
	})
	return turnID, nil
}

// StreamEvents replays events after fromSeq (exclusive) and, when a queued user
// turn exists, runs the real askernel loop and streams its events. Only one
// agent loop may run per session; a concurrent live call returns ErrConflict.
func (r *Runtime) StreamEvents(ctx context.Context, sessionID runtime.SessionID, fromSeq int64) (<-chan runtime.Event, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return nil, err
	}
	if fromSeq < 0 {
		return nil, fmt.Errorf("%w: from_seq 不能为负", runtime.ErrInvalid)
	}

	r.mu.Lock()
	sess, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	if sess.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: session %s closed", runtime.ErrConflict, sessionID)
	}
	replay := make([]runtime.Event, 0)
	for _, e := range sess.events {
		if e.Seq > fromSeq {
			replay = append(replay, e)
		}
	}
	var pending []pendingTurn
	if len(sess.pending) > 0 {
		if sess.running {
			r.mu.Unlock()
			return nil, fmt.Errorf("%w: session %s already running", runtime.ErrConflict, sessionID)
		}
		sess.running = true
		pending = sess.pending
		sess.pending = nil
	}
	r.mu.Unlock()

	ch := make(chan runtime.Event, 32)
	go func() {
		defer close(ch)
		if len(pending) > 0 {
			defer func() {
				r.mu.Lock()
				sess.running = false
				r.mu.Unlock()
			}()
		}
		for _, e := range replay {
			if !send(ctx, ch, e) {
				return
			}
		}
		if len(pending) == 0 {
			return
		}
		for _, p := range pending {
			if !r.runPrompt(ctx, sess, p, ch) {
				return
			}
		}
	}()
	return ch, nil
}

func send(ctx context.Context, ch chan<- runtime.Event, e runtime.Event) bool {
	select {
	case ch <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// runPrompt drives one askernel Prompt and maps its RunEvent stream onto the
// event contract. It returns false when the consumer/context stopped.
func (r *Runtime) runPrompt(ctx context.Context, sess *session, p pendingTurn, ch chan<- runtime.Event) bool {
	toolNames := map[string]string{}
	for ev, err := range sess.s.Prompt(ctx, p.content) {
		if err != nil {
			// Defensive: askernel folds normal failures into KindResult.
			if ctx.Err() != nil {
				return false
			}
			if !send(ctx, ch, r.record(sess, eventbus.TypeError, p.id, map[string]any{
				"message":   err.Error(),
				"retryable": false,
			})) {
				return false
			}
			continue
		}
		switch ev.Kind {
		case askernel.KindText:
			if ev.Text == "" {
				continue
			}
			if !send(ctx, ch, r.record(sess, eventbus.TypeTokenDelta, p.id, map[string]any{
				"kind": "text",
				"text": ev.Text,
			})) {
				return false
			}
		case askernel.KindThinking:
			if ev.Text == "" {
				continue
			}
			if !send(ctx, ch, r.record(sess, eventbus.TypeTokenDelta, p.id, map[string]any{
				"kind": "thinking",
				"text": ev.Text,
			})) {
				return false
			}
		case askernel.KindToolUse:
			if ev.ToolUse == nil {
				continue
			}
			toolNames[ev.ToolUse.ID] = ev.ToolUse.Name
			if !send(ctx, ch, r.record(sess, eventbus.TypeToolCallStarted, p.id, map[string]any{
				"call_id": ev.ToolUse.ID,
				"name":    ev.ToolUse.Name,
				"args":    string(ev.ToolUse.Input),
			})) {
				return false
			}
		case askernel.KindToolResult:
			if ev.ToolResult == nil {
				continue
			}
			if !send(ctx, ch, r.record(sess, eventbus.TypeToolCallFinish, p.id, map[string]any{
				"call_id":  ev.ToolResult.ToolUseID,
				"name":     toolNames[ev.ToolResult.ToolUseID],
				"is_error": ev.ToolResult.IsError,
				"output":   blocksText(ev.ToolResult.Content),
			})) {
				return false
			}
		case askernel.KindResult:
			if ev.Terminal == nil {
				continue
			}
			if ev.Terminal.Err != nil && ctx.Err() == nil {
				if !send(ctx, ch, r.record(sess, eventbus.TypeError, p.id, map[string]any{
					"message":   ev.Terminal.Err.Error(),
					"reason":    string(ev.Terminal.Reason),
					"retryable": false,
				})) {
					return false
				}
			}
			if ev.Terminal.Text != "" {
				tid := r.newTurnID()
				if !send(ctx, ch, r.record(sess, eventbus.TypeTurnCreated, tid, map[string]any{
					"role":    string(runtime.RoleAssistant),
					"content": ev.Terminal.Text,
				})) {
					return false
				}
			}
		}
	}
	return ctx.Err() == nil
}

// InvokeModel is a direct, stateless model call using the decorated model from
// the adapter configuration. It does not mutate the askernel session DTO.
func (r *Runtime) InvokeModel(ctx context.Context, req runtime.ModelRequest) (runtime.ModelResponse, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return runtime.ModelResponse{}, err
	}
	r.mu.Lock()
	_, ok := r.sessions[req.SessionID]
	r.mu.Unlock()
	if !ok {
		return runtime.ModelResponse{}, fmt.Errorf("%w: session %s", runtime.ErrNotFound, req.SessionID)
	}
	resp, err := spimodel.InvokeModel(ctx, r.opts.Model, req)
	if err != nil {
		if ctx.Err() != nil {
			return runtime.ModelResponse{}, ctx.Err()
		}
		if errors.Is(err, runtime.ErrRetryable) {
			r.recordForSession(req.SessionID, eventbus.TypeError, req.TurnID, map[string]any{
				"message":   err.Error(),
				"retryable": true,
			})
		}
		return runtime.ModelResponse{}, err
	}
	if resp.Message.Content != "" {
		r.recordForSession(req.SessionID, eventbus.TypeTokenDelta, req.TurnID, map[string]any{
			"kind": "text",
			"text": resp.Message.Content,
		})
	}
	return resp, nil
}

// CallTool is the single direct tool entry. It invokes the same CoreTool the
// kernel uses and records tool.call.started/finished. Authorization remains the
// host's concern; this primitive does not run the agent's permission loop.
func (r *Runtime) CallTool(ctx context.Context, call runtime.ToolCall) (runtime.ToolResult, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return runtime.ToolResult{}, err
	}
	if call.Name == "" {
		return runtime.ToolResult{}, fmt.Errorf("%w: tool name 不能为空", runtime.ErrInvalid)
	}

	r.mu.Lock()
	sess, ok := r.sessions[call.SessionID]
	if !ok {
		r.mu.Unlock()
		return runtime.ToolResult{}, fmt.Errorf("%w: session %s", runtime.ErrNotFound, call.SessionID)
	}
	if sess.closed {
		r.mu.Unlock()
		return runtime.ToolResult{}, fmt.Errorf("%w: session %s closed", runtime.ErrConflict, call.SessionID)
	}
	if call.ID == "" {
		r.nextCall++
		call.ID = fmt.Sprintf("call-%d", r.nextCall)
	}
	r.mu.Unlock()

	tool, ok := r.registry.Get(call.Name)
	if !ok {
		return runtime.ToolResult{}, fmt.Errorf("%w: tool %s", runtime.ErrNotFound, call.Name)
	}

	r.mu.Lock()
	if sess.closed {
		r.mu.Unlock()
		return runtime.ToolResult{}, fmt.Errorf("%w: session %s closed", runtime.ErrConflict, call.SessionID)
	}
	r.recordLocked(sess, eventbus.TypeToolCallStarted, call.TurnID, map[string]any{
		"call_id": call.ID,
		"name":    call.Name,
		"args":    string(call.Arguments),
	})
	r.mu.Unlock()

	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	tc := &tools.ToolContext{
		WorkingDir:     r.opts.WorkingDir,
		AgentID:        r.opts.AgentID,
		ToolUseID:      call.ID,
		MaxOutputChars: r.opts.MaxToolOutputChars,
		OutputDir:      r.opts.ToolOutputDir,
		Env:            r.opts.BashEnv,
	}
	res, err := tool.Call(ctx, args, tc)
	if err != nil {
		r.mu.Lock()
		if !sess.closed {
			r.recordLocked(sess, eventbus.TypeToolCallFinish, call.TurnID, map[string]any{
				"call_id":  call.ID,
				"name":     call.Name,
				"is_error": true,
				"output":   err.Error(),
			})
		}
		r.mu.Unlock()
		return runtime.ToolResult{}, err
	}
	out := runtime.ToolResult{
		CallID:  call.ID,
		Content: res.Flatten(),
		IsError: res.IsError,
	}
	r.mu.Lock()
	if sess.closed {
		r.mu.Unlock()
		return runtime.ToolResult{}, fmt.Errorf("%w: session %s closed", runtime.ErrConflict, call.SessionID)
	}
	r.recordLocked(sess, eventbus.TypeToolCallFinish, call.TurnID, map[string]any{
		"call_id":  call.ID,
		"name":     call.Name,
		"is_error": res.IsError,
	})
	r.mu.Unlock()
	return out, nil
}

// Plan is a degenerate single-intent implementation: askernel owns goal
// execution, not a separate planning topology.
func (r *Runtime) Plan(ctx context.Context, goal runtime.Goal, state runtime.State) ([]runtime.Intent, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return nil, err
	}
	if goal.Text == "" {
		return nil, fmt.Errorf("%w: goal 不能为空", runtime.ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if state.SessionID != "" {
		if _, ok := r.sessions[state.SessionID]; !ok {
			return nil, fmt.Errorf("%w: session %s", runtime.ErrNotFound, state.SessionID)
		}
	}
	r.nextIntent++
	intent := runtime.Intent{
		ID:     fmt.Sprintf("intent-%d", r.nextIntent),
		Kind:   "single",
		Goal:   goal.Text,
		Inputs: goal.Metadata,
	}
	if state.SessionID != "" {
		r.recordLocked(r.sessions[state.SessionID], eventbus.TypePlanUpdated, "", map[string]any{
			"intents": []map[string]any{{"id": intent.ID, "kind": intent.Kind, "goal": intent.Goal}},
		})
	}
	return []runtime.Intent{intent}, nil
}

// Snapshot captures the askernel DTO/usage plus the SPI turn/queue state. When
// the session has a transcript store, Restore also rewrites it for cross-process
// resume.
func (r *Runtime) Snapshot(ctx context.Context, sessionID runtime.SessionID) (runtime.State, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return runtime.State{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.sessions[sessionID]
	if !ok {
		return runtime.State{}, fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	if sess.closed {
		return runtime.State{}, fmt.Errorf("%w: session %s closed", runtime.ErrConflict, sessionID)
	}
	if sess.running {
		return runtime.State{}, fmt.Errorf("%w: session %s is running", runtime.ErrConflict, sessionID)
	}

	pending := make([]snapshotPending, 0, len(sess.pending))
	for _, p := range sess.pending {
		pending = append(pending, snapshotPending{ID: p.id, Content: p.content})
	}
	lastSeq := int64(0)
	for _, e := range sess.events {
		if e.Seq > lastSeq {
			lastSeq = e.Seq
		}
	}
	data, err := json.Marshal(snapshotData{
		Turns:    append([]runtime.Turn(nil), sess.turns...),
		Pending:  pending,
		Messages: cloneMessages(sess.s.Messages()),
		Usage:    sess.s.Usage(),
	})
	if err != nil {
		return runtime.State{}, err
	}
	return runtime.State{
		SessionID: sessionID,
		Version:   1,
		Seq:       lastSeq,
		Data:      data,
	}, nil
}

// Restore replaces the kernel DTO/usage and the SPI turns/pending. It refuses
// while a Prompt run is active.
func (r *Runtime) Restore(ctx context.Context, sessionID runtime.SessionID, state runtime.State) error {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return err
	}
	if state.Version != 1 {
		return fmt.Errorf("%w: state version %d", runtime.ErrInvalid, state.Version)
	}
	if state.SessionID != "" && state.SessionID != sessionID {
		return fmt.Errorf("%w: state session %s != %s", runtime.ErrInvalid, state.SessionID, sessionID)
	}
	if len(state.Data) == 0 {
		return fmt.Errorf("%w: state data 不能为空", runtime.ErrInvalid)
	}
	var data snapshotData
	if err := json.Unmarshal(state.Data, &data); err != nil {
		return fmt.Errorf("%w: state data: %v", runtime.ErrInvalid, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: session %s", runtime.ErrNotFound, sessionID)
	}
	if sess.closed {
		return fmt.Errorf("%w: session %s closed", runtime.ErrConflict, sessionID)
	}
	if sess.running {
		return fmt.Errorf("%w: session %s is running", runtime.ErrConflict, sessionID)
	}
	if err := sess.s.Restore(data.Messages, data.Usage); err != nil {
		return err
	}
	sess.turns = append([]runtime.Turn(nil), data.Turns...)
	sess.pending = make([]pendingTurn, 0, len(data.Pending))
	for _, p := range data.Pending {
		sess.pending = append(sess.pending, pendingTurn{id: p.ID, content: p.Content})
	}
	return nil
}

// Health declares the implemented event set. Snapshot is backed by the askernel
// DTO plus the adapter's turn/queue state.
func (r *Runtime) Health(ctx context.Context) (runtime.Capabilities, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return runtime.Capabilities{}, err
	}
	return runtime.Capabilities{
		Streaming:       true,
		ParallelWorkers: false,
		Snapshot:        true,
		EventTypes: []eventbus.Type{
			eventbus.TypeSessionCreated,
			eventbus.TypeTurnCreated,
			eventbus.TypeTokenDelta,
			eventbus.TypeToolCallStarted,
			eventbus.TypeToolCallFinish,
			eventbus.TypePlanUpdated,
			eventbus.TypeError,
		},
	}, nil
}

// Close releases every live askernel session (background task managers and
// their temporary directories). It is not part of runtime.Runtime; the host
// that owns the adapter should call it during shutdown.
func (r *Runtime) Close() error {
	r.mu.Lock()
	sessions := make([]*session, 0, len(r.sessions))
	for id, s := range r.sessions {
		if !s.closed {
			s.closed = true
			sessions = append(sessions, s)
		}
		delete(r.sessions, id)
	}
	r.mu.Unlock()

	var first error
	for _, s := range sessions {
		if err := s.s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (r *Runtime) newTurnID() runtime.TurnID {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextTurn++
	return runtime.TurnID(fmt.Sprintf("turn-%d", r.nextTurn))
}

func (r *Runtime) record(sess *session, typ eventbus.Type, turnID runtime.TurnID, payload map[string]any) runtime.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recordLocked(sess, typ, turnID, payload)
}

// recordForSession records an event only while the session is still live.
func (r *Runtime) recordForSession(sessionID runtime.SessionID, typ eventbus.Type, turnID runtime.TurnID, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.sessions[sessionID]
	if !ok || sess.closed {
		return
	}
	r.recordLocked(sess, typ, turnID, payload)
}

// recordLocked appends an event and returns it. Runtime.mu must be held.
func (r *Runtime) recordLocked(sess *session, typ eventbus.Type, turnID runtime.TurnID, payload map[string]any) runtime.Event {
	r.nextEvent++
	e := runtime.Event{
		ID:        fmt.Sprintf("evt-%d", r.nextEvent),
		Type:      typ,
		TS:        time.Now().UTC(),
		Seq:       r.nextEvent,
		SessionID: string(sess.id),
		TurnID:    string(turnID),
		Delivery:  eventbus.AtLeastOnce,
		Payload:   payload,
	}
	sess.events = append(sess.events, e)
	return e
}

func blocksText(blocks []acllm.ContentBlock) string {
	var b []byte
	for _, block := range blocks {
		if block.Type != acllm.BlockText {
			continue
		}
		if len(b) > 0 {
			b = append(b, '\n')
		}
		b = append(b, block.Text...)
	}
	return string(b)
}

func cloneMessages(in []acllm.Message) []acllm.Message {
	out := make([]acllm.Message, len(in))
	for i, m := range in {
		out[i] = m
		out[i].Content = append([]acllm.ContentBlock(nil), m.Content...)
		for j := range out[i].Content {
			out[i].Content[j].Content = append([]acllm.ContentBlock(nil), out[i].Content[j].Content...)
		}
	}
	return out
}
