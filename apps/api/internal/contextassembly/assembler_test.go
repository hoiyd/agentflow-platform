package contextassembly

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

func TestAssembleSelectsContextAndPublishesManifestWithoutRawContent(t *testing.T) {
	var published domain.RunEvent
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{
		ConversationID: "conv-1", RunID: "run-1", StageID: "stage-1", TurnID: "turn-1",
	})
	ctx = WithSession(ctx, Session{
		Config: domain.ContextAssemblyConfig{
			AssemblerVersion: AssemblerVersion, ContextWindowTokens: 320, OutputReserveTokens: 16, SafetyMarginTokens: 8,
			HistoryMaxTokens: 30, MemoryMaxTokens: 40, KnowledgeMaxTokens: 50,
		},
		Sink: eventpkg.SinkFunc(func(_ context.Context, item domain.RunEvent) error {
			published = item
			return nil
		}),
		Memories: []domain.RetrievedMemory{{
			Memory: domain.Memory{ID: "mem-1", Kind: "preference", Content: "Use concise release notes."}, Score: 0.9,
		}},
		Knowledge: []domain.RetrievedDocumentChunk{{
			Document: domain.Document{Title: "Deploy Guide"},
			Chunk:    domain.DocumentChunk{ID: "chunk-1", Content: "Run the smoke tests before deploy."}, Score: 0.8, SourceID: "S1",
		}},
	})

	request := Request{
		Model: "test-model",
		Messages: []Message{
			{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "You are a release assistant."},
			{Source: SourceHistory, ReferenceID: "old", Role: "user", Content: strings.Repeat("old context ", 40)},
			{Source: SourceHistory, ReferenceID: "recent", Role: "assistant", Content: "The build is green."},
			{Source: SourceCurrentInput, ReferenceID: "current", Role: "user", Content: "Prepare the release note."},
		},
		Tools: []Tool{{Name: "clock", Definition: map[string]any{"type": "function", "function": map[string]any{"name": "clock"}}}},
	}

	pack, err := Assemble(ctx, request)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if pack.Manifest.ID == "" || pack.Manifest.ModelCallID == "" || pack.Manifest.RunID != "run-1" {
		t.Fatalf("unexpected manifest identity: %#v", pack.Manifest)
	}
	if selectedEntry(pack.Manifest, "old") {
		t.Fatal("expected oversized old history to be excluded")
	}
	if !selectedEntry(pack.Manifest, "recent") || !selectedEntry(pack.Manifest, "mem-1") || !selectedEntry(pack.Manifest, "chunk-1") {
		t.Fatalf("expected recent and relevant context to be selected: %#v", pack.Manifest.Entries)
	}
	current := messageContent(pack.Messages, "current")
	if !strings.Contains(current, "<memories>") || !strings.Contains(current, "<untrusted_knowledge_context") {
		t.Fatalf("expected retrieved context in current input, got %q", current)
	}
	if !strings.Contains(current, `source_id="S1"`) || !strings.Contains(pack.Messages[0].Content, "[S1]") {
		t.Fatalf("expected native citation protocol in assembled context: system=%q current=%q", pack.Messages[0].Content, current)
	}
	if !strings.Contains(pack.Messages[0].Content, knowledgeTrustPolicy) {
		t.Fatalf("expected system-level knowledge trust policy, got %q", pack.Messages[0].Content)
	}
	if strings.LastIndex(current, "User request:") < strings.LastIndex(current, "</untrusted_knowledge_context>") {
		t.Fatalf("expected the user request after untrusted knowledge, got %q", current)
	}
	for _, entry := range pack.Manifest.Entries {
		if entry.ReferenceID == "chunk-1" && entry.Transformation != "untrusted_wrapped" {
			t.Fatalf("expected untrusted knowledge transformation in manifest, got %#v", entry)
		}
		if entry.ReferenceID == "chunk-1" && entry.CitationSourceID != "S1" {
			t.Fatalf("expected citation source in manifest, got %#v", entry)
		}
	}
	if strings.Contains(pack.Messages[0].Content, "mem-1") || strings.Contains(pack.Messages[0].Content, "chunk-1") {
		t.Fatal("retrieved context changed the stable system prefix")
	}
	if published.Type != domain.EventContextAssembled || published.TurnID != "turn-1" {
		t.Fatalf("unexpected published event: %#v", published)
	}
	encoded, err := json.Marshal(published.Payload)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	for _, raw := range []string{"Use concise release notes", "Run the smoke tests before deploy"} {
		if strings.Contains(string(encoded), raw) {
			t.Fatalf("manifest leaked raw context %q: %s", raw, encoded)
		}
	}
}

func TestAssembleRejectsRequiredContextOverBudget(t *testing.T) {
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run-1", TurnID: "turn-1"})
	ctx = WithSession(ctx, Session{Config: domain.ContextAssemblyConfig{
		AssemblerVersion: AssemblerVersion, ContextWindowTokens: 32, OutputReserveTokens: 4, SafetyMarginTokens: 4,
		HistoryMaxTokens: 8, MemoryMaxTokens: 8, KnowledgeMaxTokens: 8,
	}})

	_, err := Assemble(ctx, Request{Model: "small-model", Messages: []Message{
		{Source: SourceSystem, Role: "system", Content: strings.Repeat("required ", 30)},
		{Source: SourceCurrentInput, Role: "user", Content: "hello"},
	}})
	if !errors.Is(err, ErrInputBudgetExceeded) {
		t.Fatalf("expected input budget error, got %v", err)
	}
}

func TestPrefixHashIgnoresDynamicRetrieval(t *testing.T) {
	request := Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, Role: "system", Content: "stable system"},
		{Source: SourceCurrentInput, Role: "user", Content: "question"},
	}}
	assemble := func(memory string) domain.ContextManifest {
		ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run", TurnID: "turn"})
		ctx = WithSession(ctx, Session{Config: DefaultConfig(), Memories: []domain.RetrievedMemory{{
			Memory: domain.Memory{ID: "mem", Content: memory}, Score: 1,
		}}})
		pack, err := Assemble(ctx, request)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		return pack.Manifest
	}
	first := assemble("first memory")
	second := assemble("different memory")
	if first.PrefixHash == "" || first.PrefixHash != second.PrefixHash {
		t.Fatalf("dynamic retrieval changed prefix hash: %q != %q", first.PrefixHash, second.PrefixHash)
	}
}

func TestAssembleAddsSessionHistoryWithoutRepeatingCurrentInput(t *testing.T) {
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run", TurnID: "turn"})
	ctx = WithSession(ctx, Session{
		Config: DefaultConfig(), CurrentInput: "current question",
		History: []domain.Message{
			{ID: "user-old", Role: "user", Content: "earlier question"},
			{ID: "assistant-old", Role: "assistant", Content: "earlier answer"},
			{ID: "user-current", Role: "user", Content: "current question"},
		},
	})
	pack, err := Assemble(ctx, Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "system"},
		{Source: SourceCurrentInput, ReferenceID: "current", Role: "user", Content: "wrapped current question"},
	}})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if messageContent(pack.Messages, "user-old") != "earlier question" || messageContent(pack.Messages, "assistant-old") != "earlier answer" {
		t.Fatalf("session history was not assembled: %#v", pack.Messages)
	}
	if messageContent(pack.Messages, "user-current") != "" {
		t.Fatalf("current input was repeated as history: %#v", pack.Messages)
	}
}

func TestAssembleUsesPersistedCompactionAndExcludesCoveredHistory(t *testing.T) {
	compaction := domain.ContextCompaction{
		ID: "cmp-1", Summary: "## Goal\nShip the release safely.",
		SourceMessageIDs: []string{"old-user", "old-assistant"},
	}
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run", TurnID: "turn"})
	ctx = WithSession(ctx, Session{
		Config: DefaultConfig(), CurrentInput: "what next?", Compaction: &compaction,
		History: []domain.Message{
			{ID: "old-user", Role: "user", Content: "old question"},
			{ID: "old-assistant", Role: "assistant", Content: "old answer"},
			{ID: "recent-user", Role: "user", Content: "recent question"},
			{ID: "current", Role: "user", Content: "what next?"},
		},
	})
	pack, err := Assemble(ctx, Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "system"},
		{Source: SourceCurrentInput, ReferenceID: "current-input", Role: "user", Content: "what next?"},
	}})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if messageContent(pack.Messages, "old-user") != "" || messageContent(pack.Messages, "old-assistant") != "" {
		t.Fatalf("covered history remained active: %#v", pack.Messages)
	}
	current := messageContent(pack.Messages, "current-input")
	if !strings.Contains(current, "<conversation_summary") || !strings.Contains(current, "Ship the release safely") {
		t.Fatalf("compaction summary was not injected: %q", current)
	}
	if pack.Manifest.CompactionID != "cmp-1" || !selectedEntry(pack.Manifest, "cmp-1") {
		t.Fatalf("manifest did not reference compaction: %#v", pack.Manifest)
	}
}

func TestAssembleCompactsOversizedRequiredToolResult(t *testing.T) {
	config := DefaultConfig()
	config.ToolResultMaxTokens = 20
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run", TurnID: "turn"})
	ctx = WithSession(ctx, Session{Config: config})
	pack, err := Assemble(ctx, Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "system"},
		{Source: SourceToolResult, ReferenceID: "call-1", Role: "tool", Content: strings.Repeat("verbose tool output ", 200)},
		{Source: SourceCurrentInput, ReferenceID: "current", Role: "user", Content: "continue"},
	}})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	result := messageContent(pack.Messages, "call-1")
	if !strings.Contains(result, "tool result compacted") || EstimateTokens(result) > 20 {
		t.Fatalf("tool result was not compacted to budget: tokens=%d content=%q", EstimateTokens(result), result)
	}
	for _, entry := range pack.Manifest.Entries {
		if entry.ReferenceID == "call-1" && (entry.Transformation != "tool_result_compacted" || entry.OriginalBytes <= entry.IncludedBytes) {
			t.Fatalf("tool compaction metadata missing: %#v", entry)
		}
	}
}

func TestLegacySnapshotKeepsCompactionDisabled(t *testing.T) {
	legacy := NormalizeSnapshotConfig(domain.ContextAssemblyConfig{}, domain.ContextRuntimeSnapshotVersion)
	if legacy.CompactionMode != CompactionModeOff {
		t.Fatalf("legacy snapshot unexpectedly enabled compaction: %#v", legacy)
	}
	current := NormalizeSnapshotConfig(domain.ContextAssemblyConfig{}, domain.CurrentRuntimeSnapshotVersion)
	if current.CompactionMode != CompactionModeAuto || !current.HistoryRetrievalEnabled {
		t.Fatalf("current snapshot did not receive compaction defaults: %#v", current)
	}
	v3 := NormalizeSnapshotConfig(domain.ContextAssemblyConfig{}, domain.CompactionRuntimeSnapshotVersion)
	if v3.CompactionMode != CompactionModeAuto || v3.HistoryRetrievalEnabled {
		t.Fatalf("v3 snapshot lost its frozen compaction behavior: %#v", v3)
	}
}

func TestAssembleInjectsRetrievedHistoryAndRecordsStableReferences(t *testing.T) {
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run", TurnID: "turn"})
	ctx = WithSession(ctx, Session{Config: DefaultConfig(), HistorySearch: []domain.RetrievedSessionHistory{
		{Reference: "message:msg-old", SourceKind: domain.SessionHistorySourceMessage, MessageID: "msg-old", Role: "user", Content: "Exact deployment ID is release-2026-08.", OriginalBytes: 44, MatchReason: "keyword_match"},
		{Reference: "event:event-old", SourceKind: domain.SessionHistorySourceEvent, EventID: "event-old", EventType: domain.EventToolFailed, Content: `{"error":"connection refused"}`, OriginalBytes: 120, MatchReason: "event_type_match", Truncated: true},
	}})
	pack, err := Assemble(ctx, Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "system"},
		{Source: SourceCurrentInput, ReferenceID: "current", Role: "user", Content: "recover exact details"},
	}})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	current := messageContent(pack.Messages, "current")
	if !strings.Contains(current, "release-2026-08") || !strings.Contains(current, "read-only evidence") {
		t.Fatalf("retrieved history was not safely injected: %q", current)
	}
	want := map[string]string{"message:msg-old": "retrieved_original", "event:event-old": "retrieved_truncated"}
	for _, entry := range pack.Manifest.Entries {
		transformation, ok := want[entry.ReferenceID]
		if !ok {
			continue
		}
		if entry.Source != SourceHistorySearch || !entry.Selected || entry.Reason == "" || entry.Transformation != transformation || entry.EstimatedTokens <= 0 {
			t.Fatalf("unexpected history manifest entry: %#v", entry)
		}
		delete(want, entry.ReferenceID)
	}
	if len(want) != 0 {
		t.Fatalf("missing source references in manifest: %#v", want)
	}
	encoded, _ := json.Marshal(pack.Manifest)
	if strings.Contains(string(encoded), "release-2026-08") || strings.Contains(string(encoded), "connection refused") {
		t.Fatalf("manifest persisted retrieved raw content: %s", encoded)
	}
}

func TestAssemblePrefersRetrievedOriginalOverDuplicateRecentHistory(t *testing.T) {
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run", TurnID: "turn"})
	ctx = WithSession(ctx, Session{
		Config:       DefaultConfig(),
		History:      []domain.Message{{ID: "msg-old", Role: "user", Content: "Exact command: deploy --release 42"}},
		CurrentInput: "recover release 42",
		HistorySearch: []domain.RetrievedSessionHistory{{
			Reference: "message:msg-old", SourceKind: domain.SessionHistorySourceMessage,
			MessageID: "msg-old", Role: "user", Content: "Exact command: deploy --release 42",
			OriginalBytes: 34, MatchReason: "keyword_match",
		}},
	})
	pack, err := Assemble(ctx, Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "system"},
		{Source: SourceCurrentInput, ReferenceID: "current", Role: "user", Content: "recover release 42"},
	}})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	count := 0
	for _, message := range pack.Messages {
		count += strings.Count(message.Content, "deploy --release 42")
	}
	if count != 1 {
		t.Fatalf("expected one exact source injection, got %d in %#v", count, pack.Messages)
	}
	for _, entry := range pack.Manifest.Entries {
		if entry.Source == SourceHistory && entry.ReferenceID == "msg-old" && (entry.Selected || entry.Reason != "superseded_by_history_retrieval") {
			t.Fatalf("duplicate recent history was not suppressed: %#v", entry)
		}
	}
}

func selectedEntry(manifest domain.ContextManifest, referenceID string) bool {
	for _, entry := range manifest.Entries {
		if entry.ReferenceID == referenceID {
			return entry.Selected
		}
	}
	return false
}

func messageContent(messages []Message, referenceID string) string {
	for _, message := range messages {
		if message.ReferenceID == referenceID {
			return message.Content
		}
	}
	return ""
}
