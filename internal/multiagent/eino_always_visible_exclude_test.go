package multiagent

import "testing"

func alwaysVisibleContains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// The builtin merge force-adds every backend builtin; the exclude list must be able to
// demote those too, otherwise families such as webshell/batch-task stay resident forever.
func TestMergeAlwaysVisibleToolNamesExclude(t *testing.T) {
	base := mergeAlwaysVisibleToolNames([]string{"exec"}, nil)
	if !alwaysVisibleContains(base, "exec") {
		t.Fatal("configured name must be present")
	}
	if len(base) <= 1 {
		t.Fatal("expected the builtin merge to add names")
	}

	got := mergeAlwaysVisibleToolNames([]string{"exec"}, []string{"webshell_exec", "  batch_task_list  "})
	if alwaysVisibleContains(got, "webshell_exec") {
		t.Fatal("excluded builtin must be demoted")
	}
	if alwaysVisibleContains(got, "batch_task_list") {
		t.Fatal("exclude entries must be trimmed before matching")
	}
	if !alwaysVisibleContains(got, "exec") {
		t.Fatal("non-excluded names must survive")
	}
	if len(got) != len(base)-2 {
		t.Fatalf("want %d names after demoting 2, got %d", len(base)-2, len(got))
	}

	// An all-blank exclude list is a no-op, so an empty YAML list cannot silently empty
	// the resident set.
	if blank := mergeAlwaysVisibleToolNames([]string{"exec"}, []string{"", "   "}); len(blank) != len(base) {
		t.Fatalf("blank exclude must be a no-op: want %d, got %d", len(base), len(blank))
	}
}
