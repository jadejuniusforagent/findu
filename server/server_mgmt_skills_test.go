package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFSListSkillsSkipsDotDirectories(t *testing.T) {
	skillDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillDir, "good-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "good-skill", "SKILL.md"), []byte("---\nname: good-skill\ndescription: test\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, ".eino", "plantask", "conv-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: &Manager{dir: t.TempDir()}, skillDir: skillDir}
	rec := httptest.NewRecorder()
	s.fsListSkills(rec, httptest.NewRequest("GET", "/api/skills", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Skills) != 1 || out.Skills[0].Name != "good-skill" {
		t.Fatalf("skills = %+v, want only good-skill", out.Skills)
	}
}
