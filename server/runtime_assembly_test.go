package server

import (
	"context"
	"testing"
	"time"

	"finduai/agent"
	"finduai/core/eventbus"
	coremodel "finduai/core/model"
	"finduai/core/runtime"
	"finduai/transcript"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type assemblyFakeModel struct {
	msg *schema.Message
}

func (f *assemblyFakeModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return f.msg, nil
}

func (f *assemblyFakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{f.msg}), nil
}

func (f *assemblyFakeModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

func newAssemblyAgent(t *testing.T) *agent.ChatAgent {
	t.Helper()
	return agent.NewChatAgent(
		&assemblyFakeModel{msg: schema.AssistantMessage("assembly answer", nil)},
		"fake-model",
		t.TempDir(),
		transcript.NewStore(t.TempDir()),
		1000,
	)
}

func TestChatConversationRuntimeSPI(t *testing.T) {
	ca := newAssemblyAgent(t)
	s := &Server{m: &Manager{}}
	t.Setenv("FINDU_RUNTIME_SPI", "1")

	var activities []coremodel.Activity
	text, err := s.chatConversation(context.Background(), ca, "native-test", "conv-test", "hello", 0, 0, false, func(a coremodel.Activity) {
		activities = append(activities, a)
	})
	if err != nil {
		t.Fatalf("chatConversation: %v", err)
	}
	if text != "assembly answer" {
		t.Fatalf("text = %q", text)
	}
	if len(activities) == 0 {
		t.Fatal("ActivitySink received no activities")
	}
}

func TestChatConversationDefaultPath(t *testing.T) {
	ca := newAssemblyAgent(t)
	s := &Server{m: &Manager{}}
	t.Setenv("FINDU_RUNTIME_SPI", "0")

	text, err := s.chatConversation(context.Background(), ca, "native-test", "conv-default", "hello", 0, 0, false, nil)
	if err != nil {
		t.Fatalf("chatConversation default: %v", err)
	}
	if text != "assembly answer" {
		t.Fatalf("text = %q", text)
	}
}

func TestRuntimeAdapterForAgent(t *testing.T) {
	ca := newAssemblyAgent(t)
	s := &Server{m: &Manager{}}
	s.chatAgent = ca

	rt, err := s.RuntimeAdapterForAgent("native-test")
	if err != nil {
		t.Fatalf("RuntimeAdapterForAgent: %v", err)
	}
	caps, err := rt.Health(context.Background())
	if err != nil || !caps.Streaming || !caps.Snapshot {
		t.Fatalf("capabilities = %+v err=%v", caps, err)
	}

	ctx := context.Background()
	sid, err := rt.CreateSession(ctx, runtime.SessionSpec{Title: "assembly"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AppendTurn(ctx, sid, runtime.Turn{Role: runtime.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	ch, err := rt.StreamEvents(ctx, sid, 0)
	if err != nil {
		t.Fatal(err)
	}
	var assistant string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				if assistant != "assembly answer" {
					t.Fatalf("assistant = %q, events ended", assistant)
				}
				return
			}
			if e.Type == eventbus.TypeTurnCreated {
				if role, _ := e.Payload["role"].(string); role == string(runtime.RoleAssistant) {
					assistant, _ = e.Payload["content"].(string)
				}
			}
		case <-timeout:
			t.Fatal("StreamEvents timed out")
		}
	}
}
