package pluginhost

import (
	"context"
	"fmt"
	"sync"

	"finduai/core/ownership"
)

// Plugin 是插件的运行时契约。Manifest 必须是同一个不可变值（注册后不再改），
// 宿主据此做 id/版本/表归属校验。
type Plugin interface {
	Manifest() Manifest
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) error
}

// State 是插件生命周期状态。
type State string

const (
	StateRegistered State = "registered"
	StateStarting   State = "starting"
	StateStarted    State = "started"
	StateStopping   State = "stopping"
	StateStopped    State = "stopped"
	StateFailed     State = "failed"
)

// Status 是注册表对外暴露的只读快照。
type Status struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Kind    Kind   `json:"kind"`
	State   State  `json:"state"`
	Error   string `json:"error,omitempty"`
}

type entry struct {
	plugin   Plugin
	manifest Manifest
	state    State
	err      string
}

// Registry 是进程内插件注册表：登记 -> 启动 -> 停止 -> 注销。
//
// 并发约定：Start/Stop/Health 调用插件方法时不持锁，避免插件回调注册表造成
// 死锁；状态迁移在锁内完成。同一插件的并发 Start 只有第一个能进入 starting。
type Registry struct {
	apiVersion string

	mu      sync.Mutex
	entries map[string]*entry
	order   []string
}

// NewRegistry 创建注册表。apiVersion 为空时使用本包的 APIVersion。
func NewRegistry(apiVersion string) *Registry {
	if apiVersion == "" {
		apiVersion = APIVersion
	}
	return &Registry{
		apiVersion: apiVersion,
		entries:    make(map[string]*entry),
	}
}

// APIVersion 返回注册表接受的插件 API 版本。
func (r *Registry) APIVersion() string { return r.apiVersion }

// Register 校验清单（语法、API 版本、表归属）并登记插件。依赖是否存在不在
// 此时校验：允许先注册被依赖者、后注册依赖者。
func (r *Registry) Register(p Plugin) error {
	if p == nil {
		return fmt.Errorf("%w: 插件实现为 nil", ErrInvalidManifest)
	}
	m := p.Manifest()
	if err := m.Validate(); err != nil {
		return err
	}
	if m.APIVersion != r.apiVersion {
		return fmt.Errorf("%w: api_version %q 与宿主 %q 不一致",
			ErrInvalidManifest, m.APIVersion, r.apiVersion)
	}
	if m.Requires != nil && m.Requires.API != "" && m.Requires.API != r.apiVersion {
		return fmt.Errorf("%w: requires.api %q 与宿主 %q 不一致",
			ErrInvalidManifest, m.Requires.API, r.apiVersion)
	}
	// 表归属以 docs/refactor/TABLE-OWNERSHIP.md 唯一事实源为准：插件只能声明
	// plugin-<id> 名下的表，未登记的表、core/domain/别的插件的表一律拒绝。
	if err := ownership.ValidatePluginTables(m.ID, m.OwnsTables); err != nil {
		return fmt.Errorf("%w: %v", ErrTableOwnership, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[m.ID]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicatePlugin, m.ID)
	}
	r.entries[m.ID] = &entry{plugin: p, manifest: m, state: StateRegistered}
	r.order = append(r.order, m.ID)
	return nil
}

// Start 启动插件。requires.plugins 必须都已注册且处于 started。
func (r *Registry) Start(ctx context.Context, id string) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	switch e.state {
	case StateStarted, StateStarting:
		r.mu.Unlock()
		return fmt.Errorf("%w: %s 已是 %s", ErrInvalidState, id, e.state)
	case StateStopping:
		r.mu.Unlock()
		return fmt.Errorf("%w: %s 正在停止", ErrInvalidState, id)
	}
	if e.manifest.Requires != nil {
		for _, dep := range e.manifest.Requires.Plugins {
			d, ok := r.entries[dep]
			if !ok {
				r.mu.Unlock()
				return fmt.Errorf("%w: 缺少插件 %s", ErrDependency, dep)
			}
			if d.state != StateStarted {
				r.mu.Unlock()
				return fmt.Errorf("%w: 插件 %s 状态 %s", ErrDependency, dep, d.state)
			}
		}
	}
	e.state = StateStarting
	e.err = ""
	plugin := e.plugin
	r.mu.Unlock()

	err := plugin.Start(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		e.state = StateFailed
		e.err = err.Error()
		return fmt.Errorf("pluginhost: 启动 %s 失败: %w", id, err)
	}
	e.state = StateStarted
	return nil
}

// Stop 停止插件。仍被已启动的插件依赖时拒绝，先停依赖方。
func (r *Registry) Stop(ctx context.Context, id string) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	if e.state != StateStarted {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s 当前 %s", ErrInvalidState, id, e.state)
	}
	for _, other := range r.order {
		if other == id {
			continue
		}
		oe := r.entries[other]
		if oe.state != StateStarted || oe.manifest.Requires == nil {
			continue
		}
		for _, dep := range oe.manifest.Requires.Plugins {
			if dep == id {
				r.mu.Unlock()
				return fmt.Errorf("%w: %s 仍被 %s 依赖", ErrInUse, id, other)
			}
		}
	}
	e.state = StateStopping
	plugin := e.plugin
	r.mu.Unlock()

	err := plugin.Stop(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		e.state = StateFailed
		e.err = err.Error()
		return fmt.Errorf("pluginhost: 停止 %s 失败: %w", id, err)
	}
	e.state = StateStopped
	return nil
}

// Health 探活。只有 started 的插件可探活，避免把「未启动」误报成健康。
func (r *Registry) Health(ctx context.Context, id string) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	if e.state != StateStarted {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s 当前 %s", ErrInvalidState, id, e.state)
	}
	plugin := e.plugin
	r.mu.Unlock()

	if err := plugin.Health(ctx); err != nil {
		return fmt.Errorf("pluginhost: %s 健康检查失败: %w", id, err)
	}
	return nil
}

// Unregister 注销插件。必须已停止，且没有其他已注册插件依赖它。
func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPluginNotFound, id)
	}
	switch e.state {
	case StateStarting, StateStarted, StateStopping:
		return fmt.Errorf("%w: %s 当前 %s", ErrInvalidState, id, e.state)
	}
	for _, other := range r.order {
		if other == id {
			continue
		}
		oe := r.entries[other]
		if oe.manifest.Requires == nil {
			continue
		}
		for _, dep := range oe.manifest.Requires.Plugins {
			if dep == id {
				return fmt.Errorf("%w: %s 仍被 %s 依赖", ErrInUse, id, other)
			}
		}
	}
	delete(r.entries, id)
	for i, v := range r.order {
		if v == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// Get 返回已注册插件（无论状态）。
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return nil, false
	}
	return e.plugin, true
}

// Status 返回单个插件快照。
func (r *Registry) Status(id string) (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return Status{}, false
	}
	return e.status(), true
}

// List 按注册顺序返回全部插件快照。
func (r *Registry) List() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.entries[id].status())
	}
	return out
}

func (e *entry) status() Status {
	return Status{
		ID:      e.manifest.ID,
		Version: e.manifest.Version,
		Kind:    e.manifest.Kind,
		State:   e.state,
		Error:   e.err,
	}
}
