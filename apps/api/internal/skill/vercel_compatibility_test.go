package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// This opt-in smoke reads Vercel-installed files; it does not run or compare installers.
func TestLiveVercelInstallationCompatibility(t *testing.T) {
	project := os.Getenv("TEST_VERCEL_SKILL_PROJECT")
	if project == "" {
		t.Skip("opt-in: install reviewed packages first and set TEST_VERCEL_SKILL_PROJECT")
	}
	catalog, err := LoadRoots([]string{filepath.Join(project, ".agents", "skills")})
	if err != nil {
		t.Fatal(err)
	}
	metadata := catalog.List()
	if len(metadata) == 0 {
		t.Fatal("no Skills discovered; install reviewed packages in the selected project's .agents/skills directory first")
	}
	for _, entry := range metadata {
		// Dependency names satisfy only this snapshot-format check, not real Tool authorization.
		frozen, err := catalog.Freeze([]string{entry.Name}, entry.RequiredTools)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateFrozen(frozen); err != nil {
			t.Fatal(err)
		}
		item := frozen[0]
		t.Logf("vercel_skill_evidence name=%s package_hash=%s resources=%d trust_changed=false", item.Name, item.Hash, len(item.Resources))
	}
}
