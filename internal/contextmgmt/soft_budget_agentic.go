package contextmgmt

import (
	"context"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// Soft budget guard for the production (AgenticMessage) chain.
//
// Chain position: after the official summarization middleware, so it sees history that is
// still over the watermark *after* summarization. It folds the oldest complete rounds down to
// the recovery target, records a compact_boundary note, and leaves overflow recovery to the
// existing multiagent overflow-retry path.
//
// Hard constraint: the watermark (0.90) sits below the summarization trigger and a normal
// round is a no-op, because a rewritten history prefix costs the whole prefix cache.

// AgenticSoftBudget is the tiered-budget guard (Typed ChatModelAgent middleware).
type AgenticSoftBudget struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	modelName string
	policy    Policy
	breaker   *Breaker
	whitelist func() ReinjectionWhitelist
	logger    *zap.Logger
	phase     string
}

// NewAgenticSoftBudget returns nil when maxTotalTokens<=0, so a caller can skip mounting it
// when no budget is configured. whitelist may be nil (renders a blank whitelist).
func NewAgenticSoftBudget(modelName string, maxTotalTokens int, logger *zap.Logger, phase string, whitelist func() ReinjectionWhitelist) *AgenticSoftBudget {
	if maxTotalTokens <= 0 {
		return nil
	}
	return &AgenticSoftBudget{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		modelName:                         modelName,
		policy:                            DefaultPolicy(maxTotalTokens),
		breaker:                           NewBreaker(3, 5*time.Minute),
		whitelist:                         whitelist,
		logger:                            logger,
		phase:                             phase,
	}
}

func (m *AgenticSoftBudget) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if m == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	// state.ToolInfos is Eino's source of truth; ModelContext.Tools is deprecated and only
	// kept for compatibility, so it is a fallback when the former is empty.
	var tools []*schema.ToolInfo
	if len(state.ToolInfos) > 0 {
		tools = state.ToolInfos
	} else if mc != nil {
		tools = mc.Tools
	}
	inputTokens := EstimateAgenticInputTokens(m.modelName, state.Messages, tools)
	tier, target := Decide(m.policy, inputTokens)
	if tier == TierNormal {
		return ctx, state, nil
	}
	if tier == TierCollapse {
		if m.logger != nil {
			m.logger.Warn("context tier collapse: input at/over budget, overflow retry owns recovery",
				zap.String("phase", m.phase),
				zap.Int("input_tokens_estimated", inputTokens),
				zap.Int("max_total_tokens", m.policy.MaxTotalTokens))
		}
		return ctx, state, nil
	}
	if !m.breaker.Allow() {
		if m.logger != nil {
			m.logger.Warn("context soft budget skipped (breaker open)",
				zap.String("phase", m.phase),
				zap.Int("input_tokens_estimated", inputTokens))
		}
		return ctx, state, nil
	}

	systemHead, body := splitLeadingSystem(state.Messages)
	rounds := splitAgenticRounds(body)
	if len(rounds) <= 1 {
		// Only the last round is protected; nothing old enough to fold.
		return ctx, state, nil
	}
	droppedTokens := 0
	droppedRounds := 0
	kept := rounds
	// The recovery check must use the same measurement as the trigger (including tools):
	// tool definitions are resident and folding only the bare messages can look "under target"
	// while the real input still sits at the watermark, which folds again a few rounds later
	// and rewrites the prefix each time.
	for len(kept) > 1 {
		if estimateRoundsTokens(m.modelName, systemHead, kept, tools) <= target {
			break
		}
		droppedTokens += estimateRoundTokens(m.modelName, kept[0])
		kept = kept[1:]
		droppedRounds++
	}
	if droppedRounds == 0 {
		m.breaker.Success()
		return ctx, state, nil
	}

	var wl ReinjectionWhitelist
	if m.whitelist != nil {
		wl = m.whitelist()
	}
	note := BoundaryNote(droppedRounds, droppedTokens, wl)
	out := make([]*schema.AgenticMessage, 0, len(systemHead)+2+(len(kept)-1))
	out = append(out, systemHead...)
	out = append(out, &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{{
			Type:          schema.ContentBlockTypeUserInputText,
			UserInputText: &schema.UserInputText{Text: note},
		}},
	})
	for _, r := range kept {
		out = append(out, r.messages...)
	}
	m.breaker.Success()
	if m.logger != nil {
		m.logger.Warn("context soft budget: oldest rounds folded (compact_boundary recorded)",
			zap.String("phase", m.phase),
			zap.String("tier", tier.String()),
			zap.Int("input_tokens_estimated", inputTokens),
			zap.Int("snip_target_tokens", target),
			zap.Int("dropped_rounds", droppedRounds),
			zap.Int("dropped_tokens_estimated", droppedTokens),
			zap.Int("messages_before", len(state.Messages)),
			zap.Int("messages_after", len(out)))
	}
	ns := *state
	ns.Messages = out
	return ctx, &ns, nil
}

// splitLeadingSystem returns the leading run of system messages (never folded) and the rest.
func splitLeadingSystem(msgs []*schema.AgenticMessage) (head []*schema.AgenticMessage, body []*schema.AgenticMessage) {
	i := 0
	for i < len(msgs) {
		if msgs[i] != nil && msgs[i].Role == schema.AgenticRoleTypeSystem {
			i++
			continue
		}
		break
	}
	return msgs[:i:i], msgs[i:]
}

// estimateRoundsTokens estimates system plus every kept round, with the same tool set used by
// the trigger decision.
func estimateRoundsTokens(modelName string, systemHead []*schema.AgenticMessage, rounds []agenticRound, tools []*schema.ToolInfo) int {
	flat := make([]*schema.AgenticMessage, 0, len(systemHead)+len(rounds)*2)
	flat = append(flat, systemHead...)
	for _, r := range rounds {
		flat = append(flat, r.messages...)
	}
	return EstimateAgenticInputTokens(modelName, flat, tools)
}

// estimateRoundTokens estimates a single round.
func estimateRoundTokens(modelName string, r agenticRound) int {
	return EstimateAgenticInputTokens(modelName, r.messages, nil)
}
