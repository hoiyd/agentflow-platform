package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileToolArtifactRoundTripReadSearchReplayAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentflow.json")
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("artifact test")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"rows":"` + strings.Repeat("recoverable-data-", 7000) + `needle"}`)
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	artifact := domain.ToolArtifact{
		ID: "tool_artifact_roundtrip", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, StageID: "stage-1", TurnID: "turn-1", ToolCallID: "call-1", ToolName: "future_tool",
		MediaType: "application/json", ContentHash: toolArtifactContentHash(content),
		OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: now, ExpiresAt: &expires,
	}
	if _, err := fileStore.CreateToolArtifact(artifact, content); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	info, err := os.Stat(path + ".tool-artifacts")
	if err != nil {
		t.Fatalf("stat artifact directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("artifact directory permissions = %v", info.Mode().Perm())
	}
	contentInfo, err := os.Stat(filepath.Join(path+".tool-artifacts", artifact.ID+".bin"))
	if err != nil {
		t.Fatalf("stat artifact content: %v", err)
	}
	if contentInfo.Mode().Perm() != 0o600 {
		t.Fatalf("artifact content permissions = %v", contentInfo.Mode().Perm())
	}

	var recovered strings.Builder
	for offset := 0; ; {
		part, err := fileStore.ReadToolArtifact(run.ID, artifact.ID, offset, 4096)
		if err != nil {
			t.Fatalf("read artifact: %v", err)
		}
		recovered.WriteString(part.Content)
		if part.Complete {
			break
		}
		offset = part.NextOffset
	}
	if recovered.String() != string(content) {
		t.Fatal("bounded reads did not reconstruct exact artifact content")
	}
	search, err := fileStore.SearchToolArtifact(run.ID, artifact.ID, "needle", 5)
	if err != nil || len(search.Matches) != 1 || search.ScannedBytes != len(content) {
		t.Fatalf("search artifact: result=%#v err=%v", search, err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	replay, ok, err := reopened.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.ToolArtifacts) != 1 || replay.ToolArtifacts[0].ID != artifact.ID {
		t.Fatalf("replay lost artifact metadata: replay=%#v ok=%v err=%v", replay.ToolArtifacts, ok, err)
	}

	expired := artifact
	expired.ID = "tool_artifact_expired"
	expired.ToolCallID = "call-2"
	past := now.Add(-time.Minute)
	expired.ExpiresAt = &past
	if _, err := reopened.CreateToolArtifact(expired, content); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ReadToolArtifact(run.ID, expired.ID, 0, 10); !errors.Is(err, ErrToolArtifactExpired) {
		t.Fatalf("expired artifact read error = %v", err)
	}
	cleaned, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path+".tool-artifacts", expired.ID+".bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired artifact content was not purged: %v", err)
	}
	items, err := cleaned.ListToolArtifacts(run.ID)
	if err != nil || len(items) != 2 || !items[1].Expired {
		t.Fatalf("cleanup must preserve expired metadata: items=%#v err=%v", items, err)
	}
}

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
		ContentHash: toolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: now,
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
		if err := validateToolArtifact(artifact, content); err == nil {
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
			_, normalized, err := normalizeArtifactRead(test.offset, test.limit)
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
			query, matches, err := normalizeArtifactSearch(test.query, test.matches)
			if (err != nil) != test.wantError {
				t.Fatalf("normalize search error = %v", err)
			}
			if test.name == "defaults" && (query != "needle" || matches != 5) {
				t.Fatalf("normalized search = %q, %d", query, matches)
			}
		})
	}

	search := searchToolArtifact(valid, content, "needle", 1)
	if len(search.Matches) != 1 || !search.Truncated || search.Matches[0].Offset <= 0 {
		t.Fatalf("bounded search did not report truncation: %#v", search)
	}
	expiredAt := now.Add(-time.Minute)
	expired := valid
	expired.ID = "tool_artifact_expired_metadata"
	expired.ExpiresAt = &expiredAt
	items := toolArtifactsForRun([]domain.ToolArtifact{expired, valid, {ID: "other", RunID: "other-run"}}, valid.RunID)
	if len(items) != 2 || !items[0].Expired || items[1].Expired {
		t.Fatalf("run artifact metadata was not filtered and marked: %#v", items)
	}
}

func TestFileToolArtifactFailurePathsAndConversationCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentflow.json")
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("artifact failures")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"result":"needle"}`)
	artifact := domain.ToolArtifact{
		ID: "tool_artifact_failures", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, ToolCallID: "call-1", ToolName: "reader", MediaType: "application/json",
		ContentHash: toolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: time.Now().UTC(),
	}

	invalid := artifact
	invalid.SchemaVersion++
	if _, err := fileStore.CreateToolArtifact(invalid, content); err == nil {
		t.Fatal("invalid artifact schema was accepted")
	}
	missingRun := artifact
	missingRun.ID = "tool_artifact_missing_run"
	missingRun.RunID = "missing"
	if _, err := fileStore.CreateToolArtifact(missingRun, content); !IsNotFound(err) {
		t.Fatalf("missing run error = %v", err)
	}
	invalidID := artifact
	invalidID.ID = "invalid/path"
	if _, err := fileStore.CreateToolArtifact(invalidID, content); err == nil {
		t.Fatal("unsafe artifact path was accepted")
	}
	if _, err := fileStore.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	if existing, err := fileStore.CreateToolArtifact(artifact, content); err != nil || existing.ID != artifact.ID {
		t.Fatalf("idempotent create failed: artifact=%#v err=%v", existing, err)
	}
	conflictingContent := []byte(`{"result":"different"}`)
	conflict := artifact
	conflict.ContentHash = toolArtifactContentHash(conflictingContent)
	conflict.OriginalByteSize = len(conflictingContent)
	conflict.StoredByteSize = len(conflictingContent)
	if _, err := fileStore.CreateToolArtifact(conflict, conflictingContent); err == nil {
		t.Fatal("idempotency conflict was accepted")
	}

	if _, err := fileStore.ListToolArtifacts("missing"); !IsNotFound(err) {
		t.Fatalf("missing run list error = %v", err)
	}
	if _, err := fileStore.ReadToolArtifact(run.ID, artifact.ID, -1, 1); err == nil {
		t.Fatal("negative read offset was accepted")
	}
	if _, err := fileStore.ReadToolArtifact(run.ID, "missing", 0, 1); !IsNotFound(err) {
		t.Fatalf("missing artifact read error = %v", err)
	}
	if _, err := fileStore.ReadToolArtifact(run.ID, artifact.ID, len(content)+1, 1); !errors.Is(err, ErrToolArtifactRange) {
		t.Fatalf("out-of-range read error = %v", err)
	}
	if _, err := fileStore.SearchToolArtifact(run.ID, artifact.ID, "", 1); err == nil {
		t.Fatal("empty artifact search was accepted")
	}
	if _, err := fileStore.SearchToolArtifact(run.ID, "missing", "needle", 1); !IsNotFound(err) {
		t.Fatalf("missing artifact search error = %v", err)
	}

	contentPath := filepath.Join(path+".tool-artifacts", artifact.ID+".bin")
	if err := os.WriteFile(contentPath, []byte(strings.Repeat("x", len(content))), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.SearchToolArtifact(run.ID, artifact.ID, "needle", 1); err == nil {
		t.Fatal("corrupt artifact content was accepted")
	}
	if _, err := fileStore.CreateToolArtifact(artifact, content); err == nil {
		t.Fatal("idempotent create accepted corrupt existing content")
	}
	if err := os.WriteFile(contentPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fileStore.DeleteConversation(conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(contentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conversation deletion retained artifact content: %v", err)
	}
}
