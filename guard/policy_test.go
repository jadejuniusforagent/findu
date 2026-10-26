package guard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"finduai/askernel"
	actool "finduai/tools"
)

// gatedTool 是测试用的带目标声明能力的 CoreTool 包装（实现 Targeted/SideEffector）。
type gatedTool struct {
	actool.CoreTool
	effect string
	fail   bool // ExtractTargets 强制失败，验证 fail closed
}

func (g *gatedTool) ExtractTargets(in json.RawMessage) ([]actool.Target, error) {
	if g.fail {
		return nil, errors.New("target 参数不是合法目标")
	}
	var v struct {
		Target string `json:"target"`
	}
	_ = json.Unmarshal(in, &v)
	return []actool.Target{{Kind: actool.TargetKindDomain, Value: v.Target}}, nil
}

func (g *gatedTool) SideEffect() string { return g.effect }

func toolResolver(tool *gatedTool) TargetResolver {
	return func(name string) (actool.CoreTool, bool) {
		if name == "probe" {
			return tool, true
		}
		return nil, false
	}
}

func runPre(g *Guard, target string) askernel.Result {
	g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"`+target+`"}`))
	return askernel.Result{}
}

// warnCount 返回审计里 warn 条目数。
func warnCount(g *Guard) int {
	n := 0
	for _, e := range g.Audit() {
		if e.Action == "warn" {
			n++
		}
	}
	return n
}

// 影子模式（默认）：越界只记 warn，不拦截。
func TestScopeShadow(t *testing.T) {
	g := New()
	tool := &gatedTool{}
	g.SetTargetResolver(toolResolver(tool))
	g.SetScopeChecker(func(ctx context.Context, tool string, targets []actool.Target) error {
		if len(targets) > 0 && targets[0].Host() == "outside.com" {
			return errors.New("目标不在范围内")
		}
		return nil
	}, false)

	runPre(g, "outside.com")
	if warnCount(g) == 0 {
		t.Fatal("影子模式下越界必须记 warn")
	}
}

// enforce=true：越界硬拦，block 消息带平台管控 framing。
func TestScopeEnforce(t *testing.T) {
	g := New()
	tool := &gatedTool{}
	g.SetTargetResolver(toolResolver(tool))
	g.SetScopeChecker(func(ctx context.Context, tool string, targets []actool.Target) error {
		if len(targets) > 0 && targets[0].Host() == "outside.com" {
			return errors.New("不在范围")
		}
		return nil
	}, true)

	block, msg, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"outside.com"}`))
	if !block || msg == "" {
		t.Fatalf("enforce 下越界必须拦截, got block=%v msg=%q", block, msg)
	}
	if !g.isBlockedRecorded() {
		t.Fatal("拦截必须落审计")
	}
	// 范围内目标：放行。
	block2, msg2, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"in-scope.com"}`))
	if block2 || msg2 != "" {
		t.Fatalf("范围内目标必须放行, got block=%v msg=%q", block2, msg2)
	}
}

// 解析失败 = fail closed：enforce 下拦截；影子下 warn 且不拦截（影子零行为变化）。
func TestScopeParseFailureFailClosed(t *testing.T) {
	checker := func(ctx context.Context, tool string, targets []actool.Target) error { return nil }

	sg := New()
	sg.SetTargetResolver(toolResolver(&gatedTool{fail: true}))
	sg.SetScopeChecker(checker, true)
	if block, msg, _ := sg.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"x"}`)); !block || msg == "" {
		t.Fatalf("enforce 下解析失败必须拦截 (fail closed), got block=%v msg=%q", block, msg)
	}

	sh := New()
	sh.SetTargetResolver(toolResolver(&gatedTool{fail: true}))
	sh.SetScopeChecker(checker, false)
	if block, _, _ := sh.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"x"}`)); block {
		t.Fatal("影子模式下解析失败不得拦截（零行为变化）")
	}
	if warnCount(sh) == 0 {
		t.Fatal("影子模式下解析失败必须记 warn")
	}
}

// 限速：影子只 warn，enforce 拦截；无 Targeted 的工具不限。
func TestRateLimitShadowAndEnforce(t *testing.T) {
	mk := func(enforce bool) *Guard {
		g := New()
		g.SetTargetResolver(toolResolver(&gatedTool{})) // 无声明 → 兜底提取 target 键
		g.SetRateLimit(2, enforce)
		return g
	}
	sg := mk(false)
	for i := 0; i < 5; i++ {
		if block, _, _ := sg.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"same.com"}`)); block {
			t.Fatal("影子限速不得拦截")
		}
		if i >= 2 && warnCount(sg) == 0 {
			t.Fatalf("第 %d 次超限必须记 warn", i+1)
		}
	}
	g := mk(true)
	if block, _, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"same.com"}`)); block {
		t.Fatal("第一次调用不应拦截")
	}
	if block2, _, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"same.com"}`)); block2 {
		t.Fatal("第二次调用不应拦截")
	}
	block3, msg3, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"same.com"}`))
	if !block3 || msg3 == "" {
		t.Fatalf("同主机第三次调用必须被限速拦截, got block=%v msg=%q", block3, msg3)
	}
	// 不同主机互不影响。
	if block4, _, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"other.com"}`)); block4 {
		t.Fatal("不同主机不得互相连坐")
	}
}

// side_effect 观测：destructive 记 warn 且 per-tool 去重（两次调用只一条）。
func TestSideEffectObservation(t *testing.T) {
	g := New()
	g.SetTargetResolver(toolResolver(&gatedTool{effect: actool.EffectDestructive}))
	runPre(g, "a.com")
	runPre(g, "b.com")
	n := 0
	for _, e := range g.Audit() {
		if e.Reason != "" && e.Action == "warn" && strings.Contains(e.Reason, "destructive") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("destructive 观测必须 per-tool 去重, want 1 got %d", n)
	}
}

// 未解析目标 os.Args 门禁不越界：无 resolver（对话 guard）时全程 no-op。
func TestNoResolverNoop(t *testing.T) {
	g := New()
	g.SetRateLimit(1, true)
	g.SetScopeChecker(func(ctx context.Context, tool string, targets []actool.Target) error {
		return errors.New("should not run")
	}, true)
	// probe 由 resolver 缺席而有目标能力也无法探测 → 必须放行。
	if block, _, _ := g.Hooks().PreToolUse(context.Background(), "probe", []byte(`{"target":"a.com"}`)); block {
		t.Fatal("无工具索引时 scope/限速门禁必须 no-op（对话 guard 场景）")
	}
	if warnCount(g) != 0 {
		t.Fatal("无 resolver 不应产生 warn")
	}
}

func stringsContains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func (g *Guard) isBlockedRecorded() bool {
	for _, e := range g.Audit() {
		if e.Action == "block" {
			return true
		}
	}
	return false
}
