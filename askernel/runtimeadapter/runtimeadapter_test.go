package runtimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"finduai/askernel"
	"finduai/core/eventbus"
	"finduai/core/runtime"
	"finduai/tools"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel is a scripted eino model: each Generate/Stream call consumes the
// next scripted message. It records the messages and tool bindings seen.
type fakeModel struct {
	mu       sync.Mutex
	script   []*schema.Message
	err      error
	usage    *schema.TokenUsage
	calls    int
	inputs   [][]*schema.Message
	bound    [][]*schema.ToolInfo
	lastOpts []model.Option
}

func (f *fakeModel) next() (*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls
	f.calls++
	if i < len(f.script) {
		return f.script[i], nil
	}
	return schema.AssistantMessage("done", nil), nil
}

func (f *fakeModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.lastOpts = opts
	common := model.GetCommonOptions(&model.Options{}, opts...)
	f.bound = append(f.bound, common.Tools)
	f.mu.Unlock()
	msg, err := f.next()
	if err != nil {
		return nil, err
	}
	if f.usage != nil {
		cp := *msg
		cp.ResponseMeta = &schema.ResponseMeta{FinishReason: "tool_calls", Usage: f.usage}
		return &cp, nil
	}
	return msg, nil
}

func (f *fakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.lastOpts = opts
	common := model.GetCommonOptions(&model.Options{}, opts...)
	f.bound = append(f.bound, common.Tools)
	f.mu.Unlock()
	msg, err := f.next()
	if err != nil {
		return nil, err
	}
	cp := *msg
	if f.usage != nil {
		cp.ResponseMeta = &schema.ResponseMeta{Usage: f.usage}
	}
	return schema.StreamReaderFromArray([]*schema.Message{&cp}), nil
}

func (f *fakeModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	f.mu.Lock()
	f.bound = append(f.bound, infos)
	f.mu.Unlock()
	return f, nil
}

func (f *fakeModel) lastInput() []*schema.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) == 0 {
		return nil
	}
	return f.inputs[len(f.inputs)-1]
}

func echoTool(ran *int) tools.CoreTool {
	return tools.Build(tools.Spec{
		Name:        "echo",
		Description: "echo the input",
		Schema:      map[string]any{"type": "object"},
		Run: func(ctx context.Context, in json.RawMessage, tc *tools.ToolContext) (tools.Result, error) {
			if ran != nil {
				*ran = *ran + 1
			}
			return tools.Text("echo ok"), nil
		},
	})
}

func TestAdapterAgentLoopAndReplay(t *testing.T) {
	scripted := schema.AssistantMessage("hello", nil)
	scripted.ReasoningContent = "thinking text"
	model := &fakeModel{script: []*schema.Message{scripted}}
	rt, err := New(askernel.Options{Model: model, SystemPrompt: []string{"system"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	caps, err := rt.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !caps.Streaming || !caps.Snapshot || caps.ParallelWorkers {
		t.Fatalf("capabilities = %+v", caps)
	}
	for _, typ := range caps.EventTypes {
		if !typ.Valid() {
			t.Fatalf("invalid event type %q", typ)
		}
	}
	if _, ok := any(rt).(runtime.Snapshotter); !ok {
		t.Fatal("adapter must implement Snapshotter")
	}

	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{
		ProjectID: "p1",
		Title:     "adapter test",
	})
	if err != nil || sid == "" {
		t.Fatalf("CreateSession=%q err=%v", sid, err)
	}
	tid, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "hi"})
	if err != nil || tid == "" {
		t.Fatalf("AppendTurn=%q err=%v", tid, err)
	}

	events := collect(t, ctx, rt, sid, 0)
	if len(events) < 4 {
		t.Fatalf("events = %+v", events)
	}
	seen := map[eventbus.Type]int{}
	var assistantText string
	tokenKinds := map[string]bool{}
	for _, e := range events {
		if err := e.Validate(); err != nil {
			t.Fatalf("invalid event: %+v err=%v", e, err)
		}
		if e.SessionID != string(sid) {
			t.Fatalf("event session = %q, want %q", e.SessionID, sid)
		}
		seen[e.Type]++
		if e.Type == eventbus.TypeTokenDelta {
			kind, _ := e.Payload["kind"].(string)
			tokenKinds[kind] = true
		}
		if e.Type == eventbus.TypeTurnCreated {
			if role, _ := e.Payload["role"].(string); role == string(runtime.RoleAssistant) {
				assistantText, _ = e.Payload["content"].(string)
			}
		}
	}
	for _, want := range []eventbus.Type{
		eventbus.TypeSessionCreated,
		eventbus.TypeTurnCreated,
		eventbus.TypeTokenDelta,
	} {
		if seen[want] == 0 {
			t.Fatalf("missing %s in %+v", want, events)
		}
	}
	if assistantText != "hello" {
		t.Fatalf("assistant turn = %q, want hello", assistantText)
	}
	if !tokenKinds["text"] || !tokenKinds["thinking"] {
		t.Fatalf("token kinds = %+v, want text + thinking", tokenKinds)
	}

	last := events[len(events)-1]
	replay := collect(t, ctx, rt, sid, last.Seq-1)
	if len(replay) != 1 || replay[0].Seq != last.Seq {
		t.Fatalf("replay = %+v, last seq = %d", replay, last.Seq)
	}
	if got := collect(t, ctx, rt, sid, last.Seq); len(got) != 0 {
		t.Fatalf("from_seq at tail returned %+v", got)
	}
}

func TestAdapterToolCallInLoop(t *testing.T) {
	ran := 0
	echo := echoTool(&ran)
	model := &fakeModel{script: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "call-1",
			Function: schema.FunctionCall{Name: "echo", Arguments: `{"x":1}`},
		}}),
		schema.AssistantMessage("all done", nil),
	}}
	rt, err := New(askernel.Options{Model: model, Tools: []tools.CoreTool{echo}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Title: "tool loop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "use echo"}); err != nil {
		t.Fatal(err)
	}

	events := collect(t, ctx, rt, sid, 0)
	if ran != 1 {
		t.Fatalf("echo ran %d times, want 1", ran)
	}
	var started, finished bool
	var final string
	for _, e := range events {
		switch e.Type {
		case eventbus.TypeToolCallStarted:
			started = true
			if e.Payload["name"] != "echo" {
				t.Fatalf("tool started payload = %+v", e.Payload)
			}
		case eventbus.TypeToolCallFinish:
			finished = true
			if e.Payload["output"] != "echo ok" {
				t.Fatalf("tool finished payload = %+v", e.Payload)
			}
		case eventbus.TypeTurnCreated:
			if role, _ := e.Payload["role"].(string); role == string(runtime.RoleAssistant) {
				final, _ = e.Payload["content"].(string)
			}
		}
	}
	if !started || !finished {
		t.Fatalf("tool events missing: %+v", events)
	}
	if final != "all done" {
		t.Fatalf("final assistant = %q, want all done", final)
	}
}

func TestAdapterInvokeModelCallToolPlan(t *testing.T) {
	ran := 0
	echo := echoTool(&ran)
	model := &fakeModel{usage: &schema.TokenUsage{
		PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7,
	}}
	model.script = []*schema.Message{schema.AssistantMessage("assistant text", []schema.ToolCall{{
		ID:       "tc-1",
		Function: schema.FunctionCall{Name: "echo", Arguments: `{"a":true}`},
	}})}
	rt, err := New(askernel.Options{Model: model, Tools: []tools.CoreTool{echo}, WorkingDir: "."})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Title: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.InvokeModel(ctx, runtime.ModelRequest{
		SessionID: sid,
		Model:     "test-model",
		Messages: []runtime.Message{
			{Role: runtime.RoleSystem, Content: "system"},
			{Role: runtime.RoleUser, Content: "hello"},
		},
		Tools: []runtime.ToolSpec{{
			Name:        "echo",
			Description: "echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		Temperature: 0.2,
		MaxTokens:   128,
	})
	if err != nil {
		t.Fatalf("InvokeModel: %v", err)
	}
	if resp.Message.Role != runtime.RoleAssistant || resp.Message.Content != "assistant text" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Usage.TotalTokens != 7 || resp.Usage.PromptTokens != 3 || resp.Usage.CompletionTokens != 4 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "echo" ||
		string(resp.ToolCalls[0].Arguments) != `{"a":true}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}

	tr, err := rt.CallTool(ctx, runtime.ToolCall{
		SessionID: sid,
		Name:      "echo",
		Arguments: json.RawMessage(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tr.CallID == "" || tr.Content != "echo ok" || tr.IsError || ran != 1 {
		t.Fatalf("tool result = %+v ran=%d", tr, ran)
	}

	intents, err := rt.Plan(ctx, runtime.Goal{Text: "goal", Metadata: map[string]any{"k": "v"}}, runtime.State{SessionID: sid})
	if err != nil || len(intents) != 1 || intents[0].Goal != "goal" || intents[0].Kind != "single" {
		t.Fatalf("Plan = %+v err=%v", intents, err)
	}

	events := collect(t, ctx, rt, sid, 0)
	types := map[eventbus.Type]bool{}
	for _, e := range events {
		types[e.Type] = true
	}
	if !types[eventbus.TypeToolCallStarted] || !types[eventbus.TypeToolCallFinish] || !types[eventbus.TypePlanUpdated] {
		t.Fatalf("events = %+v", types)
	}
}

func TestAdapterStreamModelError(t *testing.T) {
	model := &fakeModel{err: io.ErrUnexpectedEOF}
	rt, err := New(askernel.Options{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Title: "model error"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "fail"}); err != nil {
		t.Fatal(err)
	}
	events := collect(t, ctx, rt, sid, 0)
	var sawError bool
	for _, e := range events {
		if e.Type == eventbus.TypeError {
			sawError = true
			if e.Payload["message"] == "" {
				t.Fatalf("error event payload = %+v", e.Payload)
			}
		}
	}
	if !sawError {
		t.Fatalf("missing error event: %+v", events)
	}
}

func TestAdapterSnapshotRestore(t *testing.T) {
	f1 := &fakeModel{script: []*schema.Message{schema.AssistantMessage("first answer", nil)}}
	rt1, err := New(askernel.Options{Model: f1})
	if err != nil {
		t.Fatal(err)
	}
	defer rt1.Close()
	ctx := context.Background()
	sid, err := rt1.CreateSession(ctx, runtime.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt1.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "first question"}); err != nil {
		t.Fatal(err)
	}
	collect(t, ctx, rt1, sid, 0)
	state, err := rt1.Snapshot(ctx, sid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if state.SessionID != sid || state.Version != 1 || state.Seq == 0 || len(state.Data) == 0 {
		t.Fatalf("state = %+v", state)
	}

	f2 := &fakeModel{script: []*schema.Message{schema.AssistantMessage("second answer", nil)}}
	rt2, err := New(askernel.Options{Model: f2})
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.Close()
	sid2, err := rt2.CreateSession(ctx, runtime.SessionSpec{Options: map[string]any{"session_id": string(sid)}})
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if sid2 != sid {
		t.Fatalf("reattached id = %q, want %q", sid2, sid)
	}
	if err := rt2.Restore(ctx, sid2, state); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := rt2.AppendTurn(ctx, sid2, runtime.Turn{Role: runtime.RoleUser, Content: "second question"}); err != nil {
		t.Fatal(err)
	}
	collect(t, ctx, rt2, sid2, 0)
	if !hasMessage(f2.lastInput(), "first question") {
		t.Fatal("restored DTO does not contain the prior user turn")
	}
	if !hasMessage(f2.lastInput(), "first answer") {
		t.Fatal("restored DTO does not contain the prior assistant turn")
	}

	if _, err := rt1.Snapshot(ctx, "missing"); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing Snapshot err=%v", err)
	}
	bad := state
	bad.SessionID = "other"
	if err := rt2.Restore(ctx, sid2, bad); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("mismatched Restore err=%v", err)
	}
	if _, err := rt2.CreateSession(ctx, runtime.SessionSpec{Options: map[string]any{"session_id": "../escape"}}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("path session id err=%v", err)
	}
}

func hasMessage(msgs []*schema.Message, want string) bool {
	for _, m := range msgs {
		if m != nil && m.Content == want {
			return true
		}
	}
	return false
}

func TestAdapterErrors(t *testing.T) {
	if _, err := New(askernel.Options{}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("New without model err=%v", err)
	}

	model := &fakeModel{}
	rt, err := New(askernel.Options{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.AppendTurn(ctx, "missing", runtime.Turn{Role: runtime.RoleUser, Content: "x"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session AppendTurn err=%v", err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: "robot", Content: "x"}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("bad role err=%v", err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty content err=%v", err)
	}
	if _, err := rt.StreamEvents(ctx, sid, -1); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("negative seq err=%v", err)
	}
	if _, err := rt.StreamEvents(ctx, "missing", 0); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session stream err=%v", err)
	}
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: sid}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty messages err=%v", err)
	}
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: "missing", Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "x"}}}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session model err=%v", err)
	}
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: sid, Messages: []runtime.Message{{Role: "robot", Content: "x"}}}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("bad message role err=%v", err)
	}
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: sid, Messages: []runtime.Message{{Role: runtime.RoleTool, Content: "x"}}}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("tool message without call id err=%v", err)
	}
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{
		SessionID: sid,
		Messages:  []runtime.Message{{Role: runtime.RoleUser, Content: "x"}},
		Tools:     []runtime.ToolSpec{{Name: "x", InputSchema: json.RawMessage(`not-json`)}},
	}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("bad tool schema err=%v", err)
	}
	if _, err := rt.CallTool(ctx, runtime.ToolCall{SessionID: sid}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty tool err=%v", err)
	}
	if _, err := rt.CallTool(ctx, runtime.ToolCall{SessionID: sid, Name: "missing"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("unknown tool err=%v", err)
	}
	if _, err := rt.Plan(ctx, runtime.Goal{}, runtime.State{}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty goal err=%v", err)
	}
	if _, err := rt.Plan(ctx, runtime.Goal{Text: "x"}, runtime.State{SessionID: "missing"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session plan err=%v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rt.CreateSession(canceled, runtime.SessionSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CreateSession err=%v", err)
	}
	if _, err := rt.Health(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Health err=%v", err)
	}

	model.err = io.ErrUnexpectedEOF
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{
		SessionID: sid,
		Messages:  []runtime.Message{{Role: runtime.RoleUser, Content: "x"}},
	}); !runtime.IsRetryable(err) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("model failure err=%v", err)
	}
}

func TestAdapterCloseRejectsNewTurns(t *testing.T) {
	model := &fakeModel{}
	rt, err := New(askernel.Options{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "x"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("after Close AppendTurn err=%v", err)
	}
}

func collect(t *testing.T, ctx context.Context, rt runtime.Runtime, sid runtime.SessionID, fromSeq int64) []runtime.Event {
	t.Helper()
	ch, err := rt.StreamEvents(ctx, sid, fromSeq)
	if err != nil {
		t.Fatalf("StreamEvents(%d): %v", fromSeq, err)
	}
	var out []runtime.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			t.Fatal("StreamEvents timed out")
		}
	}
}
