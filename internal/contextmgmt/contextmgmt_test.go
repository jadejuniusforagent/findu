package contextmgmt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestDecideTiers(t *testing.T) {
	p := DefaultPolicy(100_000)
	for _, tc := range []struct {
		name  string
		in    int
		want  Tier
		token int
	}{
		{"under watermark", 50_000, TierNormal, 0},
		{"just below snip", 89_999, TierNormal, 0},
		{"snip", 90_000, TierSnip, 75_000},
		{"compact", 95_000, TierCompact, 75_000},
		{"collapse", 100_000, TierCollapse, 75_000},
		{"over budget", 140_000, TierCollapse, 75_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, target := Decide(p, tc.in)
			if got != tc.want || target != tc.token {
				t.Fatalf("Decide(%d) = (%s,%d), want (%s,%d)", tc.in, got, target, tc.want, tc.token)
			}
		})
	}
	// Without a budget the guard must stay inert.
	if got, _ := Decide(Policy{}, 10_000_000); got != TierNormal {
		t.Fatalf("no budget should stay normal, got %s", got)
	}
}

func TestBreakerOpensAndRecovers(t *testing.T) {
	b := NewBreaker(2, time.Minute)
	now := time.Unix(0, 0)
	b.SetNow(func() time.Time { return now })

	if !b.Allow() {
		t.Fatal("closed breaker must allow")
	}
	b.Fail()
	if !b.Allow() {
		t.Fatal("one failure is below threshold")
	}
	b.Fail()
	if b.Allow() {
		t.Fatal("breaker must be open after reaching the threshold")
	}
	now = now.Add(2 * time.Minute)
	if !b.Allow() {
		t.Fatal("half-open probe must be allowed after the cooldown")
	}
	b.Success()
	if !b.Allow() {
		t.Fatal("success must close the breaker")
	}
}

func TestSplitAgenticRoundsPairsToolResults(t *testing.T) {
	call := func(id string) *schema.AgenticMessage {
		return &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{{
				Type:             schema.ContentBlockTypeFunctionToolCall,
				FunctionToolCall: &schema.FunctionToolCall{CallID: id, Name: "exec"},
			}},
		}
	}
	result := func(id string) *schema.AgenticMessage {
		return &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{{
				Type:               schema.ContentBlockTypeFunctionToolResult,
				FunctionToolResult: &schema.FunctionToolResult{CallID: id},
			}},
		}
	}
	text := func(s string) *schema.AgenticMessage {
		return &schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{{Type: schema.ContentBlockTypeUserInputText, UserInputText: &schema.UserInputText{Text: s}}},
		}
	}

	rounds := splitAgenticRounds([]*schema.AgenticMessage{
		text("hello"), // round 1
		call("c1"),    // round 2 (call + its result)
		result("c1"),
		result("orphan"), // unpaired result -> dropped
		call("c2"),       // round 3
		result("c2"),
	})
	if len(rounds) != 3 {
		t.Fatalf("rounds = %d, want 3", len(rounds))
	}
	if len(rounds[0].messages) != 1 || len(rounds[1].messages) != 2 {
		t.Fatalf("unexpected round shapes: %d/%d", len(rounds[0].messages), len(rounds[1].messages))
	}
}

func TestBoundaryNoteRendersWhitelist(t *testing.T) {
	note := BoundaryNote(3, 1234, ReinjectionWhitelist{TaskType: "recon", RoleName: "渗透测试", Extra: []string{"  "}})
	for _, want := range []string{"compact_boundary", "3 轮", "1234", "recon", "渗透测试", "角色提示词"} {
		if !contains(note, want) {
			t.Fatalf("boundary note missing %q:\n%s", want, note)
		}
	}
}

// The middleware must actually fold (not just decide), keep the system head, insert the
// compact_boundary note, and leave at least the newest round intact.
func TestAgenticSoftBudgetFoldsOldestRounds(t *testing.T) {
	if NewAgenticSoftBudget("m", 0, nil, "t", nil) != nil {
		t.Fatal("a missing budget must skip mounting")
	}
	build := func(rounds int) []*schema.AgenticMessage {
		var msgs []*schema.AgenticMessage
		msgs = append(msgs, &schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeSystem,
			ContentBlocks: []*schema.ContentBlock{{Type: schema.ContentBlockTypeUserInputText, UserInputText: &schema.UserInputText{Text: "system head"}}},
		})
		for i := 0; i < rounds; i++ {
			msgs = append(msgs, &schema.AgenticMessage{
				Role: schema.AgenticRoleTypeUser,
				ContentBlocks: []*schema.ContentBlock{{
					Type:          schema.ContentBlockTypeUserInputText,
					UserInputText: &schema.UserInputText{Text: strings.Repeat("x", 400)},
				}},
			})
		}
		return msgs
	}

	msgs := build(30)
	est := EstimateAgenticInputTokens("m", msgs, nil)
	if est <= 0 {
		t.Fatal("estimate must be positive")
	}
	// Land in the snip tier (>=0.90, <0.95) so Decide picks TierSnip.
	budget := int(float64(est) / 0.92)
	sb := NewAgenticSoftBudget("m", budget, nil, "t", nil)
	if sb == nil {
		t.Fatal("breaker/budget guard must mount when a budget exists")
	}
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: msgs}
	_, out, err := sb.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || len(out.Messages) >= len(msgs) {
		t.Fatalf("expected the oldest rounds folded: before=%d after=%d", len(msgs), len(out.Messages))
	}
	if out.Messages[0].Role != schema.AgenticRoleTypeSystem {
		t.Fatal("the leading system message must survive folding")
	}
	found := false
	for _, m := range out.Messages {
		if m != nil && contains(agenticTextOf(m), "compact_boundary") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("a compact_boundary note must be inserted at the fold point")
	}
	// The original slice must not be mutated in place.
	if len(msgs) != 31 {
		t.Fatalf("input state mutated: %d", len(msgs))
	}
}

func agenticTextOf(m *schema.AgenticMessage) string {
	if m == nil {
		return ""
	}
	var sb []byte
	for _, b := range m.ContentBlocks {
		if b == nil || b.UserInputText == nil {
			continue
		}
		sb = append(sb, b.UserInputText.Text...)
	}
	return string(sb)
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
