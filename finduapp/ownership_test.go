package finduapp

import (
	"strings"
	"testing"
)

func TestAPIClaimsAreUniqueAndWellFormed(t *testing.T) {
	seen := make(map[string]APIOwner, len(APIClaims()))
	for _, c := range APIClaims() {
		if !strings.HasPrefix(c.Prefix, "/api/") || strings.HasSuffix(c.Prefix, "/") {
			t.Fatalf("invalid prefix %q", c.Prefix)
		}
		if strings.Contains(c.Prefix, "//") {
			t.Fatalf("invalid prefix %q", c.Prefix)
		}
		if c.Owner != OwnerFindu && c.Owner != OwnerDual {
			t.Fatalf("prefix %q has unknown owner %q", c.Prefix, c.Owner)
		}
		if strings.TrimSpace(c.Reason) == "" {
			t.Fatalf("prefix %q has no ownership reason", c.Prefix)
		}
		if prev, ok := seen[c.Prefix]; ok {
			t.Fatalf("prefix %q declared twice (%s then %s)", c.Prefix, prev, c.Owner)
		}
		seen[c.Prefix] = c.Owner
	}
	if _, ok := seen["/api/conversations"]; ok {
		t.Fatal("/api/conversations migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/skills"]; ok {
		t.Fatal("/api/skills migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/tasks"]; ok {
		t.Fatal("/api/tasks migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/assets"]; ok {
		t.Fatal("/api/assets migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/audit"]; ok {
		t.Fatal("/api/audit migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/projects"]; ok {
		t.Fatal("/api/projects migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/webshell"]; ok {
		t.Fatal("/api/webshell migrated to host and must not be findu-owned")
	}
	if _, ok := seen["/api/multi-agent"]; ok {
		t.Fatal("/api/multi-agent must stay host-owned")
	}
}

func TestOnlyC2RemainsFinduOwned(t *testing.T) {
	paths := OwnedAPIPaths()
	if len(paths) != 1 || paths[0] != "/api/c2" {
		t.Fatalf("OwnedAPIPaths() = %v, want only /api/c2 (external C2 slot)", paths)
	}
}

func TestOwnedAPIPathsDerivedFromClaims(t *testing.T) {
	claims := APIClaims()
	paths := OwnedAPIPaths()
	if len(paths) != len(claims) {
		t.Fatalf("paths=%d claims=%d", len(paths), len(claims))
	}

	for i, c := range claims {
		if paths[i] != c.Prefix {
			t.Fatalf("paths[%d]=%q, want %q", i, paths[i], c.Prefix)
		}
	}
}
