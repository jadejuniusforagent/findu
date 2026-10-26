package runtimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"finduai/agent"
	"finduai/core/eventbus"
	"finduai/core/runtime"
	"finduai/tools"
	"finduai/transcript"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeModel struct {
	mu     sync.Mutex
	script []*schema.Message
	err    error
	usage  *schema.TokenUsage
	calls  int
	bound  [][]*schema.ToolInfo
	inputs [][]*schema.Message
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
	f.bound = append(f.bound, model.GetCommonOptions(&model.Options{}, opts...).Tools)
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

func (f *fakeModel) reset(script ...*schema.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = script
	f.err = nil
	f.calls = 0
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
		Description: "echo",
		Schema:      map[string]any{"type": "object"},
		Run: func(ctx context.Context, in json.RawMessage, tc *tools.ToolContext) (tools.Result, error) {
			if ran != nil {
				*ran = *ran + 1
			}
			return tools.Text("native echo"), nil
		},
	})
}

func newAgent(t *testing.T, f *fakeModel) *agent.ChatAgent {
	t.Helper()
	return agent.NewChatAgent(f, "fake-model", t.TempDir(), transcript.NewStore(t.TempDir()), 100000)
}

func TestNativeAdapterAgentLoopAndReplay(t *testing.T) {
	f := &fakeModel{}
	f.reset(schema.AssistantMessage("native hello", nil))
	ca := newAgent(t, f)
	rt, err := New(Config{Agent: ca, AgentKey: "native-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	ctx := context.Background()
	caps, err := rt.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Streaming || !caps.Snapshot || caps.ParallelWorkers {
		t.Fatalf("capabilities = %+v", caps)
	}
	if _, ok := any(rt).(runtime.Snapshotter); !ok {
		t.Fatal("native adapter must implement Snapshotter")
	}

	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Title: "native"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "hi"}); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	events := collect(t, ctx, rt, sid, 0)
	seen := map[eventbus.Type]int{}
	var assistant string
	for _, e := range events {
		if err := e.Validate(); err != nil {
			t.Fatalf("event invalid: %+v err=%v", e, err)
		}
		if e.SessionID != string(sid) {
			t.Fatalf("event session = %q, want %q", e.SessionID, sid)
		}
		seen[e.Type]++
		if e.Type == eventbus.TypeTurnCreated {
			if role, _ := e.Payload["role"].(string); role == string(runtime.RoleAssistant) {
				assistant, _ = e.Payload["content"].(string)
			}
		}
	}
	for _, want := range []eventbus.Type{eventbus.TypeSessionCreated, eventbus.TypeTurnCreated} {
		if seen[want] == 0 {
			t.Fatalf("missing %s: %+v", want, events)
		}
	}
	if assistant != "native hello" {
		t.Fatalf("assistant = %q", assistant)
	}

	last := events[len(events)-1]
	replay := collect(t, ctx, rt, sid, last.Seq-1)
	if len(replay) != 1 || replay[0].Seq != last.Seq {
		t.Fatalf("replay = %+v last=%d", replay, last.Seq)
	}
	if got := collect(t, ctx, rt, sid, last.Seq); len(got) != 0 {
		t.Fatalf("tail replay = %+v", got)
	}
}

func TestNativeAdapterDirectModelToolPlan(t *testing.T) {
	ran := 0
	f := &fakeModel{usage: &schema.TokenUsage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}}
	f.reset(schema.AssistantMessage("direct", []schema.ToolCall{{
		ID:       "tc-1",
		Function: schema.FunctionCall{Name: "echo", Arguments: `{"x":1}`},
	}}))
	ca := newAgent(t, f)
	echo := echoTool(&ran)
	rt, err := New(Config{Agent: ca, AgentKey: "native-test", Tools: []tools.CoreTool{echo}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.InvokeModel(ctx, runtime.ModelRequest{
		SessionID: sid,
		Messages: []runtime.Message{
			{Role: runtime.RoleSystem, Content: "system"},
			{Role: runtime.RoleUser, Content: "hi"},
		},
		Tools: []runtime.ToolSpec{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("InvokeModel: %v", err)
	}
	if resp.Message.Content != "direct" || resp.Usage.TotalTokens != 5 || len(resp.ToolCalls) != 1 {
		t.Fatalf("response = %+v", resp)
	}

	tr, err := rt.CallTool(ctx, runtime.ToolCall{
		SessionID: sid,
		Name:      "echo",
		Arguments: json.RawMessage(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tr.Content != "native echo" || tr.IsError || ran != 1 {
		t.Fatalf("tool result = %+v ran=%d", tr, ran)
	}

	intents, err := rt.Plan(ctx, runtime.Goal{Text: "goal"}, runtime.State{SessionID: sid})
	if err != nil || len(intents) != 1 || intents[0].Kind != "single" {
		t.Fatalf("Plan = %+v err=%v", intents, err)
	}

	events := collect(t, ctx, rt, sid, 0)
	types := map[eventbus.Type]bool{}
	for _, e := range events {
		types[e.Type] = true
	}
	for _, want := range []eventbus.Type{eventbus.TypeTokenDelta, eventbus.TypeToolCallStarted, eventbus.TypeToolCallFinish, eventbus.TypePlanUpdated} {
		if !types[want] {
			t.Fatalf("missing %s: %+v", want, types)
		}
	}
}

func TestNativeAdapterSnapshotRestore(t *testing.T) {
	f := &fakeModel{}
	f.reset(schema.AssistantMessage("first answer", nil))
	ca := newAgent(t, f)
	rt, err := New(Config{Agent: ca, AgentKey: "native-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := context.Background()

	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "first question"}); err != nil {
		t.Fatal(err)
	}
	collect(t, ctx, rt, sid, 0)

	state, err := rt.Snapshot(ctx, sid)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if state.SessionID != sid || state.Version != 1 || state.Seq == 0 || len(state.Data) == 0 {
		t.Fatalf("state = %+v", state)
	}

	// A fresh adapter can reattach to the same persisted session id and restore
	// the snapshot; ChatAgent.Resume must then feed the prior turn to the model.
	rt2, err := New(Config{Agent: ca, AgentKey: "native-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.Close()
	sid2, err := rt2.CreateSession(ctx, runtime.SessionSpec{Options: map[string]any{"session_id": string(sid)}})
	if err != nil {
		t.Fatalf("reattach session: %v", err)
	}
	if sid2 != sid {
		t.Fatalf("reattached id = %q, want %q", sid2, sid)
	}
	if err := rt2.Restore(ctx, sid2, state); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	f.reset(schema.AssistantMessage("second answer", nil))
	if _, err := rt2.AppendTurn(ctx, sid2, runtime.Turn{Role: runtime.RoleUser, Content: "second question"}); err != nil {
		t.Fatal(err)
	}
	collect(t, ctx, rt2, sid2, 0)
	if !hasMessage(f.lastInput(), "first question") {
		t.Fatal("restored ChatAgent input does not contain the prior user turn")
	}
	if !hasMessage(f.lastInput(), "first answer") {
		t.Fatal("restored ChatAgent input does not contain the prior assistant turn")
	}

	if _, err := rt.Snapshot(ctx, "missing"); !errors.Is(err, runtime.ErrNotFound) {
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

func TestNativeAdapterStreamError(t *testing.T) {
	f := &fakeModel{err: io.ErrUnexpectedEOF}
	ca := newAgent(t, f)
	rt, err := New(Config{Agent: ca, AgentKey: "native-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{})
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
		}
	}
	if !sawError {
		t.Fatalf("missing error event: %+v", events)
	}
}

func TestNativeAdapterCloseRejectsTurns(t *testing.T) {
	f := &fakeModel{}
	ca := newAgent(t, f)
	rt, err := New(Config{Agent: ca, AgentKey: "native-test"})
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

func TestNativeAdapterErrors(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("New empty err=%v", err)
	}
	f := &fakeModel{}
	ca := newAgent(t, f)
	if _, err := New(Config{Agent: ca}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("New without key err=%v", err)
	}
	noTx := agent.NewChatAgent(f, "fake-model", t.TempDir(), nil, 100000)
	if _, err := New(Config{Agent: noTx, AgentKey: "native-test"}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("New without transcript err=%v", err)
	}
	rt, err := New(Config{Agent: ca, AgentKey: "native-test"})
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
		t.Fatalf("canceled create err=%v", err)
	}
	if _, err := rt.Health(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled health err=%v", err)
	}
	f.err = io.ErrUnexpectedEOF
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: sid, Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "x"}}}); !runtime.IsRetryable(err) {
		t.Fatalf("model error err=%v", err)
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
