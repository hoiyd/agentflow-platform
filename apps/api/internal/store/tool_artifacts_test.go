package store

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestToolArtifactMigrationAndSchemaRequirements(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS tool_artifacts", "content bytea NOT NULL", "tool_artifacts_run_created_idx", "tool_artifacts_call_idx"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("postgres migration missing %q", required)
		}
	}
	foundContent := false
	for _, requirement := range postgresRequiredColumns {
		if requirement.Table == "tool_artifacts" && requirement.Column == "content" && requirement.UDTName == "bytea" && requirement.NotNull {
			foundContent = true
		}
	}
	if !foundContent {
		t.Fatal("postgres schema validation does not cover tool artifact content")
	}
}

func TestToolArtifactValidationAndBounds(t *testing.T) {
	content := []byte(`{"result":"needle one needle two"}`)
	now := time.Now().UTC()
	valid := domain.ToolArtifact{
		ID: "tool_artifact_validation", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: "run-1", ToolCallID: "call-1", ToolName: "reader", MediaType: "application/json",
		ContentHash: ToolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: now,
	}

	invalidArtifacts := []domain.ToolArtifact{
		{},
		func() domain.ToolArtifact { item := valid; item.SchemaVersion++; return item }(),
		func() domain.ToolArtifact { item := valid; item.MediaType = "application/octet-stream"; return item }(),
		func() domain.ToolArtifact { item := valid; item.StoredByteSize++; return item }(),
		func() domain.ToolArtifact { item := valid; item.ContentHash = "sha256:wrong"; return item }(),
		func() domain.ToolArtifact { item := valid; item.CreatedAt = time.Time{}; return item }(),
	}
	for index, artifact := range invalidArtifacts {
		if err := ValidateToolArtifact(artifact, content); err == nil {
			t.Fatalf("invalid artifact %d passed validation", index)
		}
	}

	for _, test := range []struct {
		name          string
		offset, limit int
		wantError     bool
	}{
		{name: "defaults", limit: 0},
		{name: "negative offset", offset: -1, limit: 1, wantError: true},
		{name: "oversized limit", limit: MaxToolArtifactReadBytes + 1, wantError: true},
	} {
		t.Run("read_"+test.name, func(t *testing.T) {
			_, normalized, err := NormalizeArtifactRead(test.offset, test.limit)
			if (err != nil) != test.wantError {
				t.Fatalf("normalize read error = %v", err)
			}
			if test.name == "defaults" && normalized != 8*1024 {
				t.Fatalf("default read limit = %d", normalized)
			}
		})
	}

	for _, test := range []struct {
		name      string
		query     string
		matches   int
		wantError bool
	}{
		{name: "defaults", query: " needle "},
		{name: "empty", query: " ", wantError: true},
		{name: "long query", query: strings.Repeat("x", MaxToolArtifactSearchQuery+1), wantError: true},
		{name: "too many matches", query: "needle", matches: MaxToolArtifactMatches + 1, wantError: true},
	} {
		t.Run("search_"+test.name, func(t *testing.T) {
			query, matches, err := NormalizeArtifactSearch(test.query, test.matches)
			if (err != nil) != test.wantError {
				t.Fatalf("normalize search error = %v", err)
			}
			if test.name == "defaults" && (query != "needle" || matches != 5) {
				t.Fatalf("normalized search = %q, %d", query, matches)
			}
		})
	}

	search := SearchToolArtifact(valid, content, "needle", 1)
	if len(search.Matches) != 1 || !search.Truncated || search.Matches[0].Offset <= 0 {
		t.Fatalf("bounded search did not report truncation: %#v", search)
	}
	expiredAt := now.Add(-time.Minute)
	expired := valid
	expired.ID = "tool_artifact_expired_metadata"
	expired.ExpiresAt = &expiredAt
	items := ToolArtifactsForRun([]domain.ToolArtifact{expired, valid, {ID: "other", RunID: "other-run"}}, valid.RunID)
	if len(items) != 2 || !items[0].Expired || items[1].Expired {
		t.Fatalf("run artifact metadata was not filtered and marked: %#v", items)
	}
}
