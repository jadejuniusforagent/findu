// Package mock 提供 C2 / WebShell / 流量的内存实现，供契约测试与本地演练。
// 契约要求（contracts/capability-providers.md §一.3）：每个 provider 都要有内存
// mock，CI 用 mock 跑通链路，证明「换 provider 不换 core」。
package mock

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"

	"finduai/core/provider"
)

// C2 是内存 C2 provider。任务下发即完成（responder 可定制输出），
// 适合做确定性契约测试，不模拟真实延迟与异步回连。
type C2 struct {
	mu        sync.Mutex
	seq       int64
	listeners map[string]provider.Listener
	sessions  map[string]provider.Session
	tasks     map[string]provider.Task
	results   map[string]provider.TaskResult
	artifacts []provider.Artifact
	events    []provider.Event
	responder func(provider.TaskSpec) provider.TaskResult
}

// NewC2 创建空 C2 mock。
func NewC2() *C2 {
	return &C2{
		listeners: map[string]provider.Listener{},
		sessions:  map[string]provider.Session{},
		tasks:     map[string]provider.Task{},
		results:   map[string]provider.TaskResult{},
	}
}

// SetResponder 定制任务结果；不设置时返回 "mock: <command>"、exit 0。
func (m *C2) SetResponder(fn func(provider.TaskSpec) provider.TaskResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responder = fn
}

// AddSession 注入一条已上线会话（真实实现由回连事件产生）。
func (m *C2) AddSession(s provider.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == "" {
		s.ID = m.nextID("sess")
	}
	if s.Status == "" {
		s.Status = "active"
	}
	if s.LastSeen.IsZero() {
		s.LastSeen = time.Now()
	}
	m.sessions[s.ID] = s
	m.record(provider.EventC2SessionOpened, map[string]any{"session_id": s.ID, "host": s.Host})
}

func (m *C2) StartListener(_ context.Context, spec provider.ListenerSpec) (provider.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := provider.Listener{ID: m.nextID("lst"), Spec: spec, Status: "running", CreatedAt: time.Now()}
	m.listeners[l.ID] = l
	m.record(provider.EventC2ListenerStarted, map[string]any{"listener_id": l.ID, "address": spec.Address})
	return l, nil
}

func (m *C2) StopListener(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.listeners[id]
	if !ok {
		return fmt.Errorf("%w: listener %s", provider.ErrNotFound, id)
	}
	l.Status = "stopped"
	m.listeners[id] = l
	m.record(provider.EventC2ListenerStopped, map[string]any{"listener_id": id})
	return nil
}

func (m *C2) ListListeners(context.Context) ([]provider.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.listeners))
	for id := range m.listeners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]provider.Listener, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.listeners[id])
	}
	return out, nil
}

func (m *C2) ListSessions(_ context.Context, f provider.SessionFilter) ([]provider.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]provider.Session, 0, len(ids))
	for _, id := range ids {
		s := m.sessions[id]
		if f.ListenerID != "" && s.ListenerID != f.ListenerID {
			continue
		}
		if f.Status != "" && s.Status != f.Status {
			continue
		}
		out = append(out, s)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (m *C2) GetSession(_ context.Context, id string) (provider.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return provider.Session{}, fmt.Errorf("%w: session %s", provider.ErrNotFound, id)
	}
	return s, nil
}

func (m *C2) KillSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("%w: session %s", provider.ErrNotFound, id)
	}
	s.Status = "closed"
	m.sessions[id] = s
	m.record(provider.EventC2SessionClosed, map[string]any{"session_id": id})
	return nil
}

func (m *C2) Dispatch(_ context.Context, spec provider.TaskSpec) (provider.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[spec.SessionID]
	if !ok || s.Status != "active" {
		return provider.Task{}, fmt.Errorf("%w: session %s", provider.ErrNotFound, spec.SessionID)
	}
	now := time.Now()
	t := provider.Task{ID: m.nextID("task"), SessionID: spec.SessionID, State: provider.TaskCompleted, CreatedAt: now, UpdatedAt: now}
	res := provider.TaskResult{TaskID: t.ID, Stdout: "mock: " + spec.Command}
	if m.responder != nil {
		res = m.responder(spec)
		res.TaskID = t.ID
	}
	m.tasks[t.ID] = t
	m.results[t.ID] = res
	m.record(provider.EventC2TaskDispatched, map[string]any{"task_id": t.ID, "session_id": spec.SessionID})
	m.record(provider.EventC2TaskCompleted, map[string]any{"task_id": t.ID})
	return t, nil
}

func (m *C2) PollTask(_ context.Context, id string) (provider.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return provider.Task{}, fmt.Errorf("%w: task %s", provider.ErrNotFound, id)
	}
	return t, nil
}

func (m *C2) TaskResult(_ context.Context, id string) (provider.TaskResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.results[id]
	if !ok {
		return provider.TaskResult{}, fmt.Errorf("%w: task %s", provider.ErrNotFound, id)
	}
	return res, nil
}

func (m *C2) BuildPayload(_ context.Context, spec provider.PayloadSpec) (provider.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := sha256.Sum256([]byte(spec.Name + "|" + spec.Format + "|" + spec.Host))
	a := provider.Artifact{
		ID: m.nextID("pay"), Name: spec.Name, Path: "/tmp/mock/" + spec.Name,
		SHA256: fmt.Sprintf("%x", sum), Size: int64(len(spec.Name) * 16), CreatedAt: time.Now(),
	}
	m.artifacts = append(m.artifacts, a)
	return a, nil
}

func (m *C2) ListPayloads(context.Context) ([]provider.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Artifact, len(m.artifacts))
	copy(out, m.artifacts)
	return out, nil
}

func (m *C2) Events(_ context.Context, fromSeq int64) (<-chan provider.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan provider.Event, len(m.events))
	for _, e := range m.events {
		if e.Seq > fromSeq {
			ch <- e
		}
	}
	close(ch)
	return ch, nil
}

func (m *C2) nextID(prefix string) string {
	m.seq++
	return fmt.Sprintf("%s-%d", prefix, m.seq)
}

func (m *C2) record(kind string, data map[string]any) {
	m.seq++
	m.events = append(m.events, provider.Event{Seq: m.seq, Kind: kind, At: time.Now(), Data: data})
}
