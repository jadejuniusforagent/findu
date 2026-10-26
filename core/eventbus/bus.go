package eventbus

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Filter 是订阅过滤器。零值表示全收；Types 为空表示不限类型，
// SessionID 为空表示不限会话。
type Filter struct {
	Types     []Type
	SessionID string
}

func (f Filter) matches(e Envelope) bool {
	if f.SessionID != "" && e.SessionID != f.SessionID {
		return false
	}
	if len(f.Types) == 0 {
		return true
	}
	for _, t := range f.Types {
		if t == e.Type {
			return true
		}
	}
	return false
}

// Subscription 是订阅句柄。Close 可重复调用；Close 后 channel 关闭。
type Subscription struct {
	id        string
	bus       *Bus
	filter    Filter
	ch        chan Envelope
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

// ID 返回订阅标识（由总线分配）。
func (s *Subscription) ID() string { return s.id }

// C 返回只读事件通道；订阅者应持续读取，否则 Publish 会在缓冲满时阻塞。
func (s *Subscription) C() <-chan Envelope { return s.ch }

// Close 注销订阅并关闭 channel。若 Publish 正阻塞在该订阅上，先关闭 done
// 让它立即返回 ErrClosed，再关 channel，避免死锁与 send on closed channel。
func (s *Subscription) Close() {
	s.closeOnce.Do(func() { close(s.done) })
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	s.mu.Unlock()
	if s.bus != nil {
		s.bus.remove(s.id, s)
	}
}

// deliver 在订阅锁内投递。缓冲满时等待 ctx；ctx 到期返回 ErrSlowConsumer，
// 调用方可用同一 Envelope 重试（至少一次语义，重复由 idempotency_key 去重）。
func (s *Subscription) deliver(ctx context.Context, e Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	select {
	case s.ch <- e:
		return nil
	case <-s.done:
		return ErrClosed
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrSlowConsumer, ctx.Err())
	}
}

// Bus 是进程内事件总线。
//
// 语义：Publish 在返回 nil 时，所有匹配订阅者的缓冲都已接收该事件，即
// 「至少一次」；返回 ErrSlowConsumer 时调用方可重试。总线不做跨进程/重启
// 持久化，进程退出即丢失未消费事件。
type Bus struct {
	mu      sync.RWMutex
	subs    map[string]*Subscription
	closed  bool
	nextSeq atomic.Int64
	nextSub atomic.Int64
}

// New 创建空总线。
func New() *Bus {
	return &Bus{subs: map[string]*Subscription{}}
}

// Subscribe 注册订阅者。bufferSize<=0 时用 64。
func (b *Bus) Subscribe(filter Filter, bufferSize int) (*Subscription, error) {
	for _, t := range filter.Types {
		if !t.Valid() {
			return nil, fmt.Errorf("%w: %q: %w", ErrInvalidEnvelope, t, ErrUnknownType)
		}
	}
	if bufferSize <= 0 {
		bufferSize = 64
	}
	id := fmt.Sprintf("sub-%d", b.nextSub.Add(1))
	s := &Subscription{
		id: id, bus: b, filter: filter,
		ch:   make(chan Envelope, bufferSize),
		done: make(chan struct{}),
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(s.ch)
		return nil, ErrClosed
	}
	b.subs[id] = s
	return s, nil
}

// Publish 校验并广播事件，返回补齐 seq/delivery 后的最终 Envelope。
//
// seq 由总线统一分配（从 1 开始），调用方传入的 Seq 会被覆盖；id/ts/type 由
// 调用方负责。ctx 用于限制慢订阅者造成的阻塞。
//
// 多订阅者时，若投递到第 N 个订阅者失败，前 N-1 个已收到该事件；调用方重试会
// 造成重复投递（至少一次语义），消费方按 IdempotencyKey 去重。
func (b *Bus) Publish(ctx context.Context, env Envelope) (Envelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	env = env.normalized()
	env.Seq = b.nextSeq.Add(1)
	if err := env.Validate(); err != nil {
		return env, err
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return env, ErrClosed
	}
	subs := make([]*Subscription, 0, len(b.subs))
	for _, s := range b.subs {
		if s.filter.matches(env) {
			subs = append(subs, s)
		}
	}
	b.mu.RUnlock()

	for _, s := range subs {
		if err := s.deliver(ctx, env); err != nil {
			return env, err
		}
	}
	return env, nil
}

// Close 关闭总线并关闭全部订阅 channel；可重复调用。关闭后 Publish/Subscribe
// 返回 ErrClosed。
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*Subscription, 0, len(b.subs))
	for id, s := range b.subs {
		delete(b.subs, id)
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}

func (b *Bus) remove(id string, target *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.subs[id]; ok && s == target {
		delete(b.subs, id)
	}
}
