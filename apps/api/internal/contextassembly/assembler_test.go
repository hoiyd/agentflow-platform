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
			AssemblerVersion: AssemblerVersion, ContextWindowTokens: 256, OutputReserveTokens: 16, SafetyMarginTokens: 8,
			HistoryMaxTokens: 30, MemoryMaxTokens: 40, KnowledgeMaxTokens: 40,
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
			Chunk:    domain.DocumentChunk{ID: "chunk-1", Content: "Run the smoke tests before deploy."}, Score: 0.8,
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
	if !strings.Contains(current, "<memories>") || !strings.Contains(current, "<knowledge>") {
		t.Fatalf("expected retrieved context in current input, got %q", current)
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
