package agent

import (
	"testing"
	"time"

	"finduai/internal/config"
)

func TestToolResultCache_BasicHitAndCopy(t *testing.T) {
	c := newToolResultCache(&config.AgentConfig{
		ToolResultCache: config.ToolResultCacheConfig{
			Enabled:    true,
			TTLSeconds: 60,
			MaxEntries: 8,
			Allowlist:  []string{"tool_search"},
		},
	})
	if !c.Cacheable("tool_search") {
		t.Fatal("tool_search should be cacheable")
	}
	if c.Cacheable("exec") {
		t.Fatal("exec must never be cacheable")
	}
	key := toolResultCacheKey("tool_search", "conv1", map[string]interface{}{"b": 2, "a": 1})
	if got, ok := c.Get(key); ok || got != nil {
		t.Fatal("expected miss on empty cache")
	}
	orig := &ToolExecutionResult{Result: "hello", ExecutionID: "x1"}
	c.Put(key, orig)
	got, ok := c.Get(key)
	if !ok || got.Result != "hello" {
		t.Fatalf("expected hit, got ok=%v got=%+v", ok, got)
	}
	// 命中返回副本，修改不应污染缓存
	got.Result = "mutated"
	if again, _ := c.Get(key); again.Result != "hello" {
		t.Fatal("cache result must be a copy")
	}
}

func TestToolResultCache_TTLExpiry(t *testing.T) {
	c := newToolResultCache(&config.AgentConfig{
		ToolResultCache: config.ToolResultCacheConfig{Enabled: true, TTLSeconds: 1, Allowlist: []string{"search_knowledge_base"}},
	})
	key := toolResultCacheKey("search_knowledge_base", "c", map[string]interface{}{"q": "x"})
	c.Put(key, &ToolExecutionResult{Result: "r"})
	if _, ok := c.Get(key); !ok {
		t.Fatal("expected hit before TTL")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, ok := c.Get(key); ok {
		t.Fatal("expected miss after TTL")
	}
}

func TestToolResultCache_Disabled(t *testing.T) {
	c := newToolResultCache(&config.AgentConfig{ToolResultCache: config.ToolResultCacheConfig{Enabled: false}})
	if c.Cacheable("tool_search") {
		t.Fatal("disabled cache must not report cacheable")
	}
	c.Put("k", &ToolExecutionResult{Result: "r"})
	if _, ok := c.Get("k"); ok {
		t.Fatal("disabled cache must not serve")
	}
}

func TestCanonicalToolArgs_StableOrdering(t *testing.T) {
	a := canonicalToolArgs(map[string]interface{}{"z": 1, "a": []interface{}{1, 2}, "m": "s"})
	b := canonicalToolArgs(map[string]interface{}{"m": "s", "a": []interface{}{1, 2}, "z": 1})
	if a != b {
		t.Fatalf("canonical args not stable:\n%s\n%s", a, b)
	}
	// 不同参数必须产生不同的键
	k1 := toolResultCacheKey("tool_search", "c", map[string]interface{}{"q": "a"})
	k2 := toolResultCacheKey("tool_search", "c", map[string]interface{}{"q": "b"})
	if k1 == k2 {
		t.Fatal("different args must produce different keys")
	}
	if toolResultCacheKey("tool_search", "c1", map[string]interface{}{"q": "a"}) == toolResultCacheKey("tool_search", "c2", map[string]interface{}{"q": "a"}) {
		t.Fatal("different conversations must produce different keys")
	}
}

func TestToolResultCache_Eviction(t *testing.T) {
	c := newToolResultCache(&config.AgentConfig{
		ToolResultCache: config.ToolResultCacheConfig{Enabled: true, TTLSeconds: 60, MaxEntries: 2, Allowlist: []string{"tool_search"}},
	})
	k1 := toolResultCacheKey("tool_search", "c", map[string]interface{}{"i": 1})
	k2 := toolResultCacheKey("tool_search", "c", map[string]interface{}{"i": 2})
	k3 := toolResultCacheKey("tool_search", "c", map[string]interface{}{"i": 3})
	c.Put(k1, &ToolExecutionResult{Result: "1"})
	time.Sleep(5 * time.Millisecond)
	c.Put(k2, &ToolExecutionResult{Result: "2"})
	c.Get(k1) // k1 变为最近使用
	time.Sleep(5 * time.Millisecond)
	c.Put(k3, &ToolExecutionResult{Result: "3"}) // 触发淘汰最久未用的 k2
	n, _ := c.Stats()
	if n != 2 {
		t.Fatalf("expected 2 entries after eviction, got %d", n)
	}
	if _, ok := c.Get(k2); ok {
		t.Fatal("k2 (LRU) should have been evicted")
	}
}
