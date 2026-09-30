package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// This fixture mutates the request, not the returned candidate. Output-only
// validation cannot protect upstream state from an aliased request slice.
type mutatingReranker struct{ HeuristicReranker }

func (r mutatingReranker) Rerank(ctx context.Context, request RerankRequest) (RerankResult, error) {
	request.Candidates[0].Document.WorkspaceID = "foreign"
	request.Candidates[0].Document.Metadata["nested"].(map[string]any)["value"] = "changed"
	request.Candidates[0].Chunk.Content = "FORGED AUTH-7F31"
	request.Candidates[0].Document.Metadata["list"].([]any)[0].(map[string]any)["value"] = "changed"
	request.Candidates[0].Chunk.Metadata["bytes"].([]byte)[0] = 'X'
	request.Candidates[0].Chunk.Metadata["strings"].([]string)[0] = "changed"
	return r.HeuristicReranker.Rerank(ctx, request)
}

type mutatingGate struct{ HeuristicRelevanceGate }

func (g mutatingGate) Evaluate(ctx context.Context, request RelevanceGateRequest) (RelevanceGateResult, error) {
	request.Candidates[0].Similarity = 1
	request.Candidates[0].RerankScore = 99
	request.Candidates[0].Chunk.SectionPath[0] = "changed"
	request.Candidates[0].Chunk.Document.Metadata["value"] = "changed"
	return g.HeuristicRelevanceGate.Evaluate(ctx, request)
}

func TestRetrievalStagesCannotRewriteCandidateAuthority(t *testing.T) {
	store := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{{
		Document: domain.Document{ID: "doc-1", WorkspaceID: "workspace-1", Metadata: map[string]any{
			"nested": map[string]any{"value": "original"}, "list": []any{map[string]any{"value": "original"}},
		}},
		Chunk: domain.DocumentChunk{ID: "chunk-1", Content: "AUTH-7F31 original runbook",
			ChunkSource: domain.ChunkSource{SectionPath: []string{"original"}},
			Document:    domain.Document{Metadata: map[string]any{"value": "original"}},
			Metadata:    map[string]any{"bytes": []byte("original"), "strings": []string{"original"}},
		}, Similarity: 0.9,
	}}}
	pipeline := NewRetrievalPipelineWithStages(store, mutatingReranker{}, mutatingGate{})
	response, err := pipeline.Search(context.Background(), domain.DocumentSearch{Query: "AUTH-7F31", Limit: 1}, 1, Embedding{Vector: []float64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("unexpected response: %#v", response)
	}
	item := response.Items[0]
	if item.Document.WorkspaceID != "workspace-1" || item.Chunk.Content != "AUTH-7F31 original runbook" || item.Document.Metadata["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("stage rewrote authoritative candidate: %#v", item)
	}
	if item.Chunk.SectionPath[0] != "original" || item.Chunk.Document.Metadata["value"] != "original" ||
		string(item.Chunk.Metadata["bytes"].([]byte)) != "original" || item.Chunk.Metadata["strings"].([]string)[0] != "original" ||
		item.Document.Metadata["list"].([]any)[0].(map[string]any)["value"] != "original" {
		t.Fatalf("stage mutated candidate resources: %#v", item)
	}
	if response.RelevanceDecisions[0].Similarity != 0.9 || response.RelevanceDecisions[0].RerankScore != item.RerankScore || item.RerankScore == 99 {
		t.Fatalf("Gate forged ranking/recall audit signals: %#v", response.RelevanceDecisions)
	}
}

type rerankFunction func(context.Context, RerankRequest) (RerankResult, error)

func (f rerankFunction) Rerank(ctx context.Context, request RerankRequest) (RerankResult, error) {
	return f(ctx, request)
}

type gateFunction func(context.Context, RelevanceGateRequest) (RelevanceGateResult, error)

func (f gateFunction) Evaluate(ctx context.Context, request RelevanceGateRequest) (RelevanceGateResult, error) {
	return f(ctx, request)
}

func TestRetrievalRejectsInvalidStageDecisionsBeforeContextSelection(t *testing.T) {
	for _, name := range []string{"unknown ranking identity", "duplicate ranking identity", "unknown gate identity", "duplicate gate identity", "reordered gate", "gate unavailable"} {
		t.Run(name, func(t *testing.T) {
			storage := &retrievalStoreStub{denseItems: []domain.RetrievedDocumentChunk{
				{Document: domain.Document{ID: "d1"}, Chunk: domain.DocumentChunk{ID: "c1", Content: "AUTH-7F31 runbook"}, Similarity: 0.9},
				{Document: domain.Document{ID: "d2"}, Chunk: domain.DocumentChunk{ID: "c2", Content: "AUTH-7F31 recovery"}, Similarity: 0.8},
			}}
			reranker := rerankFunction(func(ctx context.Context, request RerankRequest) (RerankResult, error) {
				result, err := NewHeuristicReranker(DefaultHeuristicRerankerConfig()).Rerank(ctx, request)
				if name == "unknown ranking identity" {
					result.Decisions[0].DocumentID = "foreign"
				}
				if name == "duplicate ranking identity" {
					result.Decisions[1].ChunkID = result.Decisions[0].ChunkID
					result.Decisions[1].DocumentID = result.Decisions[0].DocumentID
				}
				return result, err
			})
			gate := gateFunction(func(ctx context.Context, request RelevanceGateRequest) (RelevanceGateResult, error) {
				if name == "gate unavailable" {
					return RelevanceGateResult{}, errors.New("gate unavailable")
				}
				result, err := NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig()).Evaluate(ctx, request)
				if name == "unknown gate identity" {
					result.Decisions[0].DocumentID = "foreign"
				}
				if name == "duplicate gate identity" {
					result.Decisions[1] = result.Decisions[0]
				}
				if name == "reordered gate" {
					result.Decisions[0], result.Decisions[1] = result.Decisions[1], result.Decisions[0]
				}
				return result, err
			})
			response, err := NewRetrievalPipelineWithStages(storage, reranker, gate).Search(context.Background(), domain.DocumentSearch{Query: "AUTH-7F31"}, 2, Embedding{Vector: []float64{1}})
			if err == nil || (!strings.Contains(err.Error(), "reranker") && !strings.Contains(err.Error(), "gate")) {
				t.Fatalf("invalid stage result accepted: response=%#v err=%v", response, err)
			}
			if len(storage.contextSearch) != 0 || len(response.Items) != 0 || len(response.CitationSources) != 0 {
				t.Fatalf("invalid decisions reached context selection: %#v", response)
			}
		})
	}
}
