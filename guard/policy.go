// Package guard — policy: per-target authorization-scope checking, per-host
// rate limiting, and side-effect observations, layered onto the Guard's
// PreToolUse hook.
//
// 这是 RoE 授权范围机制的回归实现（guard.go 头注释里「a replacement may be added
// later」的就是它），契约对齐 contracts/tool-contract.schema.json 的 targets/side_effect
// 字段，设计对齐 capkit 的门禁顺序（scope → approval → 限速，fail closed）。
//
// 交付方式是【影子模式起步】：默认只记 warn，不拦截。每类管控有独立开关
// （ScopeChecker 带 enforce、RateLimit 带 enforce），由宿主在任务创建时按
// settings 配置注入（见 server/scope_gate.go）。这样 P4 迁移期的 agent 行为
// 不被门禁翻车污染，先积累两周 warn 分布再切强制。
package guard

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"finduai/askernel"
	actool "finduai/tools"
)

// ScopeChecker 判一次工具调用是否落在授权范围内。返回 nil = 放行；
// 返回非 nil = 拒绝，错误文案直接进 tool_result（fail closed：勘察失败即拒绝，
// 不猜测）。数据层不可用等「无法裁决」情形由实现方自行决定放行并单独告知。
type ScopeChecker func(ctx context.Context, tool string, targets []actool.Target) error

// TargetResolver 让门禁按工具名拿到 CoreTool 实例，以探测可选扩展接口
// （Targeted / SideEffector）。可选：对话式 guard 通常没有 → 相关检查退化为 no-op。
type TargetResolver func(name string) (actool.CoreTool, bool)

// policyResources is the settable part of the gate policy. Everything defaults
// to a shadow no-op so wiring is always additive.
type policyResources struct {
	mu        sync.Mutex
	resolver  TargetResolver
	scope     ScopeChecker
	scopeEnfo bool // scope 违规：false=warn 影子，true=block
	limiter   *rateLimiter
	limitEnfo bool
	once      map[string]*sync.Once // per-tool 观测告警去重
}

func (g *Guard) policy() *policyResources { return &g.policyRes }

// SetTargetResolver 注入工具索引用于可选接口探测（宿主启动时一次）。
func (g *Guard) SetTargetResolver(fn TargetResolver) {
	g.policyRes.mu.Lock()
	defer g.policyRes.mu.Unlock()
	g.policyRes.resolver = fn
}

// SetScopeChecker 注入授权范围校验。enforce=false 影子告警（默认），true 逐目标拦截。
func (g *Guard) SetScopeChecker(fn ScopeChecker, enforce bool) {
	g.policyRes.mu.Lock()
	defer g.policyRes.mu.Unlock()
	g.policyRes.scope, g.policyRes.scopeEnfo = fn, enforce
}

// SetRateLimit 开启按主机限速（每分钟每目标主机）。perMinute<=0 关闭；
// enforce=false 时超限只记 warn。
func (g *Guard) SetRateLimit(perMinute int, enforce bool) {
	g.policyRes.mu.Lock()
	defer g.policyRes.mu.Unlock()
	if perMinute <= 0 {
		g.policyRes.limiter = nil
	} else {
		g.policyRes.limiter = newRateLimiter(perMinute)
	}
	g.policyRes.limitEnfo = enforce
}

func (g *Guard) resolveTool(name string) (actool.CoreTool, bool) {
	p := &g.policyRes
	p.mu.Lock()
	fn := p.resolver
	p.mu.Unlock()
	if fn == nil {
		return nil, false
	}
	return fn(name)
}

// checkScope 是门禁顺序的第 1 步（scope 在 approval 之前，对齐 capkit 顺序）：
// 解析目标 → 逐目标校验。任何一步失败按 fail closed 处置。
//
// 返回 (rejection reason, should block)。影子模式下返回 blocked=false 但记 warn。
func (g *Guard) checkScope(ctx context.Context, ev askernel.Event) (string, bool) {
	tool := ev.ToolName
	t, ok := g.resolveTool(tool)
	if !ok {
		return "", false // 无目标扩展能力的工具（含未索引的 builtins）不在 scope 门禁内
	}
	targeted, ok := t.(actool.Targeted)
	if !ok {
		return "", false // 未实现 Targeted = 门禁覆盖之外的存量工具；依赖 side_effect 观测补位
	}
	targets, err := targeted.ExtractTargets(ev.Input)
	p := &g.policyRes
	p.mu.Lock()
	checker, enforce := p.scope, p.scopeEnfo
	p.mu.Unlock()
	if err != nil {
		// fail closed（对齐 capkit ScopeChecker）：目标解析不了就不猜测。
		// 但拦不拦仍由 enforce 开关决定——影子阶段必须零行为变化。
		reason := "目标解析失败，无法确认授权范围：" + err.Error()
		if enforce {
			return reason, true
		}
		g.warn(tool, "目标解析失败（影子未拦截）： "+err.Error(), nil)
		return "", false
	}
	if checker == nil {
		g.observe(tool, "scope", "未配置授权范围校验器（影子写作态）")
		return "", false
	}
	if err := checker(ctx, tool, targets); err != nil {
		if enforce {
			return "目标不在授权范围内：" + err.Error(), true
		}
		log.Printf("[guard] 影子模式：%s 目标越界（未拦截）：%v", tool, err)
		g.warn(tool, "目标越界（影子未拦截）： "+err.Error(), targets)
		return "", false
	}
	return "", false
}

// checkRate 是门禁顺序的第 3 步（scope → approval 之后）：按目标主机计数。
// 无 Targeted 能力的工具提取不到主机，直接放行（这层兜不住 Bash 任意命令，
// 那是审计/拦截规则的职责，这里不做启发式正则拆命令——宁可少管也不误管）。
func (g *Guard) checkRate(ev askernel.Event) (string, bool) {
	p := &g.policyRes
	p.mu.Lock()
	lm, enforce := p.limiter, p.limitEnfo
	p.mu.Unlock()
	if lm == nil {
		return "", false
	}
	hosts := g.targetHosts(ev.ToolName, ev.Input)
	for _, h := range hosts {
		if allowed, limit := lm.allow(h); !allowed {
			reason := "执行频率超限：目标 " + h + " 已达 " + strconv.Itoa(limit) + " 次/分钟上限，稍后重试"
			if enforce {
				return reason, true
			}
			log.Printf("[guard] 影子模式：%s 对 %s 超限（未拦截，上限 %d/分钟）", ev.ToolName, h, limit)
			g.warn(ev.ToolName, "限速超限（影子未拦截）： "+reason, nil)
			return "", false
		}
	}
	return "", false
}

// observeSideEffect 记录自声明副作用等级的高危观测（影子，帮助切强制前校准：
// 哪些工具自声明 destructive 却从未进过人工审批）。once-per-tool 去重避免刷屏。
func (g *Guard) observeSideEffect(ev askernel.Event) {
	t, ok := g.resolveTool(ev.ToolName)
	if !ok {
		return
	}
	se := actool.SideEffectOf(t)
	switch se {
	case actool.EffectDestructive:
		g.observe(ev.ToolName, "side_effect", "自声明 destructive，但审批阈值映射仍为影子（未强制 ask）")
	case actool.EffectNetwork:
		// 全部 recipe 默认 network，不观测（否则每次扫描都会刷 warn）。
	}
}

// observe 记一条 per-tool 一次性告警（去重，避免审计环被同一件事灌满）。
func (g *Guard) observe(tool, domain, detail string) {
	p := &g.policyRes
	p.mu.Lock()
	if p.once == nil {
		p.once = map[string]*sync.Once{}
	}
	key := domain + ":" + tool
	o, ok := p.once[key]
	if !ok {
		o = &sync.Once{}
		p.once[key] = o
	}
	p.mu.Unlock()
	o.Do(func() { g.warn(tool, "["+domain+"] "+detail, nil) })
}

// warn 记一条影子观测（action=warn，历史页可见、不算 allow 也不算 block）。
func (g *Guard) warn(tool, reason string, targets []actool.Target) {
	var names []string
	for _, t := range targets {
		names = append(names, t.Host())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.audit = append(g.audit, AuditEntry{
		TS: time.Now().Unix(), Tool: tool, Action: "warn", Reason: reason, Targets: names,
	})
	if len(g.audit) > 2000 {
		g.audit = g.audit[len(g.audit)-2000:]
	}
}

// targetHosts 解析本次调用的目标主机列表（供限速做 key）。
func (g *Guard) targetHosts(name string, input json.RawMessage) []string {
	t, ok := g.resolveTool(name)
	if !ok {
		return nil
	}
	targeted, ok := t.(actool.Targeted)
	if !ok {
		return nil
	}
	targets, err := targeted.ExtractTargets(input)
	if err != nil {
		return nil // scope 已兜住解析失败，这里不再重复处置
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(targets))
	for _, tg := range targets {
		h := tg.Host()
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}
