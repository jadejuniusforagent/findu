package ownership

import "testing"

func TestValidatePluginTables(t *testing.T) {
	if err := ValidatePluginTables("c2", []string{"c2_sessions", "c2_listeners"}); err != nil {
		t.Fatalf("c2 own tables should pass, got: %v", err)
	}
	if err := ValidatePluginTables("c2", []string{"assets"}); err == nil {
		t.Fatal("domain table should be rejected")
	}
	if err := ValidatePluginTables("notify", []string{"c2_events"}); err == nil {
		t.Fatal("cross-plugin table should be rejected")
	}
	if err := ValidatePluginTables("c2", []string{"no_such_table"}); err == nil {
		t.Fatal("unregistered table should be rejected")
	}
}

func TestCanonicalIsCore(t *testing.T) {
	for _, tbl := range []string{"canonical_session", "canonical_turn", "canonical_message", "canonical_tool_call", "canonical_artifact"} {
		o, ok := TableOwner(tbl)
		if !ok || o != "core" {
			t.Fatalf("%s should be core, got %q ok=%v", tbl, o, ok)
		}
	}
}

func TestPluginTablesCount(t *testing.T) {
	if got := len(PluginTables("intel")); got != 4 {
		t.Fatalf("intel should own 4 tables, got %d", got)
	}
	if got := len(PluginTables("c2")); got != 7 {
		t.Fatalf("c2 should own 7 tables, got %d", got)
	}
}
