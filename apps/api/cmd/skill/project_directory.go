package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func projectSkillDirectory() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// Root-level `go -C apps/api run ./cmd/skill` changes the child process cwd.
	if filepath.Base(root) == "api" && filepath.Base(filepath.Dir(root)) == "apps" {
		root = filepath.Dir(filepath.Dir(root))
	}
	for _, marker := range []string{"apps/api/go.mod", "apps/web/package.json"} {
		info, err := os.Stat(filepath.Join(root, marker))
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("run the installer from the repository root with go -C apps/api run ./cmd/skill install; current directory does not identify an AgentFlow project")
		}
	}
	return filepath.Join(root, ".agents", "skills"), nil
}
