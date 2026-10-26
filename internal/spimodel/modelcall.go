// Package spimodel contains the Eino model-call plumbing shared by the
// runtime-spi adapters. It is not a public runtime: adapters own session and
// event semantics, this package only converts SPI messages/tools and performs
// one model call.
package spimodel

import (
	"context"
	"encoding/json"
	"fmt"

	"finduai/acllm"
	"finduai/core/runtime"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// SchemaMessages converts provider-neutral runtime messages into Eino messages.
// System and tool messages are constructed directly; user/assistant text goes
// through acllm so the canonical DTO vocabulary stays the only conversion seam.
func SchemaMessages(msgs []runtime.Message) ([]*schema.Message, error) {
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		if !m.Role.Valid() {
			return nil, fmt.Errorf("%w: message role %q", runtime.ErrInvalid, m.Role)
		}
		switch m.Role {
		case runtime.RoleSystem:
			out = append(out, schema.SystemMessage(m.Content))
		case runtime.RoleTool:
			if m.ToolCallID == "" {
				return nil, fmt.Errorf("%w: tool message 缺少 tool_call_id", runtime.ErrInvalid)
			}
			out = append(out, schema.ToolMessage(m.Content, m.ToolCallID))
		case runtime.RoleUser:
			out = append(out, acllm.ToSchema([]acllm.Message{acllm.UserText(m.Content)})...)
		case runtime.RoleAssistant:
			dto := acllm.Message{
				Role:    acllm.RoleAssistant,
				Content: []acllm.ContentBlock{acllm.TextBlock(m.Content)},
			}
			out = append(out, acllm.ToSchema([]acllm.Message{dto})...)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: messages 不能为空", runtime.ErrInvalid)
	}
	return out, nil
}

// ToolInfos converts SPI tool specs into Eino tool definitions.
func ToolInfos(specs []runtime.ToolSpec) ([]*schema.ToolInfo, error) {
	out := make([]*schema.ToolInfo, 0, len(specs))
	for _, s := range specs {
		if s.Name == "" {
			return nil, fmt.Errorf("%w: tool spec name 不能为空", runtime.ErrInvalid)
		}
		var schemaMap map[string]any
		if len(s.InputSchema) > 0 {
			if err := json.Unmarshal(s.InputSchema, &schemaMap); err != nil {
				return nil, fmt.Errorf("%w: tool %s input schema: %v", runtime.ErrInvalid, s.Name, err)
			}
		}
		info, err := acllm.ToolSchemaToInfo(acllm.ToolSchema{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: schemaMap,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: tool %s input schema: %v", runtime.ErrInvalid, s.Name, err)
		}
		out = append(out, info)
	}
	return out, nil
}

// InvokeModel performs one blocking model call and maps the Eino response onto
// the runtime-spi response shape.
func InvokeModel(ctx context.Context, m model.ToolCallingChatModel, req runtime.ModelRequest) (runtime.ModelResponse, error) {
	if m == nil {
		return runtime.ModelResponse{}, fmt.Errorf("%w: model is nil", runtime.ErrUnsupported)
	}
	input, err := SchemaMessages(req.Messages)
	if err != nil {
		return runtime.ModelResponse{}, err
	}
	if len(req.Tools) > 0 {
		infos, err := ToolInfos(req.Tools)
		if err != nil {
			return runtime.ModelResponse{}, err
		}
		m, err = m.WithTools(infos)
		if err != nil {
			return runtime.ModelResponse{}, fmt.Errorf("%w: bind tools: %v", runtime.ErrInvalid, err)
		}
	}

	var opts []model.Option
	if req.Model != "" {
		opts = append(opts, model.WithModel(req.Model))
	}
	if req.Temperature != 0 {
		opts = append(opts, model.WithTemperature(float32(req.Temperature)))
	}
	if req.MaxTokens > 0 {
		opts = append(opts, model.WithMaxTokens(req.MaxTokens))
	}

	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		if ctx.Err() != nil {
			return runtime.ModelResponse{}, ctx.Err()
		}
		return runtime.ModelResponse{}, runtime.Retryable(err)
	}
	if msg == nil {
		return runtime.ModelResponse{}, fmt.Errorf("%w: model returned nil message", runtime.ErrConflict)
	}
	return Response(req, msg), nil
}

// Response maps an Eino model message onto a runtime-spi response.
func Response(req runtime.ModelRequest, msg *schema.Message) runtime.ModelResponse {
	resp := runtime.ModelResponse{
		Message:      runtime.Message{Role: runtime.RoleAssistant, Content: msg.Content},
		FinishReason: "stop",
	}
	if msg.ResponseMeta != nil {
		if msg.ResponseMeta.FinishReason != "" {
			resp.FinishReason = msg.ResponseMeta.FinishReason
		}
		if msg.ResponseMeta.Usage != nil {
			u := acllm.UsageFromToken(msg.ResponseMeta.Usage)
			resp.Usage = runtime.Usage{
				PromptTokens:     u.InputTokens,
				CompletionTokens: u.OutputTokens,
				TotalTokens:      u.InputTokens + u.OutputTokens,
			}
		}
	}
	for _, tc := range msg.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		resp.ToolCalls = append(resp.ToolCalls, runtime.ToolCall{
			ID:        tc.ID,
			SessionID: req.SessionID,
			TurnID:    req.TurnID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return resp
}
