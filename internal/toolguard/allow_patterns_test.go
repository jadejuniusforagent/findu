package toolguard

import "testing"

// The government-domain rule matches every .gov host, so read-only vulnerability
// database lookups must be exempted without loosening the rest of the space.
func TestDefaultAllowPatternsExemptVulnerabilityDatabases(t *testing.T) {
	policy, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"curl -s https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2021-34436",
		"curl https://nvd.nist.gov/vuln/detail/CVE-2021-34436",
		"curl https://www.cisa.gov/known-exploited-vulnerabilities-catalog",
		"curl https://csrc.nist.gov/pubs",
	} {
		t.Run("allowed "+input, func(t *testing.T) {
			if match := policy.Check("exec", map[string]interface{}{"command": input}); match != nil {
				t.Fatalf("unexpected match: %+v", match)
			}
		})
	}
	for _, input := range []string{
		"curl https://agency.gov/login",
		"curl https://www.agency.gov.cn/",
		"curl https://www.whitehouse.gov/",
	} {
		t.Run("blocked "+input, func(t *testing.T) {
			if match := policy.Check("exec", map[string]interface{}{"command": input}); match == nil {
				t.Fatalf("expected a match for %q", input)
			}
		})
	}
}

func TestAllowPatternsApplyToEveryRule(t *testing.T) {
	config := Config{
		Enabled:       true,
		AllowPatterns: []string{`(?i)\btrusted\.example\b`},
		Rules: []Rule{
			{ID: "gov", Name: "政府网站保护", Enabled: true, Pattern: governmentDomainPattern, Message: "hit {match}"},
			{ID: "custom", Name: "自定义", Enabled: true, Pattern: `(?i)\bblocked\.example\b`, Message: "hit {match}"},
		},
	}
	policy, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if match := policy.Check("http_request", map[string]interface{}{"url": "https://trusted.example/path"}); match != nil {
		t.Fatalf("allow pattern should exempt the custom rule too: %+v", match)
	}
	if match := policy.Check("http_request", map[string]interface{}{"url": "https://blocked.example/path"}); match == nil {
		t.Fatal("unrelated host must still be blocked")
	}
}

// A payload may mention an exempted host alongside a real target; the target
// must still be blocked.
func TestAllowPatternDoesNotHideOtherTargets(t *testing.T) {
	policy, err := Compile(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{
		"command": "curl https://services.nvd.nist.gov/rest/json/cves/2.0 && curl https://agency.gov/login",
	}
	if match := policy.Check("exec", args); match == nil {
		t.Fatal("the government target must still be blocked")
	}
}

func TestCompileRejectsInvalidAllowPattern(t *testing.T) {
	config := DefaultConfig()
	config.AllowPatterns = []string{"("}
	if _, err := Compile(config); err == nil {
		t.Fatal("expected an error for an invalid allow pattern")
	}
}

func TestCompileIgnoresBlankAllowPatterns(t *testing.T) {
	config := Config{Enabled: true, AllowPatterns: []string{"", "  ", `(?i)\bnvd\.nist\.gov\b`}}
	if _, err := Compile(config); err != nil {
		t.Fatalf("blank entries should be skipped: %v", err)
	}
}
