package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRecallProfiles(t *testing.T) {
	const entry = `{"provider":"ollama","model":"embed:latest","dimensions":1536,"min_similarity":0.62}`
	for _, tc := range []struct {
		name, content, wantError string
		wantCount                int
	}{
		{name: "empty list disables", content: `{"profiles":[]}`},
		{name: "valid", content: `{"profiles":[` + entry + `]}`, wantCount: 1},
		{name: "null list", content: `{"profiles":null}`, wantError: "profiles array is required"},
		{name: "missing list", content: `{}`, wantError: "profiles array is required"},
		{name: "unknown field", content: `{"profiles":[` + strings.Replace(entry, "min_similarity", "min_score", 1) + `]}`, wantError: "unknown field"},
		{name: "trailing value", content: `{"profiles":[]} {}`, wantError: "trailing data"},
		{name: "invalid identity", content: `{"profiles":[` + strings.Replace(entry, "1536", "0", 1) + `]}`, wantError: "provider, model and positive dimensions"},
		{name: "omitted threshold", content: `{"profiles":[` + strings.Replace(entry, `,"min_similarity":0.62`, "", 1) + `]}`, wantError: "min_similarity"},
		{name: "threshold out of range", content: `{"profiles":[` + strings.Replace(entry, "0.62", "1.01", 1) + `]}`, wantError: "min_similarity"},
		{name: "duplicate latest alias", content: `{"profiles":[` + entry + "," + strings.Replace(entry, "embed:latest", "embed", 1) + `]}`, wantError: "duplicate profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "memory-recall.json")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			profiles, err := LoadRecallProfiles(path)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error containing %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil || len(profiles) != tc.wantCount {
				t.Fatalf("want %d profiles, got %v (err %v)", tc.wantCount, profiles, err)
			}
		})
	}
	if profiles, err := LoadRecallProfiles(""); err != nil || len(profiles) != 0 {
		t.Fatalf("omitted path must disable the gate: %v, %v", profiles, err)
	}
	if _, err := LoadRecallProfiles(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("explicit missing file must fail")
	}
}
