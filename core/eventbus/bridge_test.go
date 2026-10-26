package eventbus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"finduai/core/provider"
	"finduai/core/provider/mock"
)

func TestPublishProviderEvent(t *testing.T) {
	b := New()
	defer b.Close()
	sub, err := b.Subscribe(Filter{Types: []Type{TypeC2SessionOpened}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	env, err := b.PublishProviderEvent(context.Background(), "c2-plugin", provider.Event{
		Seq: 7, Kind: provider.EventC2SessionOpened, At: at,
		Data: map[string]any{"session_id": "sess-1", "host": "10.0.0.8"},
	})
	if err != nil {
		t.Fatalf("PublishProviderEvent: %v", err)
	}
	if env.ID != "c2-plugin:7" || env.IdempotencyKey != "c2-plugin:7" {
		t.Fatalf("id=%q key=%q", env.ID, env.IdempotencyKey)
	}
	if env.Type != TypeC2SessionOpened || env.SessionID != "sess-1" || env.Delivery != AtLeastOnce {
		t.Fatalf("env=%+v", env)
	}
	if env.TS != at || env.Seq != 1 || env.Payload["host"] != "10.0.0.8" {
		t.Fatalf("env=%+v", env)
	}
	if got := mustRecv(t, sub.C()); got.ID != "c2-plugin:7" {
		t.Fatalf("sub=%+v", got)
	}
}

func TestPublishProviderEventRejectsBadInput(t *testing.T) {
	b := New()
	defer b.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	cases := []struct {
		name   string
		source string
		ev     provider.Event
		is     error
	}{
		{"空 source", "", provider.Event{Seq: 1, Kind: provider.EventC2SessionOpened, At: now}, ErrInvalidEnvelope},
		{"seq=0", "p", provider.Event{Seq: 0, Kind: provider.EventC2SessionOpened, At: now}, ErrInvalidEnvelope},
		{"未知 kind", "p", provider.Event{Seq: 1, Kind: "c2.nope", At: now}, ErrUnknownType},
	}
	for _, c := range cases {
		if _, err := b.PublishProviderEvent(ctx, c.source, c.ev); !errors.Is(err, c.is) {
			t.Fatalf("%s: err=%v want=%v", c.name, err, c.is)
		}
	}
	// At 为零时补当前时间。
	env, err := b.PublishProviderEvent(ctx, "p", provider.Event{Seq: 1, Kind: provider.EventC2ListenerStarted})
	if err != nil || env.TS.IsZero() {
		t.Fatalf("零 At: env=%+v err=%v", env, err)
	}
}

func TestBridgeProviderChannel(t *testing.T) {
	b := New()
	defer b.Close()
	sub, err := b.Subscribe(Filter{}, 4)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ch := make(chan provider.Event, 2)
	ch <- provider.Event{Seq: 1, Kind: provider.EventC2ListenerStarted, At: now, Data: map[string]any{"listener_id": "l1"}}
	ch <- provider.Event{Seq: 2, Kind: provider.EventC2SessionOpened, At: now, Data: map[string]any{"session_id": "s1"}}
	close(ch)

	if err := Bridge(context.Background(), b, "mock-c2", ch); err != nil {
		t.Fatalf("Bridge: %v", err)
	}
	first := mustRecv(t, sub.C())
	second := mustRecv(t, sub.C())
	if first.ID != "mock-c2:1" || second.ID != "mock-c2:2" {
		t.Fatalf("ids=%s,%s", first.ID, second.ID)
	}

	// 未知事件在桥接层失败，不静默丢弃。
	bad := make(chan provider.Event, 1)
	bad <- provider.Event{Seq: 1, Kind: "c2.nope", At: now}
	if err := Bridge(context.Background(), b, "mock-c2", bad); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("bad bridge err=%v", err)
	}
}

func TestBridgeContextCancel(t *testing.T) {
	b := New()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan provider.Event) // 永不关闭、无事件
	if err := Bridge(ctx, b, "p", ch); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderEventNamesCovered(t *testing.T) {
	kinds := []string{
		provider.EventC2ListenerStarted,
		provider.EventC2ListenerStopped,
		provider.EventC2SessionOpened,
		provider.EventC2SessionClosed,
		provider.EventC2TaskDispatched,
		provider.EventC2TaskCompleted,
	}
	for _, kind := range kinds {
		if !Type(kind).Valid() {
			t.Fatalf("provider 事件 %q 未登记到 event-contract", kind)
		}
	}
}

// 端到端：mock C2 产生的真实事件 → 桥接 → 订阅者按类型过滤收到。
func TestBridgeMockC2(t *testing.T) {
	b := New()
	defer b.Close()
	sub, err := b.Subscribe(Filter{Types: []Type{TypeC2SessionOpened}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	c2 := mock.NewC2()
	ctx := context.Background()
	if _, err := c2.StartListener(ctx, provider.ListenerSpec{Address: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	c2.AddSession(provider.Session{ID: "sess-42", Host: "10.0.0.9"})
	events, err := c2.Events(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := Bridge(ctx, b, "mock-c2", events); err != nil {
		t.Fatalf("Bridge: %v", err)
	}
	got := mustRecv(t, sub.C())
	if got.Type != TypeC2SessionOpened || got.SessionID != "sess-42" {
		t.Fatalf("event=%+v", got)
	}
	if !strings.HasPrefix(got.ID, "mock-c2:") || got.Payload["host"] != "10.0.0.9" {
		t.Fatalf("event=%+v", got)
	}
}
