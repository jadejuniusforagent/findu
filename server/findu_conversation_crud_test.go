package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveConversationWorkdirCleansSessionAndPlantask(t *testing.T) {
	dataDir := t.TempDir()
	skillDir := t.TempDir()
	s := &Server{m: &Manager{dir: dataDir}, skillDir: skillDir}
	id := "canary-conv-123"
	sessionDir := filepath.Join(dataDir, "sessions", "conv-"+id)
	planDir := filepath.Join(skillDir, ".eino", "plantask", id)
	for _, dir := range []string{sessionDir, planDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	s.removeConversationWorkdir(id)

	for _, dir := range []string{sessionDir, planDir} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after cleanup: %v", dir, err)
		}
	}
}
