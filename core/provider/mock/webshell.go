package mock

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"finduai/core/provider"
)

// WebShell 是内存 WebShell provider：连接与文件都在进程内，命令输出可由
// SetResponder 定制。默认回显 "mock: <command>"。
type WebShell struct {
	mu        sync.Mutex
	seq       int64
	conns     map[string]provider.Conn
	files     map[string]map[string][]byte
	responder func(command string) provider.ExecResult
}

// NewWebShell 创建空 WebShell mock。
func NewWebShell() *WebShell {
	return &WebShell{conns: map[string]provider.Conn{}, files: map[string]map[string][]byte{}}
}

// SetResponder 定制命令执行结果。
func (m *WebShell) SetResponder(fn func(string) provider.ExecResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responder = fn
}

func (m *WebShell) Open(_ context.Context, spec provider.ConnSpec) (provider.Conn, error) {
	if strings.TrimSpace(spec.URL) == "" {
		return provider.Conn{}, fmt.Errorf("%w: url 为空", provider.ErrConflict)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	c := provider.Conn{ID: fmt.Sprintf("ws-%d", m.seq), URL: spec.URL, Status: "open", CreatedAt: time.Now()}
	m.conns[c.ID] = c
	m.files[c.ID] = map[string][]byte{}
	return c, nil
}

func (m *WebShell) Close(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if !ok {
		return fmt.Errorf("%w: conn %s", provider.ErrNotFound, id)
	}
	c.Status = "closed"
	m.conns[id] = c
	return nil
}

func (m *WebShell) Test(_ context.Context, id string) (provider.Health, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if !ok {
		return provider.Health{}, fmt.Errorf("%w: conn %s", provider.ErrNotFound, id)
	}
	if c.Status != "open" {
		return provider.Health{OK: false, Message: "closed"}, nil
	}
	return provider.Health{OK: true, Message: "ok"}, nil
}

func (m *WebShell) Exec(_ context.Context, id, command string) (provider.ExecResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireOpen(id); err != nil {
		return provider.ExecResult{}, err
	}
	if m.responder != nil {
		return m.responder(command), nil
	}
	return provider.ExecResult{Stdout: "mock: " + command}, nil
}

func (m *WebShell) ListFiles(_ context.Context, id, dir string) ([]provider.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireOpen(id); err != nil {
		return nil, err
	}
	if dir == "" {
		dir = "/"
	}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var out []provider.FileEntry
	for p, content := range m.files[id] {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if strings.Contains(rest, "/") {
			continue // 只列一层
		}
		out = append(out, provider.FileEntry{
			Name: path.Base(p), Path: p, Dir: false, Size: int64(len(content)), ModTime: time.Now(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (m *WebShell) ReadFile(_ context.Context, id, p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireOpen(id); err != nil {
		return nil, err
	}
	content, ok := m.files[id][p]
	if !ok {
		return nil, fmt.Errorf("%w: file %s", provider.ErrNotFound, p)
	}
	return append([]byte(nil), content...), nil
}

func (m *WebShell) WriteFile(_ context.Context, id, p string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireOpen(id); err != nil {
		return err
	}
	m.files[id][p] = append([]byte(nil), content...)
	return nil
}

func (m *WebShell) DeleteFile(_ context.Context, id, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireOpen(id); err != nil {
		return err
	}
	if _, ok := m.files[id][p]; !ok {
		return fmt.Errorf("%w: file %s", provider.ErrNotFound, p)
	}
	delete(m.files[id], p)
	return nil
}

// Upload 与 WriteFile 语义相同：mock 不区分编码，只存字节。
func (m *WebShell) Upload(ctx context.Context, id, p string, content []byte) error {
	return m.WriteFile(ctx, id, p, content)
}

func (m *WebShell) requireOpen(id string) error {
	c, ok := m.conns[id]
	if !ok {
		return fmt.Errorf("%w: conn %s", provider.ErrNotFound, id)
	}
	if c.Status != "open" {
		return fmt.Errorf("%w: conn %s 已关闭", provider.ErrConflict, id)
	}
	return nil
}
