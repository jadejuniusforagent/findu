package runtime_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"finduai/core/eventbus"
	"finduai/core/runtime"
	"finduai/core/runtime/mock"
)

var (
	_ runtime.Runtime     = (*mock.Runtime)(nil)
	_ runtime.Snapshotter = (*mock.Runtime)(nil)
)

func TestRuntimeContract(t *testing.T) {
	ctx := context.Background()
	rt := mock.NewRuntime()

	caps, err := rt.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !caps.Streaming || !caps.Snapshot || !caps.ParallelWorkers {
		t.Fatalf("capabilities 不完整: %+v", caps)
	}
	if len(caps.EventTypes) == 0 {
		t.Fatal("EventTypes 不能为空")
	}
	for _, typ := range caps.EventTypes {
		if !typ.Valid() {
			t.Fatalf("Health 返回了契约外事件类型 %q", typ)
		}
	}
	for _, want := range []eventbus.Type{
		eventbus.TypeSessionCreated, eventbus.TypeTurnCreated, eventbus.TypeTokenDelta,
		eventbus.TypeToolCallStarted, eventbus.TypeToolCallFinish, eventbus.TypePlanUpdated,
	} {
		if !containsType(caps.EventTypes, want) {
			t.Fatalf("缺少事件能力 %s，实际 %v", want, caps.EventTypes)
		}
	}

	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{
		ProjectID: "project-1",
		Title:     "契约测试",
		Options:   map[string]any{"adapter": "mock"},
	})
	if err != nil || sid == "" {
		t.Fatalf("CreateSession=%q err=%v", sid, err)
	}

	tid, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "hello"})
	if err != nil || tid == "" {
		t.Fatalf("AppendTurn=%q err=%v", tid, err)
	}

	resp, err := rt.InvokeModel(ctx, runtime.ModelRequest{
		SessionID: sid,
		TurnID:    tid,
		Model:     "mock-model",
		Messages:  []runtime.Message{{Role: runtime.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("InvokeModel: %v", err)
	}
	if resp.Message.Role != runtime.RoleAssistant || resp.Message.Content == "" {
		t.Fatalf("InvokeModel response=%+v", resp)
	}
	if resp.Usage.TotalTokens <= 0 || resp.FinishReason == "" {
		t.Fatalf("InvokeModel usage/finish 缺失: %+v", resp)
	}

	call := runtime.ToolCall{
		SessionID: sid,
		TurnID:    tid,
		Name:      "http_request",
		Arguments: []byte(`{"url":"https://example.test"}`),
	}
	tr, err := rt.CallTool(ctx, call)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if tr.CallID == "" || tr.Content == "" || tr.IsError {
		t.Fatalf("CallTool result=%+v", tr)
	}

	intents, err := rt.Plan(ctx, runtime.Goal{Text: "验证目标"}, runtime.State{SessionID: sid})
	if err != nil || len(intents) != 1 {
		t.Fatalf("Plan=%+v err=%v", intents, err)
	}
	if intents[0].ID == "" || intents[0].Goal != "验证目标" {
		t.Fatalf("Plan intent=%+v", intents[0])
	}

	// 可选能力：实现 Snapshotter 时 Health 必须声明 Snapshot=true。
	snap, ok := any(rt).(runtime.Snapshotter)
	if !ok {
		t.Fatal("mock 应实现 Snapshotter")
	}
	state, err := snap.Snapshot(ctx, sid)
	if err != nil || state.SessionID != sid || state.Seq <= 0 {
		t.Fatalf("Snapshot=%+v err=%v", state, err)
	}
	if err := snap.Restore(ctx, sid, state); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	events := collectEvents(t, ctx, rt, sid, 0)
	seen := map[eventbus.Type]bool{}
	var lastSeq int64
	for _, e := range events {
		if err := e.Validate(); err != nil {
			t.Fatalf("事件不合法: %+v err=%v", e, err)
		}
		if e.SessionID != string(sid) {
			t.Fatalf("事件 session 不匹配: %+v", e)
		}
		if e.Seq <= lastSeq {
			t.Fatalf("seq 未递增: prev=%d cur=%d", lastSeq, e.Seq)
		}
		lastSeq = e.Seq
		seen[e.Type] = true
	}
	for _, want := range []eventbus.Type{
		eventbus.TypeSessionCreated, eventbus.TypeTurnCreated, eventbus.TypeTokenDelta,
		eventbus.TypeToolCallStarted, eventbus.TypeToolCallFinish, eventbus.TypePlanUpdated,
	} {
		if !seen[want] {
			t.Fatalf("缺少事件 %s，实际 %v", want, seen)
		}
	}

	// from_seq 是排他游标：只返回严格大于它的同会话事件。
	replay := collectEvents(t, ctx, rt, sid, lastSeq-1)
	if len(replay) != 1 || replay[0].Seq != lastSeq {
		t.Fatalf("from_seq 回放不对: %+v (lastSeq=%d)", replay, lastSeq)
	}
	if got := collectEvents(t, ctx, rt, sid, lastSeq); len(got) != 0 {
		t.Fatalf("from_seq=lastSeq 应无事件，得到 %+v", got)
	}
}

func TestRuntimeErrors(t *testing.T) {
	ctx := context.Background()
	rt := mock.NewRuntime()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Title: "errors"})
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
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: sid}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty messages err=%v", err)
	}
	if _, err := rt.InvokeModel(ctx, runtime.ModelRequest{SessionID: "missing", Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "x"}}}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session InvokeModel err=%v", err)
	}
	if _, err := rt.CallTool(ctx, runtime.ToolCall{SessionID: sid}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty tool err=%v", err)
	}
	if _, err := rt.StreamEvents(ctx, sid, -1); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("negative from_seq err=%v", err)
	}
	if _, err := rt.StreamEvents(ctx, "missing", 0); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session StreamEvents err=%v", err)
	}
	if _, err := rt.Plan(ctx, runtime.Goal{}, runtime.State{}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("empty goal err=%v", err)
	}
	if _, err := rt.Plan(ctx, runtime.Goal{Text: "x"}, runtime.State{SessionID: "missing"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session Plan err=%v", err)
	}

	snap := rt
	if _, err := snap.Snapshot(ctx, "missing"); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("missing session Snapshot err=%v", err)
	}
	if err := snap.Restore(ctx, sid, runtime.State{SessionID: "other"}); !errors.Is(err, runtime.ErrInvalid) {
		t.Fatalf("mismatched Restore err=%v", err)
	}

	inner := io.ErrUnexpectedEOF
	wrapped := runtime.Retryable(inner)
	if !runtime.IsRetryable(wrapped) {
		t.Fatalf("Retryable 未标记: %v", wrapped)
	}
	if !errors.Is(wrapped, runtime.ErrRetryable) || !errors.Is(wrapped, inner) {
		t.Fatalf("Retryable 丢失错误链: %v", wrapped)
	}
	if runtime.Retryable(nil) != nil {
		t.Fatal("Retryable(nil) 应返回 nil")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rt.CreateSession(canceled, runtime.SessionSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateSession 未响应取消: %v", err)
	}
	if _, err := rt.Health(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Health 未响应取消: %v", err)
	}
}

func TestRoleValid(t *testing.T) {
	for _, role := range []runtime.Role{runtime.RoleUser, runtime.RoleAssistant, runtime.RoleSystem, runtime.RoleTool} {
		if !role.Valid() {
			t.Fatalf("%s 应合法", role)
		}
	}
	if runtime.Role("robot").Valid() {
		t.Fatal("未知 role 不应合法")
	}
}

func collectEvents(t *testing.T, ctx context.Context, rt runtime.Runtime, sid runtime.SessionID, fromSeq int64) []runtime.Event {
	t.Helper()
	ch, err := rt.StreamEvents(ctx, sid, fromSeq)
	if err != nil {
		t.Fatalf("StreamEvents(%d): %v", fromSeq, err)
	}
	var out []runtime.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			t.Fatal("StreamEvents 超时未关闭")
		}
	}
}

func containsType(types []eventbus.Type, want eventbus.Type) bool {
	for _, typ := range types {
		if typ == want {
			return true
		}
	}
	return false
}
