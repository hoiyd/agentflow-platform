package contextassembly

import (
	"agentflow-platform/apps/api/internal/domain"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestKnowledgeReadHandoffUsesKnowledgeBudgetAndUntrustedBoundary(t *testing.T) {
	for _, smallBudget := range []bool{false, true} {
		config := DefaultConfig()
		if smallBudget {
			config.KnowledgeMaxTokens = 1
		}
		pack, err := Assemble(WithSession(t.Context(), Session{Config: config, LoadKnowledgeReads: func() ([]domain.KnowledgeToolReadResult, error) {
			return []domain.KnowledgeToolReadResult{{Content: "Durable checkpoints retain state.", Source: domain.RAGCitation{SourceID: "S2", ChunkID: "chunk-2", ToolCallID: "call-read"}}}, nil
		}}), Request{Messages: []Message{{Role: "system", Content: "Answer carefully."}, {Role: "user", Content: "Question"}}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range pack.Manifest.Entries {
			if entry.ReferenceID == "call-read" {
				found = true
				if entry.Selected == smallBudget || entry.CitationSourceID != "S2" || entry.Source != SourceKnowledge {
					t.Fatalf("entry=%#v", entry)
				}
			}
		}
		if !found {
			t.Fatal("read evidence absent from Manifest")
		}
		if !smallBudget && !strings.Contains(pack.Messages[len(pack.Messages)-1].Content, "<untrusted_knowledge_read>") {
			t.Fatal("handoff lost trust boundary")
		}
	}
	_, err := Assemble(WithSession(t.Context(), Session{LoadKnowledgeReads: func() ([]domain.KnowledgeToolReadResult, error) { return nil, errors.New("event store unavailable") }}), Request{})
	if err == nil {
		t.Fatal("handoff silently ignored persistence error")
	}
}

func TestKnowledgeToolCitationRequiresIntactSelectedObservation(t *testing.T) {
	for _, clipped := range []bool{false, true} {
		result := domain.KnowledgeToolReadResult{Content: "evidence", Source: domain.RAGCitation{SourceID: "S2", ChunkID: "chunk-2"}}
		config := DefaultConfig()
		if clipped {
			config.ToolResultMaxTokens = 20
			result.Content = strings.Repeat("evidence ", 100)
		}
		body, _ := json.Marshal(map[string]any{"tool": "knowledge_read", "result": result})
		pack, err := Assemble(WithSession(t.Context(), Session{Config: config, CurrentInput: "question"}), Request{Model: "fixture", Messages: []Message{{Role: "user", Source: SourceCurrentInput, Content: "question"}, {Role: "tool", Source: SourceToolResult, ReferenceID: "call-read", ToolCallID: "call-read", Content: string(body)}}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range pack.Manifest.Entries {
			if entry.CitationSourceID == "S2" {
				found = true
			}
		}
		if found == clipped {
			t.Fatalf("clipped=%v cited=%v", clipped, found)
		}
	}
}

func TestKnowledgeToolCitationRejectsFailedOrIncompleteObservation(t *testing.T) {
	for _, body := range []string{
		`not json`, `{"tool":"calculator"}`,
		`{"tool":"knowledge_read","error":{"code":"failed"},"result":{"content":"text","source":{"source_id":"S1"}}}`,
		`{"tool":"knowledge_read","truncated":true,"result":{"content":"text","source":{"source_id":"S1"}}}`,
	} {
		if id := knowledgeToolCitation(Message{Role: "tool", Content: body}, "original"); id != "" {
			t.Fatalf("incomplete observation cited: %s", body)
		}
	}
}
