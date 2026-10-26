package contextmgmt

import (
	"strings"

	"finduai/internal/agent"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// Input estimation and round splitting for the AgenticMessage protocol. The estimation
// convention matches the multiagent telemetry middleware (tiktoken, falling back to len/4);
// the round semantics match multiagent's classic splitMessagesIntoRounds so folding never
// separates a tool call from its results.

// EstimateAgenticInputTokens estimates one request's input tokens (messages + tool defs).
func EstimateAgenticInputTokens(modelName string, msgs []*schema.AgenticMessage, tools []*schema.ToolInfo) int {
	text := agenticMessagesText(msgs) + "\n" + agenticToolsText(tools)
	if text == "" {
		return 0
	}
	tc := agent.NewTikTokenCounter()
	if n, err := tc.Count(modelName, text); err == nil {
		return n
	}
	return (len(text) + 3) / 4
}

func agenticMessagesText(msgs []*schema.AgenticMessage) string {
	var sb strings.Builder
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		sb.WriteString(string(msg.Role))
		sb.WriteByte('\n')
		for _, blk := range msg.ContentBlocks {
			if blk == nil {
				continue
			}
			switch blk.Type {
			case schema.ContentBlockTypeUserInputText:
				if blk.UserInputText != nil {
					sb.WriteString(blk.UserInputText.Text)
				}
			case schema.ContentBlockTypeAssistantGenText:
				if blk.AssistantGenText != nil {
					sb.WriteString(blk.AssistantGenText.Text)
				}
			case schema.ContentBlockTypeReasoning:
				if blk.Reasoning != nil {
					sb.WriteString(blk.Reasoning.Text)
				}
			case schema.ContentBlockTypeFunctionToolCall:
				if blk.FunctionToolCall != nil {
					if b, err := sonic.Marshal(blk.FunctionToolCall); err == nil {
						sb.Write(b)
					}
				}
			case schema.ContentBlockTypeFunctionToolResult:
				if blk.FunctionToolResult != nil {
					if b, err := sonic.Marshal(blk.FunctionToolResult); err == nil {
						sb.Write(b)
					}
				}
			case schema.ContentBlockTypeToolSearchResult:
				if blk.ToolSearchFunctionToolResult != nil {
					if b, err := sonic.Marshal(blk.ToolSearchFunctionToolResult); err == nil {
						sb.Write(b)
					}
				}
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func agenticToolsText(tools []*schema.ToolInfo) string {
	var sb strings.Builder
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
	return sb.String()
}

// agenticRound is an indivisible unit of history:
//   - an assistant message carrying tool calls plus the following pure tool-result user
//     messages whose call ids it owns;
//   - any other user/assistant text message on its own.
type agenticRound struct {
	messages []*schema.AgenticMessage
}

func isAgenticAssistantToolCall(msg *schema.AgenticMessage) bool {
	if msg == nil || msg.Role != schema.AgenticRoleTypeAssistant {
		return false
	}
	for _, blk := range msg.ContentBlocks {
		if blk != nil && blk.Type == schema.ContentBlockTypeFunctionToolCall {
			return true
		}
	}
	return false
}

func isAgenticPureToolResult(msg *schema.AgenticMessage) bool {
	if msg == nil || msg.Role != schema.AgenticRoleTypeUser || len(msg.ContentBlocks) == 0 {
		return false
	}
	for _, blk := range msg.ContentBlocks {
		if blk == nil || blk.Type != schema.ContentBlockTypeFunctionToolResult {
			return false
		}
	}
	return true
}

func agenticToolCallIDs(msg *schema.AgenticMessage) []string {
	var ids []string
	for _, blk := range msg.ContentBlocks {
		if blk != nil && blk.Type == schema.ContentBlockTypeFunctionToolCall && blk.FunctionToolCall != nil {
			if id := strings.TrimSpace(blk.FunctionToolCall.CallID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func resultCallIDs(msg *schema.AgenticMessage) []string {
	var ids []string
	for _, blk := range msg.ContentBlocks {
		if blk != nil && blk.Type == schema.ContentBlockTypeFunctionToolResult && blk.FunctionToolResult != nil {
			if id := strings.TrimSpace(blk.FunctionToolResult.CallID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// splitAgenticRounds groups non-system messages into rounds. An orphan tool result is dropped
// rather than kept: an unpaired result makes the provider reject the request, and the orphan
// pruner middleware owns repairing that case.
func splitAgenticRounds(msgs []*schema.AgenticMessage) []agenticRound {
	if len(msgs) == 0 {
		return nil
	}
	rounds := make([]agenticRound, 0, len(msgs))
	i := 0
	for i < len(msgs) {
		msg := msgs[i]
		if msg == nil {
			i++
			continue
		}
		if isAgenticAssistantToolCall(msg) {
			provided := map[string]struct{}{}
			for _, id := range agenticToolCallIDs(msg) {
				provided[id] = struct{}{}
			}
			round := agenticRound{messages: []*schema.AgenticMessage{msg}}
			j := i + 1
			for j < len(msgs) {
				next := msgs[j]
				if next == nil {
					j++
					continue
				}
				if !isAgenticPureToolResult(next) {
					break
				}
				owned := false
				for _, id := range resultCallIDs(next) {
					if _, ok := provided[id]; ok {
						owned = true
						break
					}
				}
				if !owned {
					break
				}
				round.messages = append(round.messages, next)
				j++
			}
			rounds = append(rounds, round)
			i = j
			continue
		}
		if isAgenticPureToolResult(msg) {
			i++
			continue
		}
		rounds = append(rounds, agenticRound{messages: []*schema.AgenticMessage{msg}})
		i++
	}
	return rounds
}
