package multiagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// agenticToolSearchUnlockMiddleware records which tools tool_search unlocked so they can stay
// resident for the rest of the conversation (see tool_unlock_registry.go). It runs on the
// production agentic chain, where no equivalent existed: the classic-chain sanitizer only
// repaired malformed results and never reported unlocks.
//
// Read-only: it never mutates the message state.
type agenticToolSearchUnlockMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	conversationID string
	logger         *zap.Logger
	phase          string
}

// newAgenticToolSearchUnlockMiddleware returns nil without a conversation id, since stickiness
// is meaningless without one (a caller can therefore skip mounting it).
func newAgenticToolSearchUnlockMiddleware(conversationID string, logger *zap.Logger, phase string) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	if strings.TrimSpace(conversationID) == "" {
		return nil
	}
	return &agenticToolSearchUnlockMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		conversationID:                    strings.TrimSpace(conversationID),
		logger:                            logger,
		phase:                             strings.TrimSpace(phase),
	}
}

func (m *agenticToolSearchUnlockMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || state == nil {
		return ctx, state, nil
	}
	var names []string
	for _, msg := range state.Messages {
		if msg == nil {
			continue
		}
		for _, blk := range msg.ContentBlocks {
			if blk == nil || blk.Type != schema.ContentBlockTypeFunctionToolResult || blk.FunctionToolResult == nil {
				continue
			}
			if !IsToolSearchTool(blk.FunctionToolResult.Name) {
				continue
			}
			names = append(names, selectedToolNames(blk.FunctionToolResult)...)
		}
	}
	if len(names) == 0 {
		return ctx, state, nil
	}
	before := StickyUnlockedTools(m.conversationID)
	RecordToolUnlocks(m.conversationID, names)
	if m.logger != nil && len(before) != len(StickyUnlockedTools(m.conversationID)) {
		m.logger.Info("agentic tool_search unlocks recorded (sticky for this conversation)",
			zap.String("phase", m.phase),
			zap.String("conversation_id", m.conversationID),
			zap.Strings("unlocked", names),
			zap.Int("sticky_total", len(StickyUnlockedTools(m.conversationID))),
		)
	}
	return ctx, state, nil
}

// selectedToolNames extracts the selectedTools array from a tool_search result payload. Eino
// expects the result to be a JSON object containing selectedTools.
func selectedToolNames(result *schema.FunctionToolResult) []string {
	for _, c := range result.Content {
		if c == nil || c.Type != schema.FunctionToolResultContentBlockTypeText || c.Text == nil {
			continue
		}
		var envelope struct {
			SelectedTools []string `json:"selectedTools"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(c.Text.Text)), &envelope); err != nil {
			continue
		}
		return envelope.SelectedTools
	}
	return nil
}
