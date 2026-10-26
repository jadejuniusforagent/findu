// Package toolguard applies configurable blocking rules before tool execution.
// It inspects tool names and arguments, including JSON string values and common
// percent escapes. It does not resolve hosts or inspect redirects, files, or
// arbitrary encoded payloads, and is not an exhaustive target authorization check.
package toolguard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

const (
	MaxRules         = 100
	MaxIDLength      = 128
	MaxNameLength    = 200
	MaxPatternLength = 4096
	MaxMessageLength = 4096

	// Allow patterns bound separately; they exempt every rule rather than block.
	MaxAllowPatterns      = 500
	MaxAllowPatternLength = 4096

	// The named group keeps surrounding boundary punctuation out of the reminder.
	governmentDomainPattern = `(?i)(?:^|[^\p{L}\p{M}\p{N}_.-])(?P<match>(?:(?:[\p{L}\p{M}\p{N}_*-]+\.)+gov(?:\.[\p{L}\p{M}\p{N}_*-]+)*|gov(?:\.[\p{L}\p{M}\p{N}_*-]+)+|\.gov(?:\.[\p{L}\p{M}\p{N}_*-]+)*)\.?)(?:$|[^\p{L}\p{M}\p{N}_.-])`
	defaultMessage          = "识别到 {match}，工具调用已被安全规则「{rule}」拦截，请检查目标与授权范围后再试。"

	// ActionDeny blocks the call (the default when a rule omits the action).
	ActionDeny = "deny"
	// ActionAllow short-circuits: lower-priority rules are not evaluated for this call.
	ActionAllow = "allow"
	// ActionAsk suspends the call for human adjudication; callers without an adjudication
	// channel must treat it as deny (fail-closed).
	ActionAsk = "ask"
)

// NormalizeAction maps action aliases to the canonical value; an empty action means deny.
// Unknown actions are rejected so a typo cannot silently downgrade a rule to allow.
func NormalizeAction(action string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "deny", "block", "拦截", "拒绝":
		return ActionDeny, nil
	case "allow", "permit", "pass", "放行":
		return ActionAllow, nil
	case "ask", "confirm", "ask_user", "hitl", "人工", "询问":
		return ActionAsk, nil
	default:
		return "", fmt.Errorf("unknown action %q (want deny/allow/ask)", action)
	}
}

type Config struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Rules   []Rule `json:"rules" yaml:"rules"`
	// AllowPatterns holds RE2 expressions evaluated against the text a rule
	// matched. When any entry matches, that rule is skipped for the call, so a
	// curated read-only reference host (for example nvd.nist.gov, which lives
	// under .gov) stays reachable while the rest of the matched space keeps its
	// protection. Exemptions are global: they apply to every rule.
	AllowPatterns []string `json:"allowPatterns" yaml:"allow_patterns"`
}

type Rule struct {
	ID      string `json:"id" yaml:"id"`
	Name    string `json:"name" yaml:"name"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Pattern string `json:"pattern" yaml:"pattern"`
	Message string `json:"message" yaml:"message"`
	// Priority orders evaluation: higher values are checked first; equal priorities keep
	// configuration order. Zero is the default.
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`
	// Action is what a match does: deny (default) / allow / ask. Empty means deny.
	Action string `json:"action,omitempty" yaml:"action,omitempty"`
}

type Match struct {
	RuleID      string `json:"ruleId"`
	RuleName    string `json:"ruleName"`
	MatchedText string `json:"matchedText"`
	Message     string `json:"message"`
}

// Decision is the outcome of a policy evaluation: allow short-circuits (later, lower-priority
// rules are skipped), deny blocks, ask needs human adjudication. Check only surfaces deny/ask,
// so allow is invisible to callers that can only express block/allow.
type Decision struct {
	Action      string `json:"action"`
	RuleID      string `json:"ruleId"`
	RuleName    string `json:"ruleName"`
	MatchedText string `json:"matchedText"`
	Message     string `json:"message"`
}

// toMatch projects a decision onto the legacy block/allow result.
func (d *Decision) toMatch() *Match {
	if d == nil || d.Action == ActionAllow {
		return nil
	}
	return &Match{RuleID: d.RuleID, RuleName: d.RuleName, MatchedText: d.MatchedText, Message: d.Message}
}

type compiledRule struct {
	rule       Rule
	pattern    *regexp.Regexp
	matchGroup int
}

// Policy is an immutable snapshot safe for concurrent checks.
type Policy struct {
	config        Config
	rules         []compiledRule
	allowPatterns []*regexp.Regexp
}

// DefaultConfig enables a conservative government-domain rule, including .gov,
// .gov.cn and wildcard forms such as *.gov.*. Additional rules can be configured.
// The default allow list keeps well-known vulnerability databases reachable.
func DefaultConfig() Config {
	return Config{
		Enabled:       true,
		AllowPatterns: DefaultAllowPatterns(),
		Rules: []Rule{{
			ID:      "government-domains",
			Name:    "政府网站保护",
			Enabled: true,
			Pattern: governmentDomainPattern,
			Message: "识别到 {match}，禁止攻击政府网站。请检查目标与授权范围，并更换为已获授权的非政府目标。",
		}},
	}
}

// DefaultAllowPatterns lists well-known vulnerability databases and advisory
// feeds. The government-domain rule matches every .gov host, which would also
// block read-only lookups such as the NVD API, so those hosts are exempted while
// the rest of the .gov space keeps its protection. Entries are RE2 expressions
// tested against the matched text; only the .gov entries change behaviour today,
// the remainder are listed so the trust list stays in one place for future rules.
func DefaultAllowPatterns() []string {
	return []string{
		// 权威漏洞库（.gov）
		`(?i)\bnvd\.nist\.gov\b`,    // NVD，含 services.nvd.nist.gov
		`(?i)\bcsrc\.nist\.gov\b`,   // NIST CSRC
		`(?i)\bcisa\.gov\b`,         // CISA，含 KEV 已知被利用漏洞目录
		`(?i)\bwww\.us-cert\.gov\b`, // US-CERT（已并入 CISA）
		// MITRE / CVE
		`(?i)\bcve\.org\b`,
		`(?i)\bcve\.mitre\.org\b`,
		// 聚合 / 索引
		`(?i)\bcvefeed\.io\b`,
		`(?i)\bcve\.report\b`,
		`(?i)\bcvedetails\.com\b`,
		`(?i)\bvuldb\.com\b`,
		`(?i)\bvulmon\.com\b`,
		`(?i)\bvulners\.com\b`,
		`(?i)\bosv\.dev\b`, // Google OSV
		`(?i)\battackerkb\.com\b`,
		// 发行版 / 厂商公告
		`(?i)\bsecurity-tracker\.debian\.org\b`,
		`(?i)\bsecurity\.ubuntu\.com\b`,
		`(?i)\baccess\.redhat\.com\b`,
		`(?i)\bmsrc\.microsoft\.com\b`,
		`(?i)\bkb\.cert\.org\b`, // CERT/CC
		// 利用 / PoC
		`(?i)\bexploit-db\.com\b`,
		`(?i)\bpacketstormsecurity\.com\b`,
		`(?i)\bcxsecurity\.com\b`,
		`(?i)\b0day\.today\b`,
	}
}

// Compile validates every rule, including disabled ones. Limits are byte counts.
// Rule order determines precedence. An optional named (?P<match>...) group selects
// the text inserted into {match}; otherwise the entire regex match is used.
func Compile(config Config) (*Policy, error) {
	if len(config.Rules) > MaxRules {
		return nil, fmt.Errorf("tool guard: at most %d rules are allowed", MaxRules)
	}
	if len(config.AllowPatterns) > MaxAllowPatterns {
		return nil, fmt.Errorf("tool guard: at most %d allow patterns are allowed", MaxAllowPatterns)
	}
	policy := &Policy{config: cloneConfig(config)}
	// Blank entries are ignored so trailing newlines survive a hand-edited list.
	for i, raw := range policy.config.AllowPatterns {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if len(raw) > MaxAllowPatternLength {
			return nil, fmt.Errorf("tool guard allow pattern %d: must be at most %d bytes", i+1, MaxAllowPatternLength)
		}
		pattern, err := regexp.Compile(raw)
		if err != nil {
			return nil, fmt.Errorf("tool guard allow pattern %d: invalid regular expression: %w", i+1, err)
		}
		policy.allowPatterns = append(policy.allowPatterns, pattern)
	}
	ids := make(map[string]struct{}, len(config.Rules))
	for i, rule := range policy.config.Rules {
		prefix := fmt.Sprintf("tool guard rule %d", i+1)
		if strings.TrimSpace(rule.ID) == "" || len(rule.ID) > MaxIDLength {
			return nil, fmt.Errorf("%s: id must be nonempty and at most %d bytes", prefix, MaxIDLength)
		}
		if rule.ID != strings.TrimSpace(rule.ID) {
			return nil, fmt.Errorf("%s: id must not have surrounding whitespace", prefix)
		}
		if _, exists := ids[rule.ID]; exists {
			return nil, fmt.Errorf("%s: duplicate id %q", prefix, rule.ID)
		}
		ids[rule.ID] = struct{}{}
		if strings.TrimSpace(rule.Name) == "" || len(rule.Name) > MaxNameLength {
			return nil, fmt.Errorf("%s: name must be nonempty and at most %d bytes", prefix, MaxNameLength)
		}
		if strings.TrimSpace(rule.Pattern) == "" || len(rule.Pattern) > MaxPatternLength {
			return nil, fmt.Errorf("%s: pattern must be nonempty and at most %d bytes", prefix, MaxPatternLength)
		}
		if len(rule.Message) > MaxMessageLength {
			return nil, fmt.Errorf("%s: message must be at most %d bytes", prefix, MaxMessageLength)
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): invalid regular expression: %w", prefix, rule.ID, err)
		}
		// Normalise here so a typo cannot silently turn a rule into something other than
		// what was configured. The canonical action lives on the compiled rule ONLY: the
		// stored config (Policy.config / Manager.Config()) stays byte-identical to what the
		// caller supplied, so save→load→compare round-trips and dry-run-vs-live checks keep
		// matching. Callers that need the effective action read it from Decide's Decision.
		action, actionErr := NormalizeAction(rule.Action)
		if actionErr != nil {
			return nil, fmt.Errorf("%s (%s): %w", prefix, rule.ID, actionErr)
		}
		rule.Action = action
		if rule.Enabled {
			policy.rules = append(policy.rules, compiledRule{rule: rule, pattern: pattern, matchGroup: pattern.SubexpIndex("match")})
		}
	}
	// Higher priority first; equal priorities keep configuration order (stable).
	sort.SliceStable(policy.rules, func(i, j int) bool {
		return policy.rules[i].rule.Priority > policy.rules[j].rule.Priority
	})
	return policy, nil
}

// Decide evaluates the rules in precedence order and reports what should happen.
//
// Every match in a candidate is evaluated, so an exempted host cannot mask a real target that
// appears later in the same string. A matching allow rule short-circuits — lower-priority rules
// are not evaluated for that call. A nil decision means no rule matched (the call may proceed).
// Args should be JSON-compatible and must not be mutated while Decide is running.
func (p *Policy) Decide(toolName string, args map[string]interface{}) *Decision {
	if p == nil || !p.config.Enabled || len(p.rules) == 0 {
		return nil
	}
	candidates := candidateTexts(toolName, args)
	for _, rule := range p.rules {
		for _, candidate := range candidates {
			for _, indices := range rule.pattern.FindAllStringSubmatchIndex(candidate, -1) {
				start, end := indices[0], indices[1]
				if group := rule.matchGroup; group > 0 && indices[group*2] >= 0 {
					start, end = indices[group*2], indices[group*2+1]
				}
				matched := candidate[start:end]
				if p.allowMatch(matched) {
					continue
				}
				message := rule.rule.Message
				if strings.TrimSpace(message) == "" {
					message = defaultMessage
				}
				// A single replacement pass prevents matched text from
				// introducing additional template substitutions.
				message = strings.NewReplacer("{match}", matched, "{tool}", toolName, "{rule}", rule.rule.Name).Replace(message)
				action := rule.rule.Action
				if action == "" {
					action = ActionDeny
				}
				return &Decision{Action: action, RuleID: rule.rule.ID, RuleName: rule.rule.Name, MatchedText: matched, Message: message}
			}
		}
	}
	return nil
}

// Check is the legacy block/allow gate: allow reads as pass, deny/ask read as blocked. Callers
// that can route ask to a human must call Decide instead.
func (p *Policy) Check(toolName string, args map[string]interface{}) *Match {
	return p.Decide(toolName, args).toMatch()
}

// allowMatch reports whether the text a rule matched is on the exemption list.
func (p *Policy) allowMatch(text string) bool {
	for _, pattern := range p.allowPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// Manager atomically replaces validated policy snapshots for live settings.
type Manager struct {
	policy atomic.Pointer[Policy]
}

func NewManager(config Config) (*Manager, error) {
	m := &Manager{}
	if err := m.Update(config); err != nil {
		return nil, err
	}
	return m, nil
}

// Update retains the active policy if validation fails.
func (m *Manager) Update(config Config) error {
	policy, err := Compile(config)
	if err != nil {
		return err
	}
	m.policy.Store(policy)
	return nil
}

func (m *Manager) Config() Config {
	if m == nil {
		return Config{}
	}
	if policy := m.policy.Load(); policy != nil {
		return cloneConfig(policy.config)
	}
	return Config{}
}

func (m *Manager) Check(toolName string, args map[string]interface{}) *Match {
	if m == nil {
		return nil
	}
	return m.policy.Load().Check(toolName, args)
}

// Decide returns the full decision (including allow) so a caller can route ask to a human.
func (m *Manager) Decide(toolName string, args map[string]interface{}) *Decision {
	if m == nil {
		return nil
	}
	return m.policy.Load().Decide(toolName, args)
}

func cloneConfig(config Config) Config {
	if config.Rules != nil {
		rules := make([]Rule, len(config.Rules))
		copy(rules, config.Rules)
		config.Rules = rules
	}
	if config.AllowPatterns != nil {
		allow := make([]string, len(config.AllowPatterns))
		copy(allow, config.AllowPatterns)
		config.AllowPatterns = allow
	}
	return config
}

func candidateTexts(toolName string, args map[string]interface{}) []string {
	var candidates []string
	seen := make(map[string]struct{})
	add := func(value string) {
		// Decode at most three rounds to cover common nested URL escaping
		// without claiming support for arbitrarily encoded tool inputs.
		for round := 0; round <= 3; round++ {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				candidates = append(candidates, value)
			}
			decoded := decodePercentEscapes(value)
			if decoded == value {
				break
			}
			value = decoded
		}
	}
	add(toolName)
	var walk func(interface{}, int)
	walk = func(value interface{}, remainingDepth int) {
		if remainingDepth == 0 {
			return
		}
		switch value := value.(type) {
		case string:
			add(value)
		case map[string]interface{}:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				add(key)
				walk(value[key], remainingDepth-1)
			}
		case []interface{}:
			for _, item := range value {
				walk(item, remainingDepth-1)
			}
		}
	}
	// Unmarshaling also normalizes typed slices/maps, json.RawMessage and
	// escaped JSON keys/values into the recursive representation above.
	if raw, err := json.Marshal(args); err == nil {
		var value interface{}
		if json.Unmarshal(raw, &value) == nil {
			// encoding/json accepts at most 10,000 nesting levels. The decoded
			// tree is acyclic, so inspect every accepted string/key at that depth.
			walk(value, 10001)
		}
		add(string(raw))
	} else {
		// MCP rejects invalid JSON arguments independently; still inspect the
		// ordinary values if a caller supplies a non-JSON value alongside them.
		walk(args, 128)
	}
	return candidates
}

// Decode valid percent triplets even if another part of the string has a stray
// percent sign. Whole-string URL unescaping otherwise misses such mixed inputs.
func decodePercentEscapes(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	var out strings.Builder
	out.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			hi, okHi := hexValue(value[i+1])
			lo, okLo := hexValue(value[i+2])
			if okHi && okLo {
				out.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		out.WriteByte(value[i])
	}
	return out.String()
}

func hexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
