package eventbus

import (
	"errors"
	"fmt"
	"time"
)

// Type 是事件类型。取值必须与 contracts/event-contract.schema.json 的
// `type` 枚举完全一致；TestTypesMatchContract 会读取 schema 做双向比对，
// 防止 Go 侧先加类型、契约没跟上。
type Type string

// Agent 运行时事件（契约初版已冻结）。
const (
	TypeSessionCreated  Type = "session.created"
	TypeSessionClosed   Type = "session.closed"
	TypeTurnCreated     Type = "turn.created"
	TypeTokenDelta      Type = "token.delta"
	TypeToolCallStarted Type = "tool.call.started"
	TypeToolCallFinish  Type = "tool.call.finished"
	TypePlanUpdated     Type = "plan.updated"
	TypeArtifactCreated Type = "artifact.created"
	TypeError           Type = "error"
)

// Provider 事件（Step 4 收尾与 core/provider 的事件名常量对齐）。
const (
	TypeC2ListenerStarted Type = "c2.listener.started"
	TypeC2ListenerStopped Type = "c2.listener.stopped"
	TypeC2SessionOpened   Type = "c2.session.opened"
	TypeC2SessionClosed   Type = "c2.session.closed"
	TypeC2TaskDispatched  Type = "c2.task.dispatched"
	TypeC2TaskCompleted   Type = "c2.task.completed"
)

// Delivery 是投递语义，对应 schema 的 delivery 枚举。
type Delivery string

const (
	AtLeastOnce Delivery = "at_least_once"
	AtMostOnce  Delivery = "at_most_once"
)

var allTypes = []Type{
	TypeSessionCreated, TypeSessionClosed, TypeTurnCreated, TypeTokenDelta,
	TypeToolCallStarted, TypeToolCallFinish, TypePlanUpdated, TypeArtifactCreated, TypeError,
	TypeC2ListenerStarted, TypeC2ListenerStopped, TypeC2SessionOpened,
	TypeC2SessionClosed, TypeC2TaskDispatched, TypeC2TaskCompleted,
}

var validTypes = func() map[Type]struct{} {
	m := make(map[Type]struct{}, len(allTypes))
	for _, t := range allTypes {
		m[t] = struct{}{}
	}
	return m
}()

// Valid 报告事件类型是否在契约枚举内。
func (t Type) Valid() bool {
	_, ok := validTypes[t]
	return ok
}

// AllTypes 返回全部合法类型（副本，调用方可随意修改）。
func AllTypes() []Type {
	out := make([]Type, len(allTypes))
	copy(out, allTypes)
	return out
}

// Valid 报告投递语义是否合法。
func (d Delivery) Valid() bool {
	return d == AtLeastOnce || d == AtMostOnce
}

// Envelope 是事件契约的 Go 映射。Seq 由 Bus.Publish 统一分配，调用方填 0 即可；
// ID/TS 由生产方负责（Provider 桥接会自动补齐 TS）。
type Envelope struct {
	ID             string         `json:"id"`
	Type           Type           `json:"type"`
	TS             time.Time      `json:"ts"`
	Seq            int64          `json:"seq"`
	SessionID      string         `json:"session_id,omitempty"`
	TurnID         string         `json:"turn_id,omitempty"`
	Delivery       Delivery       `json:"delivery,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
}

// 事件总线错误。调用方用 errors.Is 判断。
var (
	ErrClosed          = errors.New("eventbus: closed")
	ErrInvalidEnvelope = errors.New("eventbus: invalid envelope")
	ErrUnknownType     = errors.New("eventbus: unknown event type")
	ErrSlowConsumer    = errors.New("eventbus: subscriber buffer full")
)

// Validate 校验契约必填项与枚举。Delivery 为空时按 at_least_once 处理。
func (e Envelope) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("%w: id 不能为空", ErrInvalidEnvelope)
	}
	if !e.Type.Valid() {
		return fmt.Errorf("%w: %q: %w", ErrInvalidEnvelope, e.Type, ErrUnknownType)
	}
	if e.TS.IsZero() {
		return fmt.Errorf("%w: ts 不能为零值", ErrInvalidEnvelope)
	}
	if e.Seq < 0 {
		return fmt.Errorf("%w: seq 不能为负", ErrInvalidEnvelope)
	}
	if e.Delivery != "" && !e.Delivery.Valid() {
		return fmt.Errorf("%w: delivery=%q", ErrInvalidEnvelope, e.Delivery)
	}
	return nil
}

// normalized 返回补齐默认投递语义的副本；不修改调用方传入的值。
func (e Envelope) normalized() Envelope {
	if e.Delivery == "" {
		e.Delivery = AtLeastOnce
	}
	return e
}
