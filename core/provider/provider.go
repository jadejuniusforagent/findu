// Package provider 定义可替换能力（C2 / WebShell / 流量）的接口与装配注册表。
//
// 契约见 contracts/capability-providers.md。运行时不直接依赖任何具体实现：
// core 只认本包接口，插件在装配阶段把实现注册进来。每个能力同一时刻只允许
// 一个实现（滚动切换在装配层完成，不在请求路径上热换）。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
package provider

import (
	"errors"
	"sync"
	"time"
)

// Capability 是可替换能力名。
type Capability string

const (
	CapabilityC2       Capability = "c2"
	CapabilityWebShell Capability = "webshell"
	CapabilityTraffic  Capability = "traffic"
)

// Valid 报告能力名是否在枚举内。
func (c Capability) Valid() bool {
	switch c {
	case CapabilityC2, CapabilityWebShell, CapabilityTraffic:
		return true
	}
	return false
}

// 可判定错误。调用方用 errors.Is 判断。
var (
	ErrNotFound          = errors.New("provider: not found")
	ErrConflict          = errors.New("provider: conflict")
	ErrNilProvider       = errors.New("provider: nil provider")
	ErrAlreadyRegistered = errors.New("provider: capability already registered")
	ErrUnknownCapability = errors.New("provider: unknown capability")
)

// Event 是 provider 向宿主推送的通用事件。具体字段放 Data，避免为每种能力
// 定义一套事件结构；事件名按 "<capability>.<verb>" 约定，如 "c2.session.opened"。
type Event struct {
	Seq  int64          `json:"seq"`
	Kind string         `json:"kind"`
	At   time.Time      `json:"at"`
	Data map[string]any `json:"data,omitempty"`
}

// Registry 是能力的装配注册表：一个能力一个实现，重复注册直接失败。
type Registry struct {
	mu       sync.RWMutex
	c2       C2Provider
	c2Source string
	ws       WebShellProvider
	wsSource string
	tr       TrafficProvider
	trSource string
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry { return &Registry{} }

// RegisterC2 注册 C2 实现。source 是插件 id，仅用于状态展示。
func (r *Registry) RegisterC2(source string, p C2Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		return ErrNilProvider
	}
	if r.c2 != nil {
		return ErrAlreadyRegistered
	}
	r.c2, r.c2Source = p, source
	return nil
}

// RegisterWebShell 注册 WebShell 实现。
func (r *Registry) RegisterWebShell(source string, p WebShellProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		return ErrNilProvider
	}
	if r.ws != nil {
		return ErrAlreadyRegistered
	}
	r.ws, r.wsSource = p, source
	return nil
}

// RegisterTraffic 注册流量实现。
func (r *Registry) RegisterTraffic(source string, p TrafficProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		return ErrNilProvider
	}
	if r.tr != nil {
		return ErrAlreadyRegistered
	}
	r.tr, r.trSource = p, source
	return nil
}

// C2 返回已注册的 C2 实现。
func (r *Registry) C2() (C2Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.c2, r.c2 != nil
}

// WebShell 返回已注册的 WebShell 实现。
func (r *Registry) WebShell() (WebShellProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ws, r.ws != nil
}

// Traffic 返回已注册的流量实现。
func (r *Registry) Traffic() (TrafficProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tr, r.tr != nil
}

// Bindings 返回「能力 -> 插件 id」快照，供 /api/plugins 之类只读接口展示。
func (r *Registry) Bindings() map[Capability]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[Capability]string{}
	if r.c2 != nil {
		out[CapabilityC2] = r.c2Source
	}
	if r.ws != nil {
		out[CapabilityWebShell] = r.wsSource
	}
	if r.tr != nil {
		out[CapabilityTraffic] = r.trSource
	}
	return out
}
