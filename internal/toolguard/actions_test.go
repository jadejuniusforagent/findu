package toolguard

import "testing"

func TestNormalizeActionAliases(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ActionDeny},
		{"deny", ActionDeny},
		{"  BLOCK ", ActionDeny},
		{"拦截", ActionDeny},
		{"allow", ActionAllow},
		{"permit", ActionAllow},
		{"放行", ActionAllow},
		{"ask", ActionAsk},
		{"HITL", ActionAsk},
		{"人工", ActionAsk},
	} {
		got, err := NormalizeAction(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("NormalizeAction(%q) = (%q,%v), want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := NormalizeAction("maybe"); err == nil {
		t.Fatal("an unknown action must be rejected so a typo cannot change a rule's effect")
	}
}

func TestCompileRejectsUnknownAction(t *testing.T) {
	cfg := Config{Enabled: true, Rules: []Rule{{ID: "r", Name: "r", Enabled: true, Pattern: "x", Action: "nope"}}}
	if _, err := Compile(cfg); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}

// A higher-priority allow rule must short-circuit a lower-priority deny rule, and Check must
// report the allow as a pass.
func TestDecideAllowShortCircuits(t *testing.T) {
	cfg := Config{Enabled: true, Rules: []Rule{
		{ID: "deny-gov", Name: "政府网站保护", Enabled: true, Priority: 10, Action: ActionDeny, Pattern: governmentDomainPattern, Message: "hit {match}"},
		{ID: "allow-lab", Name: "实验室豁免", Enabled: true, Priority: 100, Action: ActionAllow, Pattern: `(?i)\blab\.agency\.gov\b`, Message: "exception"},
	}}
	policy, err := Compile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{"target": "https://lab.agency.gov/x"}
	// Decide surfaces allow so a caller can distinguish it from "no rule matched".
	if d := policy.Decide("http_request", args); d == nil || d.Action != ActionAllow {
		t.Fatalf("allow must win over the lower-priority deny rule: %+v", d)
	}
	if m := policy.Check("http_request", args); m != nil {
		t.Fatalf("Check must read allow as a pass: %+v", m)
	}
	// A different host under the same deny rule is still blocked.
	if d := policy.Decide("http_request", map[string]interface{}{"target": "https://other.agency.gov/x"}); d == nil || d.Action != ActionDeny {
		t.Fatalf("deny rule must still apply: %+v", d)
	}
}

func TestDecideReportsAskWithCanonicalAction(t *testing.T) {
	cfg := Config{Enabled: true, Rules: []Rule{
		{ID: "ask-gov", Name: "政府网站保护", Enabled: true, Action: "人工", Pattern: governmentDomainPattern, Message: "hit {match}"},
	}}
	policy, err := Compile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d := policy.Decide("http_request", map[string]interface{}{"target": "https://agency.gov/login"})
	if d == nil || d.Action != ActionAsk {
		t.Fatalf("decision = %+v, want ask", d)
	}
	// The legacy gate cannot express ask, so it must block (fail-closed).
	if m := policy.Check("http_request", map[string]interface{}{"target": "https://agency.gov/login"}); m == nil {
		t.Fatal("Check must block an ask decision")
	}
	// Compile must not mutate the caller's config: the canonical action is reported by
	// Decide, never written back into the stored config. The API round-trip tests
	// (save→load→compare, dry-run vs live) depend on this invariance.
	if cfg.Rules[0].Action != "人工" {
		t.Fatalf("Compile mutated caller config action = %q, want original %q", cfg.Rules[0].Action, "人工")
	}
}
