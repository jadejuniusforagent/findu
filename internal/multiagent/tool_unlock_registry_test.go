package multiagent

import (
	"context"
	"testing"

	"finduai/internal/config"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func toolSearchResultMessage(callID, payload string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{{
			Type: schema.ContentBlockTypeFunctionToolResult,
			FunctionToolResult: &schema.FunctionToolResult{
				CallID: callID,
				Name:   toolSearchToolName,
				Content: []*schema.FunctionToolResultContentBlock{{
					Type: schema.FunctionToolResultContentBlockTypeText,
					Text: &schema.UserInputText{Text: payload},
				}},
			},
		}},
	}
}

// A tool_search result must make its tools stick for the whole conversation, and the registry
// must stay bounded and isolated per conversation.
func TestStickyUnlockRegistry(t *testing.T) {
	resetStickyUnlocksForTest()
	defer resetStickyUnlocksForTest()

	RecordToolUnlocks("conv-a", []string{"ffuf", " ", "nmap", "ffuf"})
	got := StickyUnlockedTools("conv-a")
	if len(got) != 2 || got[0] != "ffuf" || got[1] != "nmap" {
		t.Fatalf("sticky set = %v, want [ffuf nmap]", got)
	}
	if other := StickyUnlockedTools("conv-b"); other != nil {
		t.Fatalf("conversations must not share unlocks: %v", other)
	}
	if none := StickyUnlockedTools(""); none != nil {
		t.Fatal("an empty conversation id must not read the registry")
	}

	// The per-conversation cap keeps the set bounded.
	var bulk []string
	for i := 0; i < maxStickyUnlockedTools+20; i++ {
		bulk = append(bulk, "tool-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	RecordToolUnlocks("conv-c", bulk)
	if n := len(StickyUnlockedTools("conv-c")); n > maxStickyUnlockedTools {
		t.Fatalf("sticky set exceeded the cap: %d", n)
	}
}

func TestAgenticToolSearchUnlockMiddlewareRecords(t *testing.T) {
	resetStickyUnlocksForTest()
	defer resetStickyUnlocksForTest()

	if mw := newAgenticToolSearchUnlockMiddleware("", nil, "p"); mw != nil {
		t.Fatal("without a conversation id the middleware must not mount")
	}
	mw := newAgenticToolSearchUnlockMiddleware("conv-1", nil, "p")
	if mw == nil {
		t.Fatal("expected a middleware")
	}
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages: []*schema.AgenticMessage{
			toolSearchResultMessage("c1", `{"selectedTools":["nuclei","dirsearch"]}`),
			toolSearchResultMessage("c2", `not json`),
			toolSearchResultMessage("c3", `{"selectedTools":["sqlmap"]}`),
		},
	}
	if _, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil); err != nil {
		t.Fatal(err)
	}
	got := StickyUnlockedTools("conv-1")
	if len(got) != 3 {
		t.Fatalf("sticky set = %v, want 3 names", got)
	}
}

// The residency merge must include the sticky names.
func TestAlwaysVisibleToolNamesIncludesSticky(t *testing.T) {
	resetStickyUnlocksForTest()
	defer resetStickyUnlocksForTest()

	RecordToolUnlocks("conv-m", []string{"unlocked-by-search"})
	names := alwaysVisibleToolNames(&config.MultiAgentEinoMiddlewareConfig{
		ToolSearchAlwaysVisibleTools: []string{"exec"},
	}, "conv-m")
	found := false
	for _, n := range names {
		if n == "unlocked-by-search" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("sticky unlock must be added to the always-visible names")
	}
}
