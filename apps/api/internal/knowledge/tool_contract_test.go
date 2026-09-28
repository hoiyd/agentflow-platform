package knowledge

import (
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/tooltest"
)

func TestKnowledgeBindingsSatisfyContractHarness(t *testing.T) {
	bindings := NewKnowledgeBase(nil, nil).ToolBindings(fixturestore.New())
	tooltest.RunBindingContract(t, tooltest.BindingContract{
		Binding: bindings[0], ValidArguments: json.RawMessage(`{"query":"checkpoint","max_results":3}`),
		GoodResult: domain.KnowledgeToolSearchResult{Query: "checkpoint", Items: []domain.KnowledgeMatch{{Reference: "chunk-1", DocumentID: "doc-1"}}, TrustBoundary: knowledgeTrustBoundary},
		InvalidCalls: []tooltest.InvalidCall{
			{Name: "missing query", Arguments: json.RawMessage(`{}`), WantArgumentCode: "required"},
			{Name: "empty query", Arguments: json.RawMessage(`{"query":""}`), WantArgumentCode: "min_length"},
			{Name: "too many matches", Arguments: json.RawMessage(`{"query":"checkpoint","max_results":6}`), WantArgumentCode: "maximum"},
			{Name: "scope override", Arguments: json.RawMessage(`{"query":"checkpoint","workspace_id":"foreign"}`), WantArgumentCode: "additional_properties"},
		},
		BadResults: []tooltest.BadResult{{Name: "missing reference", Value: domain.KnowledgeToolSearchResult{Query: "checkpoint", Items: []domain.KnowledgeMatch{{}}, TrustBoundary: knowledgeTrustBoundary}}},
		ValidateResult: func(value any) error {
			result, ok := value.(domain.KnowledgeToolSearchResult)
			if !ok || result.Query == "" || result.TrustBoundary == "" {
				return errors.New("search result requires query and trust boundary")
			}
			for _, item := range result.Items {
				if item.Reference == "" || item.DocumentID == "" {
					return errors.New("search result requires scoped references")
				}
			}
			return nil
		},
	})
	tooltest.RunBindingContract(t, tooltest.BindingContract{
		Binding: bindings[1], ValidArguments: json.RawMessage(`{"reference":"chunk-1","offset":0,"limit":128}`),
		GoodResult: domain.KnowledgeToolReadResult{Reference: "chunk-1", Content: "facts", NextOffset: 5, Source: domain.RAGCitation{SourceID: "S1", ChunkID: "chunk-1"}, TrustBoundary: knowledgeTrustBoundary},
		InvalidCalls: []tooltest.InvalidCall{
			{Name: "missing reference", Arguments: json.RawMessage(`{}`), WantArgumentCode: "required"},
			{Name: "negative offset", Arguments: json.RawMessage(`{"reference":"chunk-1","offset":-1}`), WantArgumentCode: "minimum"},
			{Name: "zero limit", Arguments: json.RawMessage(`{"reference":"chunk-1","limit":0}`), WantArgumentCode: "minimum"},
			{Name: "oversized page", Arguments: json.RawMessage(`{"reference":"chunk-1","limit":4097}`), WantArgumentCode: "maximum"},
			{Name: "path override", Arguments: json.RawMessage(`{"reference":"chunk-1","path":"/etc/passwd"}`), WantArgumentCode: "additional_properties"},
		},
		BadResults: []tooltest.BadResult{{Name: "missing source", Value: domain.KnowledgeToolReadResult{Reference: "chunk-1", Content: "facts", NextOffset: 5, TrustBoundary: knowledgeTrustBoundary}}},
		ValidateResult: func(value any) error {
			result, ok := value.(domain.KnowledgeToolReadResult)
			if !ok || result.Reference == "" || result.Source.SourceID == "" || result.NextOffset <= result.Offset || result.TrustBoundary == "" {
				return errors.New("read result requires provenance, continuation and trust boundary")
			}
			return nil
		},
	})
}
