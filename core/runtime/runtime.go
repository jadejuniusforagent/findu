// Package runtime 定义 agent 运行时的对外 I/O 边界（runtime-spi）。
//
// 契约见 contracts/runtime-spi.md。运行时只关心会话、回合、事件、模型调用、
// 工具调用、规划与快照，不抽象规划算法、上下文压缩或 prompt 策略；这些留在
// adapter 内。换运行时只换 adapter，core 与前端不动。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"finduai/core/eventbus"
)

// SessionID / TurnID 是运行时侧的不透明标识。core 负责 canonical 落库，
// adapter 只负责在其隔离边界内创建与引用。
type (
	SessionID string
	TurnID    string
)

// Role 是回合/消息的角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// Valid 报告角色是否在契约枚举内。
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleSystem, RoleTool:
		return true
	}
	return false
}

// SessionSpec 描述一个待创建的运行时会话。
type SessionSpec struct {
	ProjectID string         `json:"project_id,omitempty"`
	Title     string         `json:"title,omitempty"`
	Options   map[string]any `json:"options,omitempty"` // adapter 私有参数透传位
}

// Turn 是一个回合的内容。TurnID 由 AppendTurn 返回，不要求调用方预填。
// 回合粒度 = 一次用户输入到 agent 停止；adapter 只提交内容，canonical 落库由 core 负责。
type Turn struct {
	Role      Role           `json:"role"`
	Content   string         `json:"content"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Event 是事件契约的 Go 映射。直接复用 eventbus.Envelope，避免 runtime-spi
// 与 event-contract 出现两套字段；seq/type/ts 的校验由 eventbus 统一负责。
type Event = eventbus.Envelope

// Message 是模型输入的 provider 中立消息。Turn 是 canonical 回合，Message 是
// 模型上下文中的单条消息，两者不混用。
type Message struct {
	Role       Role   `json:"role"`
	Content    string `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolSpec 是模型可调用工具的形状；具体工具实现仍走 CallTool。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ModelRequest 是一次模型调用。tools schema 由 core 传入，adapter 不自行发现工具。
type ModelRequest struct {
	SessionID   SessionID      `json:"session_id"`
	TurnID      TurnID         `json:"turn_id,omitempty"`
	Model       string         `json:"model,omitempty"`
	Messages    []Message      `json:"messages"`
	Tools       []ToolSpec     `json:"tools,omitempty"`
	Temperature float64        `json:"temperature,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Usage 是模型调用的 token 统计。零值表示 adapter 未提供。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// ModelResponse 是一次模型调用的结果。工具调用通过 ToolCalls 返回，由 core
// 决定是否交给 CallTool。
type ModelResponse struct {
	Message      Message    `json:"message"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        Usage      `json:"usage,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

// ToolCall 是一次工具调用请求。Arguments 保持原始 JSON，避免不同工具 schema
// 在 runtime 层被二次编码。
type ToolCall struct {
	ID        string          `json:"id"`
	SessionID SessionID       `json:"session_id,omitempty"`
	TurnID    TurnID          `json:"turn_id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
}

// ToolResult 是一次工具调用的结果。
type ToolResult struct {
	CallID   string         `json:"call_id,omitempty"`
	Content  string         `json:"content"`
	IsError  bool           `json:"is_error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Goal 是规划输入。State 是 adapter 的当前状态快照，可为零值。
type Goal struct {
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// State 是中断恢复与上下文压缩用的 opaque 状态。Data 由 adapter 定义，
// core 只负责透传与持久化。
type State struct {
	SessionID SessionID       `json:"session_id,omitempty"`
	Version   int             `json:"version,omitempty"`
	Seq       int64           `json:"seq,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Intent 是一个规划意图。runtime 只要求返回意图列表，不规定拓扑与算法；
// 单 agent adapter 可以返回一个退化意图。
type Intent struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind,omitempty"`
	Goal   string         `json:"goal"`
	Inputs map[string]any `json:"inputs,omitempty"`
}

// Capabilities 是 Health 的能力声明。Snapshot=false 时调用方不得依赖
// Snapshotter；EventTypes 必须全部在 event-contract 枚举内。
type Capabilities struct {
	Streaming       bool            `json:"streaming"`
	ParallelWorkers bool            `json:"parallel_workers"`
	Snapshot        bool            `json:"snapshot"`
	EventTypes      []eventbus.Type `json:"event_types"`
}

// Runtime 是 runtime-spi 的必选接口。Plan 允许退化实现（单意图）；
// 不支持的实现必须返回 ErrUnsupported，而不是静默返回空结果。
//
// Snapshot/Restore 是可选能力，单独由 Snapshotter 声明，避免强制所有
// adapter 实现空方法；是否支持以 Health().Snapshot 为准。
type Runtime interface {
	CreateSession(ctx context.Context, spec SessionSpec) (SessionID, error)
	AppendTurn(ctx context.Context, sessionID SessionID, turn Turn) (TurnID, error)
	StreamEvents(ctx context.Context, sessionID SessionID, fromSeq int64) (<-chan Event, error)
	InvokeModel(ctx context.Context, req ModelRequest) (ModelResponse, error)
	CallTool(ctx context.Context, call ToolCall) (ToolResult, error)
	Plan(ctx context.Context, goal Goal, state State) ([]Intent, error)
	Health(ctx context.Context) (Capabilities, error)
}

// Snapshotter 是可选的中断恢复能力。实现 Snapshotter 时 Health 必须声明
// Snapshot=true；不实现时调用方只能依赖 CreateSession/AppendTurn 重建。
type Snapshotter interface {
	Snapshot(ctx context.Context, sessionID SessionID) (State, error)
	Restore(ctx context.Context, sessionID SessionID, state State) error
}

// 可判定错误。调用方用 errors.Is 判断；可重试错误用 Retryable 包装。
var (
	ErrNotFound    = errors.New("runtime: not found")
	ErrInvalid     = errors.New("runtime: invalid argument")
	ErrConflict    = errors.New("runtime: conflict")
	ErrUnsupported = errors.New("runtime: unsupported")
	ErrRetryable   = errors.New("runtime: retryable")
)

// retryableError 同时暴露 ErrRetryable 与内部错误，便于 errors.Is 同时判断
// 「可重试」和「具体原因」。
type retryableError struct {
	err error
}

func (e *retryableError) Error() string { return fmt.Sprintf("%v: %v", ErrRetryable, e.err) }
func (e *retryableError) Unwrap() error { return e.err }
func (e *retryableError) Is(target error) bool {
	return target == ErrRetryable
}

// Retryable 包装一个可重试错误；nil 原样返回。
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return &retryableError{err: err}
}

// IsRetryable 报告 err 是否由 Retryable 包装。
func IsRetryable(err error) bool { return errors.Is(err, ErrRetryable) }
