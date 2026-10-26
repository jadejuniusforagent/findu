package multiagent

import (
	"context"
	"strings"

	"finduai/internal/contextmgmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// appendEinoAgenticChatModelTailMiddlewares appends protocol-neutral handlers for
// TypedChatModelAgent[*schema.AgenticMessage]. Classic ReAct history repair
// handlers stay on the schema.Message path because AgenticMessage has native
// content blocks for function calls/results.
func appendEinoAgenticChatModelTailMiddlewares(
	handlers []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage],
	cfg einoChatModelTailConfig,
) []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	handlers = append(handlers, newAgenticSystemMessageNormalizerMiddleware(cfg.logger, cfg.phase))
	handlers = append(handlers, newAgenticContinuationUserDedupMiddleware(cfg.logger, cfg.phase))
	// 粘性解锁上报：把 tool_search 解锁过的工具记进会话级可见集，下一轮仍常驻，
	// 免去每轮重新解锁的往返（Eino 的解锁只在单次 run 内有效）。只读。
	if unlockMw := newAgenticToolSearchUnlockMiddleware(cfg.conversationID, cfg.logger, cfg.phase); unlockMw != nil {
		handlers = append(handlers, unlockMw)
	}
	if cfg.agenticSummarization != nil {
		handlers = append(handlers, newAgenticToolPairReconcilerMiddleware(cfg.logger, cfg.phase+"_pre_summarization"))
		handlers = append(handlers, cfg.agenticSummarization)
	}
	handlers = append(handlers, newAgenticToolPairReconcilerMiddleware(cfg.logger, cfg.phase))
	// 分级预算软卫兵（收口二）：挂在官方 summarization 与配对修复之后 —— 它看到的是「摘要后仍超
	// 水位且协议已合法」的历史，折叠最旧完整轮到回收目标并留 compact_boundary 边界记录。
	// 常规轮次零动作（前缀缓存是一票否决约束）；开关默认关闭，需显式打开。
	if cfg.contextSoftBudgetEnabled() {
		if sb := contextmgmt.NewAgenticSoftBudget(cfg.modelName, cfg.maxTotalTokens, cfg.logger, cfg.phase+":soft_budget", nil); sb != nil {
			handlers = append(handlers, sb)
			if cfg.logger != nil {
				cfg.logger.Info("eino middleware: context soft budget mounted",
					zap.String("phase", cfg.phase),
					zap.Int("max_total_tokens", cfg.maxTotalTokens))
			}
		}
	}
	if !cfg.skipOrphanPruner {
		handlers = append(handlers, newAgenticOrphanToolPrunerMiddleware(cfg.logger, cfg.phase))
	}
	if !cfg.skipTrace && cfg.trace != nil {
		if capMw := newAgenticModelFacingTraceMiddleware(cfg.trace); capMw != nil {
			handlers = append(handlers, capMw)
		}
	}
	// 模型输入遥测（度量先行）：生产链此前对「每次请求的输入构成」零度量 —— 经典链那份
	// （einoModelInputTelemetryMiddleware）唯一调用点是 PlanExecute 的 PlannerReplanner 重写钩子，
	// 实测只占约 8% 调用量。本中间件只读，不改 state/工具面，logger 为空时不挂。
	if !cfg.skipTelemetry {
		if teleMw := newAgenticModelInputTelemetryMiddleware(cfg.logger, cfg.modelName, cfg.conversationID, cfg.phase); teleMw != nil {
			handlers = append(handlers, teleMw)
		}
	}
	// 压缩后技能重注入：必须排在 summarization 之后，才能发现被压缩掉的技能内容。
	// 未挂载技能缓存（未启用/未调用技能）时恒透传。
	handlers = append(handlers, newAgenticSkillReinjectMiddleware("", cfg.logger, cfg.phase))
	// 墙钟就地收尾：放最后，保证收尾指令不被后续中间件重排掉。
	// cfg.wallClock 显式传入；为 nil 时中间件回落到 run ctx 里的预算（未启用则恒透传）。
	handlers = append(handlers, newWallClockSettleMiddlewareWithBudget(cfg.wallClock, cfg.logger, cfg.phase))
	return handlers
}

type agenticSystemMessageNormalizerMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	logger *zap.Logger
	phase  string
}

func newAgenticSystemMessageNormalizerMiddleware(logger *zap.Logger, phase string) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	return &agenticSystemMessageNormalizerMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		logger:                            logger,
		phase:                             phase,
	}
}

func (m *agenticSystemMessageNormalizerMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	before := countAgenticSystemMessages(state.Messages)
	if before <= 1 {
		return ctx, state, nil
	}
	normalized := normalizeSingleLeadingAgenticSystemMessage(state.Messages)
	if len(normalized) == len(state.Messages) && countAgenticSystemMessages(normalized) >= before {
		return ctx, state, nil
	}
	if m.logger != nil {
		m.logger.Info("eino agentic system messages merged",
			zap.String("phase", m.phase),
			zap.Int("system_before", before),
			zap.Int("system_after", countAgenticSystemMessages(normalized)),
			zap.Int("messages_before", len(state.Messages)),
			zap.Int("messages_after", len(normalized)),
		)
	}
	out := *state
	out.Messages = normalized
	return ctx, &out, nil
}

type agenticContinuationUserDedupMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	logger *zap.Logger
	phase  string
}

func newAgenticContinuationUserDedupMiddleware(logger *zap.Logger, phase string) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	return &agenticContinuationUserDedupMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		logger:                            logger,
		phase:                             phase,
	}
}

func (m *agenticContinuationUserDedupMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	deduped, dropped := dedupAgenticContinuationUserMessages(state.Messages)
	if dropped == 0 {
		return ctx, state, nil
	}
	if m.logger != nil {
		m.logger.Info("eino agentic continuation user messages deduplicated",
			zap.String("phase", m.phase),
			zap.Int("dropped", dropped),
			zap.Int("messages_before", len(state.Messages)),
			zap.Int("messages_after", len(deduped)),
		)
	}
	out := *state
	out.Messages = deduped
	return ctx, &out, nil
}

func countAgenticSystemMessages(msgs []*schema.AgenticMessage) int {
	n := 0
	for _, msg := range msgs {
		if msg != nil && msg.Role == schema.AgenticRoleTypeSystem {
			n++
		}
	}
	return n
}

func normalizeSingleLeadingAgenticSystemMessage(msgs []*schema.AgenticMessage) []*schema.AgenticMessage {
	var systemParts []string
	out := make([]*schema.AgenticMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if msg.Role == schema.AgenticRoleTypeSystem {
			if text := strings.TrimSpace(agenticMessageText(msg)); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		out = append(out, msg)
	}
	if len(systemParts) == 0 {
		return out
	}
	merged := schema.SystemAgenticMessage(strings.Join(systemParts, "\n\n"))
	return append([]*schema.AgenticMessage{merged}, out...)
}

func dedupAgenticContinuationUserMessages(msgs []*schema.AgenticMessage) ([]*schema.AgenticMessage, int) {
	lastIdx := -1
	contCount := 0
	for i, msg := range msgs {
		if !isAgenticContinuationUserMessage(msg) {
			continue
		}
		contCount++
		lastIdx = i
	}
	if contCount <= 1 {
		return msgs, 0
	}
	out := make([]*schema.AgenticMessage, 0, len(msgs)-(contCount-1))
	dropped := 0
	for i, msg := range msgs {
		if isAgenticContinuationUserMessage(msg) && i != lastIdx {
			dropped++
			continue
		}
		out = append(out, msg)
	}
	return out, dropped
}

func isAgenticContinuationUserMessage(msg *schema.AgenticMessage) bool {
	if msg == nil || msg.Role != schema.AgenticRoleTypeUser {
		return false
	}
	return strings.Contains(agenticMessageText(msg), continuationSessionMarker)
}

func agenticMessageText(msg *schema.AgenticMessage) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.UserInputText != nil:
			if s := strings.TrimSpace(block.UserInputText.Text); s != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(s)
			}
		case block.AssistantGenText != nil:
			if s := strings.TrimSpace(block.AssistantGenText.Text); s != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(s)
			}
		}
	}
	return b.String()
}
