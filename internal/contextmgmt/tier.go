// Package contextmgmt owns the context-budget tiers of the production (agentic) chain.
//
// It decides *when* a request input needs trimming and executes the cheapest tier
// (folding the oldest complete rounds). Summarization and overflow recovery stay with the
// existing multiagent middlewares; this package only attributes those tiers.
//
// Hard constraint: outside the snip tier nothing rewrites the history prefix. Prefix cache
// hit rate is a platform-level constraint (FindUAI measured 92%), so the snip watermark sits
// below the summarization trigger and the fold target recovers well below both — otherwise
// every round would rewrite the prefix and destroy the cache.
package contextmgmt

// Tier is the context watermark tier for a single model call.
type Tier int

const (
	// TierNormal is within budget: the middleware is a no-op.
	TierNormal Tier = iota
	// TierSnip is at the fold watermark: drop the oldest complete rounds (executed here).
	TierSnip
	// TierCompact is at the summarization watermark (executed by multiagent summarization).
	TierCompact
	// TierCollapse is at/over budget (executed by multiagent overflow retry).
	TierCollapse
)

// String returns the tier name used in logs and tests.
func (t Tier) String() string {
	switch t {
	case TierSnip:
		return "snip"
	case TierCompact:
		return "compact"
	case TierCollapse:
		return "collapse"
	default:
		return "normal"
	}
}

// Policy is the tiered budget policy. Watermarks are fractions of MaxTotalTokens; zero
// values fall back to the defaults documented on each field.
type Policy struct {
	// MaxTotalTokens is the per-request budget (same source as the model channel budget).
	MaxTotalTokens int
	// SnipWatermark triggers folding (default 0.90).
	SnipWatermark float64
	// CompactWatermark is the summarization tier (default 0.95; attribution only).
	CompactWatermark float64
	// SnipTargetFactor is the recovery target after folding (default 0.75) — leaving headroom
	// for a round's growth is what prevents consecutive prefix rewrites.
	SnipTargetFactor float64
}

// DefaultPolicy derives the default policy from the request budget.
func DefaultPolicy(maxTotalTokens int) Policy {
	return Policy{MaxTotalTokens: maxTotalTokens}
}

func (p Policy) snipWatermark() float64 {
	if p.SnipWatermark > 0 {
		return p.SnipWatermark
	}
	return 0.90
}

func (p Policy) compactWatermark() float64 {
	if p.CompactWatermark > 0 {
		return p.CompactWatermark
	}
	return 0.95
}

// SnipTargetTokens is the token target to recover to when folding.
func (p Policy) SnipTargetTokens() int {
	f := p.SnipTargetFactor
	if f <= 0 || f >= 1 {
		f = 0.75
	}
	return int(float64(p.MaxTotalTokens) * f)
}

// Decide maps an estimated input size to a tier (pure function). Without a usable budget it
// always returns TierNormal so an unconfigured platform keeps its previous behaviour.
func Decide(p Policy, inputTokens int) (Tier, int) {
	if p.MaxTotalTokens <= 0 || inputTokens <= 0 {
		return TierNormal, 0
	}
	f := float64(inputTokens) / float64(p.MaxTotalTokens)
	target := p.SnipTargetTokens()
	switch {
	case f >= 1.0:
		return TierCollapse, target
	case f >= p.compactWatermark():
		return TierCompact, target
	case f >= p.snipWatermark():
		return TierSnip, target
	default:
		return TierNormal, 0
	}
}
