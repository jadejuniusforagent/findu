package askernel

import (
	"testing"

	"finduai/acllm"
	"finduai/transcript"
)

func TestSessionRestoreRoundTripsTranscript(t *testing.T) {
	store := transcript.NewStore(t.TempDir())
	s := NewSession(Options{Model: &fakeModel{}, Transcript: store, SessionID: "restore-id"})
	msgs := []acllm.Message{
		acllm.UserText("user turn"),
		{Role: acllm.RoleAssistant, Content: []acllm.ContentBlock{acllm.TextBlock("assistant turn")}},
	}
	usage := acllm.Usage{InputTokens: 3, OutputTokens: 4}
	if err := s.Restore(msgs, usage); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := s.Messages(); len(got) != 2 || got[0].Text() != "user turn" || got[1].Text() != "assistant turn" {
		t.Fatalf("in-memory messages = %+v", got)
	}
	if got := s.Usage(); got != usage {
		t.Fatalf("usage = %+v, want %+v", got, usage)
	}
	_ = s.Close()

	s2 := NewSession(Options{Model: &fakeModel{}, Transcript: store, SessionID: "restore-id"})
	defer s2.Close()
	if err := s2.Resume("restore-id"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := s2.Messages(); len(got) != 2 || got[0].Text() != "user turn" || got[1].Text() != "assistant turn" {
		t.Fatalf("resumed messages = %+v", got)
	}
	if got := s2.Usage(); got != usage {
		t.Fatalf("resumed usage = %+v, want %+v", got, usage)
	}
}
