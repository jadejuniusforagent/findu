package eventbus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func mustRecv(t *testing.T, ch <-chan Envelope) Envelope {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("channel 已关闭")
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("等待事件超时")
		return Envelope{}
	}
}

func waitClosed(t *testing.T, ch <-chan Envelope) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("期望 channel 关闭，仍收到事件")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 channel 关闭超时")
	}
}

func TestPublishFilterAndSeq(t *testing.T) {
	b := New()
	defer b.Close()
	ctx := context.Background()

	all, err := b.Subscribe(Filter{}, 4)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := b.Subscribe(Filter{Types: []Type{TypeC2SessionOpened}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Subscribe(Filter{SessionID: "s1"}, 4)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	first, err := b.Publish(ctx, Envelope{ID: "e1", Type: TypeSessionCreated, TS: now, SessionID: "s1"})
	if err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if first.Seq != 1 || first.Delivery != AtLeastOnce {
		t.Fatalf("seq/delivery 未补齐: %+v", first)
	}
	second, err := b.Publish(ctx, Envelope{ID: "e2", Type: TypeC2SessionOpened, TS: now, SessionID: "s2", Delivery: AtMostOnce})
	if err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if second.Seq != 2 {
		t.Fatalf("seq=%d", second.Seq)
	}

	if got := mustRecv(t, all.C()); got.ID != "e1" {
		t.Fatalf("all 第一条=%s", got.ID)
	}
	if got := mustRecv(t, all.C()); got.ID != "e2" {
		t.Fatalf("all 第二条=%s", got.ID)
	}
	if got := mustRecv(t, typed.C()); got.ID != "e2" {
		t.Fatalf("typed 应只收 e2，实际 %s", got.ID)
	}
	if got := mustRecv(t, session.C()); got.ID != "e1" {
		t.Fatalf("session 应只收 e1，实际 %s", got.ID)
	}
}

func TestPublishValidationAndClosed(t *testing.T) {
	b := New()
	ctx := context.Background()

	if _, err := b.Publish(ctx, Envelope{ID: "x", Type: "bad", TS: time.Now()}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("未知类型 err=%v", err)
	}
	if _, err := b.Subscribe(Filter{Types: []Type{"bad"}}, 1); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("订阅未知类型 err=%v", err)
	}

	sub, err := b.Subscribe(Filter{}, 0) // 0 走默认 64
	if err != nil {
		t.Fatal(err)
	}
	if cap(sub.C()) != 64 {
		t.Fatalf("默认 buffer=%d", cap(sub.C()))
	}
	sub.Close()
	waitClosed(t, sub.C())
	sub.Close() // 幂等

	// 无订阅者时 Publish 仍成功并分配 seq；nil ctx 不应 panic。
	if _, err := b.Publish(nil, Envelope{ID: "e", Type: TypeError, TS: time.Now()}); err != nil {
		t.Fatalf("无订阅者 publish: %v", err)
	}

	b.Close()
	b.Close()
	if _, err := b.Publish(ctx, Envelope{ID: "e", Type: TypeError, TS: time.Now()}); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后 publish err=%v", err)
	}
	if _, err := b.Subscribe(Filter{}, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后 subscribe err=%v", err)
	}
}

func TestBackpressureAndRetry(t *testing.T) {
	b := New()
	defer b.Close()
	sub, err := b.Subscribe(Filter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := b.Publish(ctx, Envelope{ID: "e1", Type: TypeError, TS: now}); err != nil {
		t.Fatalf("第一条: %v", err)
	}

	// 缓冲已满且无人消费：带超时的 Publish 必须返回 ErrSlowConsumer，而不是永久阻塞。
	slow, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := b.Publish(slow, Envelope{ID: "e2", Type: TypeError, TS: now}); !errors.Is(err, ErrSlowConsumer) {
		t.Fatalf("缓冲满 err=%v", err)
	}
	if !errors.Is(slow.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx err=%v", slow.Err())
	}

	// 消费一条后重试成功（至少一次：失败的事件可原样重投）。
	if got := mustRecv(t, sub.C()); got.ID != "e1" {
		t.Fatalf("first=%s", got.ID)
	}
	if _, err := b.Publish(ctx, Envelope{ID: "e2", Type: TypeError, TS: now}); err != nil {
		t.Fatalf("重试: %v", err)
	}
	if got := mustRecv(t, sub.C()); got.ID != "e2" {
		t.Fatalf("retry=%s", got.ID)
	}
}

func TestCloseUnblocksPublish(t *testing.T) {
	b := New()
	defer b.Close()
	sub, err := b.Subscribe(Filter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := b.Publish(ctx, Envelope{ID: "e1", Type: TypeError, TS: now}); err != nil {
		t.Fatal(err)
	}

	// 缓冲已满，Publish 用 Background 阻塞；Close 订阅必须让它立即返回，
	// 而不是等一个永远不会到的 ctx 超时。
	result := make(chan error, 1)
	go func() {
		_, err := b.Publish(ctx, Envelope{ID: "e2", Type: TypeError, TS: now})
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	sub.Close()
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close 未解除 Publish 阻塞")
	}
}

func TestConcurrentPublish(t *testing.T) {
	b := New()
	defer b.Close()
	const publishers, each, total = 8, 50, 400
	sub, err := b.Subscribe(Filter{}, total)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				if _, err := b.Publish(context.Background(), Envelope{
					ID: "e", Type: TypeError, TS: time.Now().UTC(),
				}); err != nil {
					t.Errorf("publish: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	seen := map[int64]bool{}
	for i := 0; i < total; i++ {
		e := mustRecv(t, sub.C())
		if seen[e.Seq] {
			t.Fatalf("seq 重复: %d", e.Seq)
		}
		seen[e.Seq] = true
	}
	if len(seen) != total {
		t.Fatalf("收到 %d 条", len(seen))
	}
}
