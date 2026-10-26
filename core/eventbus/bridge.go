package eventbus

import (
	"context"
	"fmt"
	"time"

	"finduai/core/provider"
)

// PublishProviderEvent 把 provider 事件转成契约 Envelope 后投递。
//
// 映射规则（与 contracts/capability-providers.md §一.5 一致）：
//   - type  = provider.Event.Kind（必须是契约枚举中的 <capability>.<verb>）；
//   - id / idempotency_key = "<source>:<seq>"，同一 provider 的重复投递可去重；
//   - ts    = provider.Event.At（零值时补当前时间）；
//   - session_id 从 Data["session_id"] 提取（有则填）；
//   - payload = Data，delivery = at_least_once。
//
// provider.Event.Seq 是 provider 侧单调序号，必须 >0；否则拒绝，避免生成重复 id。
func (b *Bus) PublishProviderEvent(ctx context.Context, source string, ev provider.Event) (Envelope, error) {
	if source == "" {
		return Envelope{}, fmt.Errorf("%w: provider source 不能为空", ErrInvalidEnvelope)
	}
	if ev.Seq <= 0 {
		return Envelope{}, fmt.Errorf("%w: provider event seq 必须 >0", ErrInvalidEnvelope)
	}
	if !Type(ev.Kind).Valid() {
		return Envelope{}, fmt.Errorf("%w: %q: %w", ErrInvalidEnvelope, ev.Kind, ErrUnknownType)
	}
	ts := ev.At
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	id := fmt.Sprintf("%s:%d", source, ev.Seq)
	sessionID, _ := ev.Data["session_id"].(string)
	return b.Publish(ctx, Envelope{
		ID:             id,
		Type:           Type(ev.Kind),
		TS:             ts,
		SessionID:      sessionID,
		Delivery:       AtLeastOnce,
		IdempotencyKey: id,
		Payload:        ev.Data,
	})
}

// Bridge 持续把 provider 事件流转发到总线，直到 events 关闭或 ctx 取消。
// channel 正常关闭返回 nil；ctx 取消返回 ctx.Err()。
func Bridge(ctx context.Context, bus *Bus, source string, events <-chan provider.Event) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if _, err := bus.PublishProviderEvent(ctx, source, ev); err != nil {
				return err
			}
		}
	}
}
