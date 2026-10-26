package migrate

import (
	"testing"
	"testing/fstest"
)

func TestLoadSortsByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"core/0002_b.up.sql":   {Data: []byte("SELECT 2;")},
		"core/0002_b.down.sql": {Data: []byte("SELECT 22;")},
		"core/0001_a.up.sql":   {Data: []byte("SELECT 1;")},
		"core/readme.txt":      {Data: []byte("ignored")},
	}
	ms, err := Load(fsys, "core")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("should load 2 migrations, got %d", len(ms))
	}
	if ms[0].Version != "0001_a" || ms[1].Version != "0002_b" {
		t.Fatalf("bad order: %s, %s", ms[0].Version, ms[1].Version)
	}
	if ms[1].Down == "" {
		t.Fatal("down script of 0002_b not loaded")
	}
}

func TestLoadMissingDir(t *testing.T) {
	fsys := fstest.MapFS{}
	if _, err := Load(fsys, "core"); err == nil {
		t.Fatal("expected error for missing dir")
	}
}
