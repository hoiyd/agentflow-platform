package domain

import "testing"

func TestNormalizeWorkspaceID(t *testing.T) {
	for _, input := range []string{"", "  ", "default", " default "} {
		if got := NormalizeWorkspaceID(input); got != DefaultWorkspaceID {
			t.Fatalf("normalize %q: got %q want %q", input, got, DefaultWorkspaceID)
		}
	}
	if got := NormalizeWorkspaceID(" workspace-a "); got != "workspace-a" {
		t.Fatalf("expected custom workspace to be preserved, got %q", got)
	}
}
