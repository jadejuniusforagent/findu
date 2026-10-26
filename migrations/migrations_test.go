package migrations_test

import (
	"testing"

	"finduai/core/migrate"
	"finduai/migrations"
)

func TestCoreMigrationsLoad(t *testing.T) {
	ms, err := migrate.Load(migrations.CoreFS, "core")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 2 {
		t.Fatalf("expected at least 2 migrations, got %d", len(ms))
	}
	for _, m := range ms {
		if m.Up == "" {
			t.Fatalf("%s missing up script", m.Version)
		}
		if m.Down == "" {
			t.Fatalf("%s missing down script (回滚能力是硬要求)", m.Version)
		}
	}
	if ms[0].Version != "0001_canonical" {
		t.Fatalf("first migration should be 0001_canonical, got %s", ms[0].Version)
	}
}
