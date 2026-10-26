// Package tools — targets: optional per-target gate surface.
//
// capkit 的契约原则在这里的最小落法：作用外部目标的工具必须能枚举「这次调用打谁」，
// 门禁才能逐目标做授权校验（fail closed：解析不出就拒绝，不猜测）。
// 这里只定义形状与提取规则；校验策略在 guard 里（影子模式起步，见 guard/policy.go）。
//
// 扩展用可选接口 + 类型断言（与 AcceptsRawInput 同一模式），刻意不给 CoreTool 加方法：
// 那会 break 每一个实现 CoreTool 的宿主与工具。声明来源三级：
//  1. 工具自实现 Targeted（深度集成，如双图/流量工具）
//  2. 配方在 tool-contract/YAML 里写 targets 声明（推荐，数据驱动）
//  3. 无声明时按 CommonTargetFields 通配键名兜底（透明但宽松，guard 侧只做影子观测）
package tools

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// TargetKind enumerates target shapes; values must stay in lockstep with the
// kind enum in contracts/tool-contract.schema.json.
type TargetKind string

const (
	TargetKindDomain TargetKind = "domain"
	TargetKindIP     TargetKind = "ip"
	TargetKindCIDR   TargetKind = "cidr"
	TargetKindURL    TargetKind = "url"
)

// Target is one thing a tool call acts on. Value keeps the raw form; Host
// returns the normalized identity the gate keys off (rate limiting / scope).
type Target struct {
	Kind  TargetKind `json:"kind"`
	Value string     `json:"value"`
}

// Host returns the gate identity of this target: the bare host for URLs and
// host:port-ish values, the IP, or the CIDR verbatim. "(unparsable)" never
// happens here — ParseTarget refuses to build such a Target (fail closed).
func (t Target) Host() string {
	switch t.Kind {
	case TargetKindURL:
		if u, err := url.Parse(t.Value); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
		if host, _, err := net.SplitHostPort(t.Value); err == nil {
			return host
		}
	case TargetKindDomain, TargetKindIP:
		if host, _, err := net.SplitHostPort(t.Value); err == nil && len(host) > 0 {
			return host
		}
	}
	return t.Value
}

// Targeted is the optional gate extension: a tool whose side effects reach
// external targets declares how targets are pulled from its input.
//
// Contract: ExtractTargets returns err when a target cannot be parsed — the
// gate treats that the same way a ScopeChecker failure is treated (refuse,
// never guess). A tool may return an empty list when none of its parameters
// resolve to a target; the gate then treats the call as non-targeted.
type Targeted interface {
	ExtractTargets(input json.RawMessage) ([]Target, error)
}

// SideEffector is the optional second extension: a tool self-declares its
// side-effect level, mirroring side_effect in the tool contract. The guard
// observes declared "destructive"/"network" tools for the (shaded) approval
// threshold mapping; an undeclared tool reads as the empty string.
type SideEffector interface{ SideEffect() string }

// Side-effect levels; values mirror the side_effect enum in the contract.
const (
	EffectNone        = "none"
	EffectRead        = "read"
	EffectWrite       = "write"
	EffectDestructive = "destructive"
	EffectNetwork     = "network"
)

// SideEffectOf resolves a tool's declared side-effect level ("" = undeclared).
func SideEffectOf(t CoreTool) string {
	if se, ok := t.(SideEffector); ok {
		return se.SideEffect()
	}
	return ""
}

// TargetDecl is one field-target mapping as written in the contract's targets
// array (or the recipe YAML targets declaration).
type TargetDecl struct {
	Field string `json:"field"` // dot-separated path into the tool input,
	Kind  string `json:"kind"`  // domain|ip|cidr|url
}

// ExtractTargetsFromInput is the blanket extractor for declared rules: reads
// each field's value (string or []string anywhere under the dot-path) and
// parses every entry into a Target. A declared field whose value fails to
// parse yields an error — fail closed, not skip.
func ExtractTargetsFromInput(input json.RawMessage, decls []TargetDecl) ([]Target, error) {
	if len(decls) == 0 {
		return nil, nil
	}
	var root any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &root); err != nil {
			return nil, err
		}
	}
	var out []Target
	for _, d := range decls {
		for _, raw := range valuesAt(root, d.Field) {
			t, ok := ParseTarget(raw)
			if !ok {
				return nil, fmt.Errorf("fail closed: 目标字段 %s 的值 %q 不是可解析的目标", d.Field, raw)
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// CommonTargetFields is the recipe fallback: common key names of target-ish
// parameters. Used only when a tool declares no targets at all; the guard
// treats fallback-extracted targets as advisory (shadow) evidence.
var CommonTargetFields = []string{
	"target", "targets",
	"host", "hosts",
	"ip", "ips",
	"domain", "domains",
	"url", "urls",
	"endpoint",
}

// ExtractTargetsFromCommonFields walks the input tree and pulls values from
// the common target keys. It never errors on unparsable strings — a fallback
// scan must stay conservative: non-target words (paths, flags, secrets) share
// these key names too rarely to block on, and gating is driven by real
// declarations instead.
func ExtractTargetsFromCommonFields(input json.RawMessage) ([]Target, error) {
	var root any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &root); err != nil {
			return nil, err
		}
	}
	var out []Target
	decls := make([]TargetDecl, len(CommonTargetFields))
	for i, f := range CommonTargetFields {
		decls[i] = TargetDecl{Field: f, Kind: ""}
	}
	for _, d := range decls {
		for _, raw := range valuesAt(root, d.Field) {
			if t, ok := ParseTarget(raw); ok {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// ParseTarget classifies one raw value into a Target. Detection order:
// URL (has a :// scheme) → IP → CIDR → domain (dotted name or localhost).
// Anything else (flags, paths, free text) returns ok=false — not a target.
func ParseTarget(raw string) (Target, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return Target{}, false
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err == nil && u.Hostname() != "" {
			return Target{Kind: TargetKindURL, Value: raw}, true
		}
		return Target{}, false
	}
	if ip := net.ParseIP(raw); ip != nil {
		return Target{Kind: TargetKindIP, Value: raw}, true
	}
	if ip, _, err := net.ParseCIDR(raw); err == nil && ip != nil {
		return Target{Kind: TargetKindCIDR, Value: raw}, true
	}
	host := raw
	if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	}
	if strings.Contains(host, ".") || strings.EqualFold(host, "localhost") {
		return Target{Kind: TargetKindDomain, Value: raw}, true
	}
	return Target{}, false
}

// valuesAt resolves a dot-separated path into every string / []string leaf it
// passes through, tolerating arrays along the way. A missing path yields
// nothing (declared-but-absent is normal for optional args).
func valuesAt(root any, path string) []string {
	var out []string
	cur := []any{root}
	for _, seg := range strings.Split(path, ".") {
		var next []any
		for _, node := range cur {
			switch v := node.(type) {
			case map[string]any:
				if child, ok := v[seg]; ok {
					next = append(next, child)
				}
			case []any:
				for _, item := range v {
					if m, ok := item.(map[string]any); ok {
						if child, ok := m[seg]; ok {
							next = append(next, child)
						}
					}
				}
			}
		}
		cur = next
	}
	var collect func(v any)
	collect = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case []any:
			for _, item := range x {
				collect(item)
			}
		}
	}
	for _, node := range cur {
		collect(node)
	}
	return out
}
