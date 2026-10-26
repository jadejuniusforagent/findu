package multiagent

import (
	"context"
	"strings"

	"finduai/internal/agent"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

type einoModelInputTelemetryMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	logger         *zap.Logger
	modelName      string
	conversationID string
	phase          string
}

func newEinoModelInputTelemetryMiddleware(
	logger *zap.Logger,
	modelName string,
	conversationID string,
	phase string,
) adk.ChatModelAgentMiddleware {
	if logger == nil {
		return nil
	}
	return &einoModelInputTelemetryMiddleware{
		logger:         logger,
		modelName:      strings.TrimSpace(modelName),
		conversationID: strings.TrimSpace(conversationID),
		phase:          strings.TrimSpace(phase),
	}
}

func (m *einoModelInputTelemetryMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	mc *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if m == nil || m.logger == nil || state == nil {
		return ctx, state, nil
	}
	tokens := estimateTokensForMessagesAndTools(ctx, m.modelName, state.Messages, mcTools(mc))
	m.logger.Info("eino model input estimated",
		zap.String("phase", m.phase),
		zap.String("conversation_id", m.conversationID),
		zap.Int("messages", len(state.Messages)),
		zap.Int("tools", len(mcTools(mc))),
		zap.Int("input_tokens_estimated", tokens),
	)
	return ctx, state, nil
}

func mcTools(mc *adk.ModelContext) []*schema.ToolInfo {
	if mc == nil || len(mc.Tools) == 0 {
		return nil
	}
	return mc.Tools
}

func estimateTokensForMessagesAndTools(
	_ context.Context,
	modelName string,
	messages []adk.Message,
	tools []*schema.ToolInfo,
) int {
	var sb strings.Builder
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		sb.WriteString(string(msg.Role))
		sb.WriteByte('\n')
		sb.WriteString(msg.Content)
		sb.WriteByte('\n')
		if msg.ReasoningContent != "" {
			sb.WriteString(msg.ReasoningContent)
			sb.WriteByte('\n')
		}
		if len(msg.ToolCalls) > 0 {
			if b, err := sonic.Marshal(msg.ToolCalls); err == nil {
				sb.Write(b)
				sb.WriteByte('\n')
			}
		}
	}
	appendToolEstimates(&sb, tools)
	return countEstimatedTokens(modelName, sb.String())
}

// appendToolEstimates writes the model-visible tool definitions into the estimate buffer.
func appendToolEstimates(sb *strings.Builder, tools []*schema.ToolInfo) {
	for _, tl := range tools {
		if tl == nil {
			continue
		}
		cp := *tl
		cp.Extra = nil
		if text, err := sonic.MarshalString(cp); err == nil {
			sb.WriteString(text)
			sb.WriteByte('\n')
		}
	}
}

// countEstimatedTokens prefers the model's own tokenizer and falls back to a byte heuristic.
func countEstimatedTokens(modelName, text string) int {
	if text == "" {
		return 0
	}
	tc := agent.NewTikTokenCounter()
	if n, err := tc.Count(modelName, text); err == nil {
		return n
	}
	return (len(text) + 3) / 4
}

// agenticModelInputTelemetryMiddleware reports the estimated size and composition of every
// model call on the production agentic chain. einoModelInputTelemetryMiddleware only sits on
// the classic chain, whose single caller is the PlanExecute rewrite hook, so without this the
// platform has no per-request measurement of what it actually sends to the model.
type agenticModelInputTelemetryMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	logger         *zap.Logger
	modelName      string
	conversationID string
	phase          string
}

func newAgenticModelInputTelemetryMiddleware(
	logger *zap.Logger,
	modelName string,
	conversationID string,
	phase string,
) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	if logger == nil {
		return nil
	}
	return &agenticModelInputTelemetryMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		logger:                            logger,
		modelName:                         strings.TrimSpace(modelName),
		conversationID:                    strings.TrimSpace(conversationID),
		phase:                             strings.TrimSpace(phase),
	}
}

// BeforeModelRewriteState only observes: it never mutates the state or the tool list.
func (m *agenticModelInputTelemetryMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || m.logger == nil || state == nil {
		return ctx, state, nil
	}
	// ToolInfos is the source of truth for the model call on this protocol; ModelContext.Tools
	// is deprecated and may lag behind edits made by earlier middlewares.
	tools := state.ToolInfos
	tokens := estimateTokensForAgenticMessagesAndTools(m.modelName, state.Messages, tools)
	m.logger.Info("eino model input estimated",
		zap.String("phase", m.phase),
		zap.String("chain", "agentic"),
		zap.String("conversation_id", m.conversationID),
		zap.Int("messages", len(state.Messages)),
		zap.Int("tools", len(tools)),
		zap.Int("input_tokens_estimated", tokens),
	)
	return ctx, state, nil
}

// estimateTokensForAgenticMessagesAndTools mirrors the classic estimator for AgenticMessage,
// whose content lives in typed content blocks (text, tool calls, tool results) rather than
// flat Content/ToolCalls fields.
func estimateTokensForAgenticMessagesAndTools(
	modelName string,
	messages []*schema.AgenticMessage,
	tools []*schema.ToolInfo,
) int {
	var sb strings.Builder
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		sb.WriteString(string(msg.Role))
		sb.WriteByte('\n')
		if len(msg.ContentBlocks) > 0 {
			if b, err := sonic.Marshal(msg.ContentBlocks); err == nil {
				sb.Write(b)
				sb.WriteByte('\n')
				continue
			}
		}
		if text := strings.TrimSpace(agenticMessageText(msg)); text != "" {
			sb.WriteString(text)
			sb.WriteByte('\n')
		}
	}
	appendToolEstimates(&sb, tools)
	return countEstimatedTokens(modelName, sb.String())
}

func logPlanExecuteModelInputEstimate(
	logger *zap.Logger,
	modelName string,
	conversationID string,
	phase string,
	msgs []adk.Message,
) {
	if logger == nil {
		return
	}
	tokens := estimateTokensForMessagesAndTools(context.Background(), modelName, msgs, nil)
	logger.Info("eino model input estimated",
		zap.String("phase", phase),
		zap.String("conversation_id", strings.TrimSpace(conversationID)),
		zap.Int("messages", len(msgs)),
		zap.Int("tools", 0),
		zap.Int("input_tokens_estimated", tokens),
	)
}
