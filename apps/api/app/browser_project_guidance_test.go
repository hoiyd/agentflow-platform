package app

import (
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
)

func browserProjectGuidanceConfig(t *testing.T, cfg *config.Config, root string) {
	t.Helper()
	storage, err := store.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "project", "GUIDE.md"), []byte("BROWSER_PROJECT_GUIDANCE: report only evidence from the supplied material."), 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "project", "reports")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "input.md"), []byte("Synthetic nested task material."), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.ProjectGuidancePath = filepath.Join(root, "project-guidance.json")
	writeHeadlessJSON(t, cfg.ProjectGuidancePath, map[string]any{"projects": []any{map[string]any{"workspace_id": workspace, "root": "project", "entry": "GUIDE.md"}}})
}
