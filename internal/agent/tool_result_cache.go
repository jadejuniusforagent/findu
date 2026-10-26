package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"finduai/internal/config"
)

// ToolResultCache 参数级工具结果缓存（P0 性能优化）。
//
// 设计要点：
//   - 仅缓存 config 中 agent.tool_result_cache.allowlist 显式列出的工具（默认仅只读、语义稳定工具）；
//     命令执行 / 写操作 / 时间敏感查询（exec、webshell_*、record_vulnerability、get_tool_execution 等）永不缓存，
//     避免"读旧值"破坏渗透测试语义。
//   - 键 = 工具名 + 对话ID + 规范化参数（键有序 JSON 的 SHA-256），避免跨会话/跨参数串味。
//   - 带 TTL 与容量上限（近似 LRU 淘汰），默认 20s / 512 条。
//   - 命中返回结果副本，避免调用方修改污染缓存。
type ToolResultCache struct {
	enabled    bool
	ttl        time.Duration
	maxEntries int
	allow      map[string]struct{}

	mu      sync.Mutex
	entries map[string]*toolCacheEntry
}

type toolCacheEntry struct {
	result    *ToolExecutionResult
	expiresAt time.Time
	lastUsed  time.Time
}

// newToolResultCache 依据 agent 配置构造缓存；agentCfg 为空或未启用时返回禁用实例（各方法安全空转）。
func newToolResultCache(agentCfg *config.AgentConfig) *ToolResultCache {
	c := &ToolResultCache{
		ttl:        20 * time.Second,
		maxEntries: 512,
		allow:      make(map[string]struct{}),
		entries:    make(map[string]*toolCacheEntry),
	}
	if agentCfg == nil {
		return c
	}
	tc := agentCfg.ToolResultCache
	c.enabled = tc.Enabled
	if tc.TTLSeconds > 0 {
		c.ttl = time.Duration(tc.TTLSeconds) * time.Second
	}
	if tc.MaxEntries > 0 {
		c.maxEntries = tc.MaxEntries
	}
	allow := tc.Allowlist
	if len(allow) == 0 {
		allow = config.DefaultToolResultCacheAllowlist()
	}
	for _, n := range allow {
		if n = strings.TrimSpace(n); n != "" {
			c.allow[n] = struct{}{}
		}
	}
	return c
}

// Enabled 是否启用。
func (c *ToolResultCache) Enabled() bool {
	return c != nil && c.enabled
}

// Cacheable 判断某工具是否在缓存白名单内。
func (c *ToolResultCache) Cacheable(toolName string) bool {
	if !c.Enabled() {
		return false
	}
	_, ok := c.allow[toolName]
	return ok
}

// Get 命中则返回结果副本。
func (c *ToolResultCache) Get(key string) (*ToolExecutionResult, bool) {
	if !c.Enabled() || key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if now.After(e.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	e.lastUsed = now
	cp := *e.result
	return &cp, true
}

// Put 写入缓存（仅成功结果）。
func (c *ToolResultCache) Put(key string, result *ToolExecutionResult) {
	if !c.Enabled() || key == "" || result == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.evictLocked(time.Now())
	}
	cp := *result
	c.entries[key] = &toolCacheEntry{
		result:    &cp,
		expiresAt: time.Now().Add(c.ttl),
		lastUsed:  time.Now(),
	}
}

// evictLocked 先清理过期项，若仍有容量压力则淘汰最久未使用项。调用方需持锁。
func (c *ToolResultCache) evictLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
			continue
		}
		if oldestKey == "" || e.lastUsed.Before(oldest) {
			oldestKey, oldest = k, e.lastUsed
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// Stats 返回 (条目数, 是否启用)，用于观测。
func (c *ToolResultCache) Stats() (int, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.enabled
}

// toolResultCacheKey 计算缓存键：工具名 + 对话ID + 规范化参数。
func toolResultCacheKey(toolName, conversationID string, args map[string]interface{}) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte{0})
	h.Write([]byte(conversationID))
	h.Write([]byte{0})
	h.Write([]byte(canonicalToolArgs(args)))
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalToolArgs 生成键有序、稳定的参数表示，规避 map 遍历顺序随机导致的缓存击穿。
func canonicalToolArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(args[k])
		if err != nil {
			vb = []byte("null")
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}
