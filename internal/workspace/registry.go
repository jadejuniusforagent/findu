package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Scope 注册目录的作用域，决定隔离层级与 GC 粒度。
type Scope string

const (
	// ScopeGlobal 平台全局共享（如 sliver-builds）。
	ScopeGlobal Scope = "global"
	// ScopeProject 项目内多会话共享（如 workspace/projects/<id>）。
	ScopeProject Scope = "project"
	// ScopeConversation 单会话独占（如 workspace/conversations/<id>）。
	ScopeConversation Scope = "conversation"
)

// Entry 目录注册表条目：平台落盘目录的登记五要素（方案 §3.2）。
// 唯一登记口径——子系统不得再对 dataDir 做裸 filepath 拼装，统一走本注册表。
type Entry struct {
	// Name 唯一键（如 "evasion-artifacts"），重复注册报错。
	Name string
	// Base 基路径解析函数，延迟求值（避免启动期 IO）；须返回绝对或进程相对路径。
	Base func() (string, error)
	// Scope 作用域。
	Scope Scope
	// TTL 无访问超过该时长则成为回收候选；0 = 不参与 TTL 回收。
	TTL time.Duration
	// QuotaBytes 容量上限；0 = 不限量。超限（超水位）时按最久未访问优先回收。
	QuotaBytes int64
	// ProtectKey 引用保护键（非空时有引用则永不回收，判定交给 Protector）；
	// 空 = 无引用保护。
	ProtectKey string
}

// ErrDuplicateEntry 同名条目重复注册。
var ErrDuplicateEntry = errors.New("workspace: 目录条目重复注册")

// Registry 目录注册表。零值不可用，用 NewRegistry 创建；包级 Default
// 供各子系统在 init/装配时注册，GC 与观测端点统一从这里读。
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// Default 包级注册表；🔴 var/tmp/workspace 与 var/tmp/reduction 是平台运行数据，
// 永远不得注册为可回收条目（gc.RunGC 另有硬拒绝兜底）。
var Default = NewRegistry()

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Entry)}
}

// Validate 校验条目五要素的完整性（Register 与测试共用）。
func (e Entry) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("workspace: Name 不能为空")
	}
	if e.Base == nil {
		return fmt.Errorf("workspace: %s 的 Base（延迟解析函数）不能为 nil", e.Name)
	}
	switch e.Scope {
	case ScopeGlobal, ScopeProject, ScopeConversation:
	default:
		return fmt.Errorf("workspace: %s 的 Scope 非法: %q", e.Name, e.Scope)
	}
	if e.TTL < 0 {
		return fmt.Errorf("workspace: %s 的 TTL 不能为负", e.Name)
	}
	if e.QuotaBytes < 0 {
		return fmt.Errorf("workspace: %s 的 QuotaBytes 不能为负", e.Name)
	}
	return nil
}

// Register 登记一个目录条目，同名重复返回 ErrDuplicateEntry。
func (r *Registry) Register(e Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[e.Name]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateEntry, e.Name)
	}
	r.entries[e.Name] = e
	return nil
}

// Lookup 按名字取条目。
func (r *Registry) Lookup(name string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e, ok
}

// Entries 全部条目，按 Name 排序（观测输出与 GC 遍历的稳定顺序）。
func (r *Registry) Entries() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Register 向包级 Default 注册。
func Register(e Entry) error { return Default.Register(e) }

// Lookup 在包级 Default 查找。
func Lookup(name string) (Entry, bool) { return Default.Lookup(name) }

// Entries 返回包级 Default 的全部条目。
func Entries() []Entry { return Default.Entries() }

// Unregister 从包级 Default 摘除条目（配置驱动的重注册 / 测试清理用）；
// 返回是否确有删除。
func Unregister(name string) bool { return Default.Unregister(name) }

// Unregister 摘除条目；返回是否确有删除。
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; !ok {
		return false
	}
	delete(r.entries, name)
	return true
}

// JoinBase 生成 Entry.Base：语义与 filepath.Join(base, elem...) 逐字一致
// （含 base 为空时退化为相对路径的既有行为），恒不报错。
// 子系统用它装配注册条目，替换对 dataDir 的裸 filepath 拼装——
// 裸拼棘轮（治理方案 §五验收）按该 grep 形态的命中数只减不增。
func JoinBase(base string, elem ...string) func() (string, error) {
	return func() (string, error) {
		return filepath.Join(append([]string{base}, elem...)...), nil
	}
}

// RegisterOrResolve 以 upsert 语义登记条目（同名后写覆盖，装配期重入安全）
// 并解析其基路径。「后写覆盖」与注册表前各子系统「用自己的 dataDir 拼装」的
// 行为一致：同进程内以最后一次装配为准（测试重复初始化、装配重放均安全）。
func RegisterOrResolve(e Entry) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	Default.mu.Lock()
	Default.entries[e.Name] = e
	Default.mu.Unlock()
	return e.Base()
}

// Resolve 解析已注册条目的基路径；未注册返回错误（消费方在请求期解析，
// 装配期注册——解析失败即装配缺位，应显式暴露而不是静默回退）。
func Resolve(name string) (string, error) {
	e, ok := Default.Lookup(name)
	if !ok {
		return "", fmt.Errorf("workspace: 目录条目 %s 未注册", name)
	}
	return e.Base()
}

// ResolveOrDefault 解析已注册条目，未注册/解析失败/空值时静默回落 fallback。
// 供请求期消费方（handler 等）使用：生产装配必已注册（返回登记值），直构测试
// 等绕过装配的场景回落原语义——零行为变化。与 Resolve 的分工：Resolve 显式
// 报错（装配/强一致面用），本函数静默回落（消费面用）。
func ResolveOrDefault(name, fallback string) string {
	if e, ok := Default.Lookup(name); ok {
		if dir, err := e.Base(); err == nil && dir != "" {
			return dir
		}
	}
	return fallback
}
