package apkanalysis

// backend_registry — 后端注册表与降级链。
// 按注册顺序构成优先级降级链：首选 → 次选 → ... → 兜底（smali）。
// Resolve 按顺序检查 Health，返回第一个健康的后端。

import (
	"context"
	"fmt"
	"sync"
)

// BackendInfo 后端状态摘要（供 HTTP API 消费）。
type BackendInfo struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

// BackendRegistry 后端注册表，按优先级维护降级链。
type BackendRegistry struct {
	mu       sync.RWMutex
	backends []DecompilerBackend
	prefer   string // 首选 kind（空 = 第一个注册的）
}

// NewBackendRegistry 创建空注册表。
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{}
}

// Register 按注册顺序追加后端（高优先级先注册）。
func (r *BackendRegistry) Register(b DecompilerBackend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends = append(r.backends, b)
}

// Remove 按 kind 移除后端。
func (r *BackendRegistry) Remove(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, b := range r.backends {
		if b.Kind() == kind {
			r.backends = append(r.backends[:i], r.backends[i+1:]...)
			return
		}
	}
}

// SetPrefer 把指定 kind 的后端提升到首位（优先降级）。
// 若不存在该 kind，静默忽略。
func (r *BackendRegistry) SetPrefer(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, b := range r.backends {
		if b.Kind() == kind {
			if i > 0 {
				r.backends = append([]DecompilerBackend{b}, append(r.backends[:i], r.backends[i+1:]...)...)
			}
			r.prefer = kind
			return
		}
	}
}

// Prefer 返回首选 kind。
func (r *BackendRegistry) Prefer() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.prefer != "" {
		return r.prefer
	}
	if len(r.backends) > 0 {
		return r.backends[0].Kind()
	}
	return ""
}

// Resolve 按降级顺序取第一个健康的后端；无可用时返回 nil。
func (r *BackendRegistry) Resolve(ctx context.Context) DecompilerBackend {
	r.mu.RLock()
	backends := make([]DecompilerBackend, len(r.backends))
	copy(backends, r.backends)
	r.mu.RUnlock()
	for _, b := range backends {
		if b.Health(ctx) == nil {
			return b
		}
	}
	return nil
}

// List 返回所有注册后端的健康状态摘要。
func (r *BackendRegistry) List(ctx context.Context) []BackendInfo {
	r.mu.RLock()
	backends := make([]DecompilerBackend, len(r.backends))
	copy(backends, r.backends)
	prefer := r.prefer
	r.mu.RUnlock()

	out := make([]BackendInfo, 0, len(backends))
	for _, b := range backends {
		err := b.Health(ctx)
		info := BackendInfo{
			Kind:    b.Kind(),
			Name:    b.Name(),
			Healthy: err == nil,
		}
		if err != nil {
			info.Error = err.Error()
		}
		out = append(out, info)
	}
	_ = prefer // Phase 2: 前端通过 prefer 字段高亮首选项
	return out
}

// ResolveWithFallback 按优先级降级；所有后端均不可用时返回 ErrNoBackend。
func (r *BackendRegistry) ResolveWithFallback(ctx context.Context, prefer string) (DecompilerBackend, error) {
	// 临时提升 prefer
	if prefer != "" {
		r.SetPrefer(prefer)
	}
	b := r.Resolve(ctx)
	if b == nil {
		return nil, fmt.Errorf("no healthy decompiler backend available")
	}
	return b, nil
}

// HasKind 检查是否已注册指定 kind 的后端。
func (r *BackendRegistry) HasKind(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, b := range r.backends {
		if b.Kind() == kind {
			return true
		}
	}
	return false
}
