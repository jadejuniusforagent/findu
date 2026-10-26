package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"finduai/internal/toolguard"
)

const toolGuardBlockedPrefix = "工具调用已被安全规则拦截"
const toolGuardBlockedMetaKey = "finduai.ai/blocked"

// toolGuardAskTimeout bounds how long an ask-action hit waits for a human. On expiry the call
// is denied (fail-closed), so a stalled approval channel can never unblock a call.
const toolGuardAskTimeout = 3 * time.Minute

// ToolGuardAsker routes an ask-action tool-guard hit to a human adjudication channel. The
// implementation lives outside this package (the app wires it in at startup) because the HITL
// engine depends on the handler layer, which already imports this one.
type ToolGuardAsker interface {
	AskToolGuard(ctx context.Context, conversationID, toolName, payload string, timeout time.Duration) (approved bool, comment string)
}

var toolGuardAsker atomic.Value // ToolGuardAsker

// SetToolGuardAsker installs the adjudication channel used by ask rules.
func SetToolGuardAsker(asker ToolGuardAsker) {
	if asker != nil {
		toolGuardAsker.Store(asker)
	}
}

func currentToolGuardAsker() ToolGuardAsker {
	if v := toolGuardAsker.Load(); v != nil {
		if asker, ok := v.(ToolGuardAsker); ok {
			return asker
		}
	}
	return nil
}

// toolGuardBlockError carries structured policy results through pre-run hooks.
type toolGuardBlockError struct{ result *ToolResult }

func (e *toolGuardBlockError) Error() string { return ToolResultPlainText(e.result) }

func toolResultProtocolMeta(result *ToolResult) map[string]interface{} {
	if result != nil && result.Blocked {
		return map[string]interface{}{toolGuardBlockedMetaKey: true}
	}
	return nil
}

// toolGuardBlockedResult uses the standard MCP error result so the refusal is
// visible both to the model and in persisted execution monitoring records.
func toolGuardBlockedResult(guard *toolguard.Manager, toolName string, args map[string]interface{}) *ToolResult {
	return toolGuardEvaluate(context.Background(), guard, toolName, args)
}

// toolGuardEvaluate applies the policy and returns a blocked result when the call must not run.
//
// allow short-circuits (nil); deny blocks; ask is routed to the adjudication channel. An ask
// without a conversation context, without a wired asker, or without an approval is denied —
// fail-closed, since a missing channel must never be more permissive than a present one.
func toolGuardEvaluate(ctx context.Context, guard *toolguard.Manager, toolName string, args map[string]interface{}) *ToolResult {
	if guard == nil {
		return nil
	}
	decision := guard.Decide(toolName, args)
	if decision == nil || decision.Action == toolguard.ActionAllow {
		return nil
	}
	if decision.Action == toolguard.ActionAsk {
		conversationID := MCPConversationIDFromContext(ctx)
		asker := currentToolGuardAsker()
		if asker == nil || strings.TrimSpace(conversationID) == "" {
			return toolGuardBlockedResultFromDecision(decision, "该规则需人工裁决，但当前链路无会话上下文或 HITL 未接入")
		}
		payload := "{}"
		if raw, err := json.Marshal(map[string]interface{}{"toolName": toolName, "arguments": args}); err == nil {
			payload = string(raw)
		}
		approved, comment := asker.AskToolGuard(ctx, conversationID, toolName, payload, toolGuardAskTimeout)
		if approved {
			return nil
		}
		if strings.TrimSpace(comment) == "" {
			comment = "未获批准（超时或拒绝）"
		}
		return toolGuardBlockedResultFromDecision(decision, "人工裁决未放行："+comment)
	}
	return toolGuardBlockedResultFromDecision(decision, "")
}

// toolGuardBlockedResultFromDecision renders the standard MCP error result, so the refusal is
// visible both to the model and in persisted execution monitoring records.
func toolGuardBlockedResultFromDecision(decision *toolguard.Decision, extra string) *ToolResult {
	if decision == nil {
		return nil
	}
	message := toolGuardBlockedPrefix
	if custom := strings.TrimSpace(decision.Message); custom != "" {
		message += "：" + custom
	}
	if note := strings.TrimSpace(extra); note != "" {
		message += "\n" + note
	}
	message += fmt.Sprintf("\n规则: %s (%s)\n动作: %s\n匹配内容: %q", decision.RuleName, decision.RuleID, decision.Action, decision.MatchedText)
	return &ToolResult{Content: []Content{{Type: "text", Text: message}}, IsError: true, Blocked: true}
}
