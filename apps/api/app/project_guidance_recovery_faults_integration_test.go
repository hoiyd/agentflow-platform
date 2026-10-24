package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

// Corrupt only an isolated test Run's JSONB, then exercise the actual Continue
// endpoint. Recovery must reject invalid frozen evidence before calling a model.
func exerciseFrozenGuidanceFaults(t *testing.T, call func(string, any) string, database string, newPaused func() domain.Run, providerCalls func() int) {
	t.Helper()
	db, err := sql.Open("pgx", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	write := func(run domain.Run, snapshot *domain.RuntimeSnapshot) {
		t.Helper()
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), `UPDATE runs SET runtime_snapshot=$1 WHERE id=$2`, data, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*domain.ProjectGuidanceSnapshot)
	}{
		{"identity", func(s *domain.ProjectGuidanceSnapshot) { s.Version = "unknown" }},
		{"paths", func(s *domain.ProjectGuidanceSnapshot) { s.Selection.Paths = nil }},
		{"source-path", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[0].Path = "../GUIDE.md" }},
		{"duplicate-source", func(s *domain.ProjectGuidanceSnapshot) { s.Entries = append(s.Entries, s.Entries[0]) }},
		{"pattern", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[1].Patterns = []string{"reports/["} }},
		{"matches", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[1].MatchedPaths = nil }},
		{"content-hash", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[0].Content += "tampered" }},
		{"unmatched-body", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[0].Reason = "path_not_matched" }},
		{"invalid-duplicate", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[0].Reason = "duplicate_content" }},
		{"selection-reason", func(s *domain.ProjectGuidanceSnapshot) { s.Entries[0].Reason = "global_fallback" }},
		{"body-budget", func(s *domain.ProjectGuidanceSnapshot) {
			e := &s.Entries[0]
			e.Content = strings.Repeat("中", 4100)
			e.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(e.Content)))
			e.OriginalBytes, e.EstimatedTokens = len(e.Content), 4100
		}},
	} {
		t.Run("frozen-"+tc.name, func(t *testing.T) {
			// A rejected continuation becomes a failed Run. Each scenario needs
			// its own real approval boundary, not a reset terminal fixture.
			run := newPaused()
			snapshot := store.CloneRuntimeSnapshot(*run.RuntimeSnapshot)
			tc.change(snapshot.ProjectGuidance)
			write(run, snapshot)
			before := providerCalls()
			response := call("/api/runs/"+run.ID+"/continue", map[string]any{"plan": "guidance-gate: reject corrupt frozen context"})
			if !strings.Contains(response, "project_guidance_") || providerCalls() != before {
				t.Fatalf("corrupt frozen guidance reached execution: %s", response)
			}
		})
	}
}
