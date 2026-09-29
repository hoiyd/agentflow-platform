package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveVercelInstallationCompatibility(t *testing.T) {
	project := os.Getenv("TEST_VERCEL_SKILL_PROJECT")
	if project == "" {
		t.Skip("opt-in: install reviewed packages first and set TEST_VERCEL_SKILL_PROJECT")
	}
	catalog, err := LoadRoots([]string{filepath.Join(project, ".agents", "skills")})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := catalog.Freeze([]string{"article-writing", "brand-voice"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFrozen(frozen); err != nil {
		t.Fatal(err)
	}
	for _, item := range frozen {
		t.Logf("vercel_skill_evidence name=%s package_hash=%s resources=%d trust_changed=false", item.Name, item.Hash, len(item.Resources))
	}
}
