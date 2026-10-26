// Package runtimeadapter maps runtime-spi onto the findu application runtime:
// agent.ChatAgent (the same runner used by the conversation page). Unlike the
// low-level askernel adapter, this adapter preserves findu's prompt assembly,
// skill/MCP tool augmentation, transcript/resume and guard wiring.
package runtimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"finduai/agent"
	"finduai/core/eventbus"
	coremodel "finduai/core/model"
	"finduai/core/runtime"
	"finduai/internal/spimodel"
	"finduai/tools"
	"finduai/transcript"
)

// Config is the assembly-layer configuration for one findu application runtime.
// Agent and AgentKey identify the production ChatAgent; the remaining fields
// tune a session's step budget and direct tool calls.
type Config struct {
	Agent       *agent.ChatAgent
	AgentKey    string
	MaxTurns    int
	MaxDuration time.Duration
	WebSearch   bool

	// Tools is the base toolset for direct CallTool calls. Nil means
	// tools.DefaultTools plus agent.AugmentTools(ctx, agentKey, ...), matching
	// ChatAgent's own base assembly.
	Tools []tools.CoreTool

	// Direct tool-call context. WorkingDir defaults to Agent.WorkDir().
	WorkingDir         string
	MaxToolOutputChars int
	OutputDir          string
	Env                []string

	// ActivitySink receives the original ChatAgent activities (the same stream
	// the existing conversation route persists). Optional.
	ActivitySink func(coremodel.Activity)
	// ResultSink receives the final Chat result once per queued user turn.
	// Optional; it is the adapter-level bridge for callers that need the
	// ChatAgent return value while consuming StreamEvents.
	ResultSink func(text string, err error)
}

// Runtime is the findu application-runtime implementation of runtime.Runtime.
type Runtime struct {
	cfg Config

	mu          sync.Mutex
	sessions    map[runtime.SessionID]*session
	nextSession int64
	nextTurn    int64
	nextCall    int64
	nextIntent  int64
	nextEvent   int64
}

type session struct {
	id       runtime.SessionID
	spec     runtime.SessionSpec
	agentKey string

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
	AgentKey string              `json:"agent_key"`
	Turns    []runtime.Turn      `json:"turns,omitempty"`
	Pending  []snapshotPending   `json:"pending,omitempty"`
	Records  []transcript.Record `json:"records,omitempty"`
}

type snapshotPending struct {
	ID      runtime.TurnID `json:"id"`
	Content string         `json:"content"`
}

var (
	_ runtime.Runtime     = (*Runtime)(nil)
	_ runtime.Snapshotter = (*Runtime)(nil)
)

// New validates the ChatAgent configuration. The agent must be built by the
// normal server assembly; this package only adapts it to the SPI.
func New(cfg Config) (*Runtime, error) {
	if cfg.Agent == nil {
		return nil, fmt.Errorf("%w: findu adapter requires Config.Agent", runtime.ErrInvalid)
	}
	if cfg.AgentKey == "" {
		return nil, fmt.Errorf("%w: findu adapter requires Config.AgentKey", runtime.ErrInvalid)
	}
	if cfg.Agent.Provider() == nil {
		return nil, fmt.Errorf("%w: findu adapter requires a ChatAgent provider", runtime.ErrInvalid)
	}
	if cfg.Agent.Transcript() == nil {
		return nil, fmt.Errorf("%w: findu adapter requires a ChatAgent transcript store", runtime.ErrInvalid)
	}
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = cfg.Agent.WorkDir()
	}
	return &Runtime{
		cfg:      cfg,
		sessions: map[runtime.SessionID]*session{},
	}, nil
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

// CreateSession creates the SPI-side session record. ChatAgent.Chat creates the
// kernel session lazily on the first StreamEvents call, so this method only
// allocates the stable id + event history.
func (r *Runtime) CreateSession(ctx context.Context, spec runtime.SessionSpec) (runtime.SessionID, error) {
	ctx = nonNilCtx(ctx)
	if err := errCtx(ctx); err != nil {
		return "", err
	}
	agentKey := r.cfg.AgentKey
	if v, ok := spec.Options["agent_key"].(string); ok && v != "" {
		agentKey = v
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSession++
	// The id also names ChatAgent's transcript/workdir; include a timestamp so a
	// server restart never resumes an unrelated prior session. A caller can pin
	// an existing id (e.g. restore/reattach), but never a path-shaped one.
	id := runtime.SessionID(fmt.Sprintf("sess-%d-%d", time.Now().UnixNano(), r.nextSession))
	if v, ok := spec.Options["session_id"].(string); ok && v != "" {
		if strings.ContainsAny(v, `/\`) || v == "." || v == ".." {
			return "", fmt.Errorf("%w: invalid session_id %q", runtime.ErrInvalid, v)
		}
		id = runtime.SessionID(v)
	}
	if _, exists := r.sessions[id]; exists {
		return "", fmt.Errorf("%w: session %s", runtime.ErrConflict, id)
	}
	sess := &session{id: id, spec: spec, agentKey: agentKey}
	r.sessions[id] = sess
	r.recordLocked(sess, eventbus.TypeSessionCreated, "", map[string]any{
		"title":      spec.Title,
		"project_id": spec.ProjectID,
		"agent_key":  agentKey,
	})
	return id, nil
}

// AppendTurn records a turn. User turns are queued for the next StreamEvents
// call; other roles are recorded only (ChatAgent owns its transcript/DTO).
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

// StreamEvents replays events after fromSeq and runs queued user turns through
// ChatAgent.Chat. model.Activity is the native event source; no askernel
// RunEvent is involved on this path.
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
		for _, p := range pending {
			if !r.runChat(ctx, sess, p, ch) {
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

func (r *Runtime) runChat(ctx context.Context, sess *session, p pendingTurn, ch chan<- runtime.Event) bool {
	var (
		sawError         bool
		emittedAssistant bool
	)
	emit := func(a coremodel.Activity) {
		if r.cfg.ActivitySink != nil {
			r.cfg.ActivitySink(a)
		}
		if ctx.Err() != nil {
			return
		}
		text := a.Detail
		if text == "" {
			text = a.Summary
		}
		switch a.Kind {
		case "text":
			if text == "" {
				return
			}
			send(ctx, ch, r.record(sess, eventbus.TypeTokenDelta, p.id, map[string]any{
				"kind": "text",
				"text": text,
			}))
		case "thinking":
			if text == "" {
				return
			}
			send(ctx, ch, r.record(sess, eventbus.TypeTokenDelta, p.id, map[string]any{
				"kind": "thinking",
				"text": text,
			}))
		case "tool_use":
			send(ctx, ch, r.record(sess, eventbus.TypeToolCallStarted, p.id, map[string]any{
				"call_id": a.ToolUseID,
				"name":    a.Tool,
				"args":    text,
			}))
		case "tool_result":
			send(ctx, ch, r.record(sess, eventbus.TypeToolCallFinish, p.id, map[string]any{
				"call_id":  a.ToolUseID,
				"name":     a.Tool,
				"is_error": a.IsError,
				"output":   text,
			}))
		case "result":
			if a.IsError {
				sawError = true
				if text == "" {
					text = "native runtime error"
				}
				send(ctx, ch, r.record(sess, eventbus.TypeError, p.id, map[string]any{
					"message":   text,
					"retryable": false,
				}))
				return
			}
			if text != "" {
				emittedAssistant = true
				send(ctx, ch, r.record(sess, eventbus.TypeTurnCreated, r.newTurnID(), map[string]any{
					"role":    string(runtime.RoleAssistant),
					"content": text,
				}))
			}
		}
	}

	text, err := r.cfg.Agent.Chat(ctx, sess.agentKey, string(sess.id), p.content, r.cfg.MaxTurns, r.cfg.MaxDuration, r.cfg.WebSearch, emit)
	if r.cfg.ResultSink != nil {
		r.cfg.ResultSink(text, err)
	}
	if ctx.Err() != nil {
		return false
	}
	if err != nil && !sawError {
		send(ctx, ch, r.record(sess, eventbus.TypeError, p.id, map[string]any{
			"message":   err.Error(),
			"retryable": false,
		}))
	}
	if text != "" && !emittedAssistant {
		send(ctx, ch, r.record(sess, eventbus.TypeTurnCreated, r.newTurnID(), map[string]any{
			"role":    string(runtime.RoleAssistant),
			"content": text,
		}))
	}
	return true
}

// InvokeModel is a direct model call using the same provider ChatAgent uses. It
// is stateless and does not mutate the ChatAgent transcript.
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

	resp, err := spimodel.InvokeModel(ctx, r.cfg.Agent.Provider(), req)
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

// CallTool invokes one CoreTool from the findu tool assembly. With Config.Tools
// nil it uses DefaultTools + agent.AugmentTools (skills/MCP); otherwise the
// supplied base set is used verbatim.
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

	base := r.cfg.Tools
	cleanup := func() {}
	if base == nil {
		base = tools.DefaultTools()
		ctx = agent.WithRunInfo(ctx, agent.RunInfo{SessionID: string(call.SessionID)})
		base, _, cleanup = agent.AugmentTools(ctx, sess.agentKey, base)
	}
	defer cleanup()
	tool, ok := tools.NewRegistry(base...).Get(call.Name)
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

	sessionDir := filepath.Join(r.cfg.WorkingDir, "sessions", string(call.SessionID))
	_ = os.MkdirAll(sessionDir, 0o755)
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	outputDir := r.cfg.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(sessionDir, "cmd-output")
	}
	res, err := tool.Call(ctx, args, &tools.ToolContext{
		WorkingDir:     sessionDir,
		AgentID:        sess.agentKey,
		ToolUseID:      call.ID,
		MaxOutputChars: r.cfg.MaxToolOutputChars,
		OutputDir:      outputDir,
		Env:            r.cfg.Env,
	})
	if err != nil {
		r.recordForSession(call.SessionID, eventbus.TypeToolCallFinish, call.TurnID, map[string]any{
			"call_id":  call.ID,
			"name":     call.Name,
			"is_error": true,
			"output":   err.Error(),
		})
		return runtime.ToolResult{}, err
	}
	out := runtime.ToolResult{
		CallID:  call.ID,
		Content: res.Flatten(),
		IsError: res.IsError,
	}
	r.recordForSession(call.SessionID, eventbus.TypeToolCallFinish, call.TurnID, map[string]any{
		"call_id":  call.ID,
		"name":     call.Name,
		"is_error": res.IsError,
		"output":   out.Content,
	})
	return out, nil
}

// Plan is the native runtime's degenerate single-intent implementation:
// ChatAgent has no separate planning topology.
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

// Snapshot captures the SPI turn/queue state plus the ChatAgent transcript. The
// pair is what Restore needs: the transcript seeds ChatAgent.Resume on the next
// turn, while turns/pending keep the SPI view consistent.
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
	tx := r.cfg.Agent.Transcript()
	if tx == nil {
		return runtime.State{}, fmt.Errorf("%w: transcript store is nil", runtime.ErrUnsupported)
	}
	recs, err := tx.Load(tx.MainPath(string(sessionID)))
	if err != nil {
		return runtime.State{}, err
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
		AgentKey: sess.agentKey,
		Turns:    append([]runtime.Turn(nil), sess.turns...),
		Pending:  pending,
		Records:  recs,
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

// Restore atomically replaces the transcript file and restores the SPI turns and
// pending queue. It refuses while a Chat run is active.
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
	if data.AgentKey != "" && data.AgentKey != sess.agentKey {
		return fmt.Errorf("%w: state agent_key %s != %s", runtime.ErrInvalid, data.AgentKey, sess.agentKey)
	}
	tx := r.cfg.Agent.Transcript()
	if tx == nil {
		return fmt.Errorf("%w: transcript store is nil", runtime.ErrUnsupported)
	}
	recs := append([]transcript.Record(nil), data.Records...)
	for i := range recs {
		recs[i].SessionID = string(sessionID)
	}
	if err := tx.Replace(tx.MainPath(string(sessionID)), recs); err != nil {
		return err
	}
	sess.turns = append([]runtime.Turn(nil), data.Turns...)
	sess.pending = make([]pendingTurn, 0, len(data.Pending))
	for _, p := range data.Pending {
		sess.pending = append(sess.pending, pendingTurn{id: p.ID, content: p.Content})
	}
	return nil
}

// Health declares the implemented event set. Snapshot is backed by the
// ChatAgent transcript (see Snapshot/Restore).
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

// Close marks all sessions closed. ChatAgent owns the kernel session lifecycle
// per Chat call, so there is no long-lived kernel object to release here.
func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		s.closed = true
		delete(r.sessions, id)
	}
	return nil
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

func (r *Runtime) recordForSession(sessionID runtime.SessionID, typ eventbus.Type, turnID runtime.TurnID, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.sessions[sessionID]
	if !ok || sess.closed {
		return
	}
	r.recordLocked(sess, typ, turnID, payload)
}

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
