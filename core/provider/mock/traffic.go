package mock

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"finduai/core/provider"
)

// Traffic 是内存流量 provider：抓包会话与 flow 都在进程内，便于契约测试。
type Traffic struct {
	mu       sync.Mutex
	seq      int64
	captures map[string]provider.Capture
	flows    []provider.Flow
}

// NewTraffic 创建空流量 mock。
func NewTraffic() *Traffic {
	return &Traffic{captures: map[string]provider.Capture{}}
}

func (m *Traffic) StartCapture(_ context.Context, filter provider.CaptureFilter) (provider.Capture, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	c := provider.Capture{
		ID:     fmt.Sprintf("cap-%d", m.seq),
		Filter: filter, Status: "running", StartedAt: time.Now(),
	}
	m.captures[c.ID] = c
	return c, nil
}

func (m *Traffic) StopCapture(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.captures[id]
	if !ok {
		return fmt.Errorf("%w: capture %s", provider.ErrNotFound, id)
	}
	c.Status = "stopped"
	m.captures[id] = c
	return nil
}

// AddFlow 注入一条已录制 flow。真实实现由抓包回调写入。
func (m *Traffic) AddFlow(f provider.Flow) provider.Flow {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	if f.ID == "" {
		f.ID = fmt.Sprintf("flow-%d", m.seq)
	}
	if f.CapturedAt.IsZero() {
		f.CapturedAt = time.Now()
	}
	m.flows = append(m.flows, f)
	return f
}

func (m *Traffic) Query(_ context.Context, c provider.FlowCriteria) ([]provider.Flow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Flow, 0, len(m.flows))
	for _, f := range m.flows {
		if c.Method != "" && !strings.EqualFold(f.Method, c.Method) {
			continue
		}
		if c.Host != "" && f.Host != c.Host {
			continue
		}
		if c.URLContains != "" && !strings.Contains(f.URL, c.URLContains) {
			continue
		}
		out = append(out, f)
		if c.Limit > 0 && len(out) >= c.Limit {
			break
		}
	}
	return out, nil
}

func (m *Traffic) Get(_ context.Context, id string) (provider.Flow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.flows {
		if f.ID == id {
			return f, nil
		}
	}
	return provider.Flow{}, fmt.Errorf("%w: flow %s", provider.ErrNotFound, id)
}

func (m *Traffic) Replay(_ context.Context, id string, mut provider.Mutation) (provider.Flow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var src *provider.Flow
	for i := range m.flows {
		if m.flows[i].ID == id {
			src = &m.flows[i]
			break
		}
	}
	if src == nil {
		return provider.Flow{}, fmt.Errorf("%w: flow %s", provider.ErrNotFound, id)
	}
	m.seq++
	replayed := *src
	replayed.ID = fmt.Sprintf("flow-%d", m.seq)
	replayed.CapturedAt = time.Now()
	if mut.Method != "" {
		replayed.Method = mut.Method
	}
	if mut.URL != "" {
		replayed.URL = mut.URL
	}
	if mut.Headers != nil {
		replayed.RequestHeaders = map[string][]string{}
		for k, v := range mut.Headers {
			replayed.RequestHeaders[k] = []string{v}
		}
	}
	if mut.Body != nil {
		replayed.RequestBody = append([]byte(nil), mut.Body...)
	}
	m.flows = append(m.flows, replayed)
	return replayed, nil
}
