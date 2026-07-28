package rag

import (
	"math"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestReciprocalRankFusionCombinesIndependentRankings(t *testing.T) {
	items := []domain.RetrievedDocumentChunk{
		{Chunk: domain.DocumentChunk{ID: "dense-only"}, VectorRank: 1},
		{Chunk: domain.DocumentChunk{ID: "both"}, VectorRank: 3, LexicalRank: 2},
		{Chunk: domain.DocumentChunk{ID: "lexical-only"}, LexicalRank: 1},
	}

	fused := ReciprocalRankFusion(items)
	if len(fused) != 3 {
		t.Fatalf("expected three fused candidates, got %d", len(fused))
	}
	if fused[0].Chunk.ID != "both" || fused[0].FusionRank != 1 {
		t.Fatalf("expected candidate recalled by both routes to lead, got %#v", fused)
	}
	expected := 1.0/float64(rrfRankConstant+3) + 1.0/float64(rrfRankConstant+2)
	if math.Abs(fused[0].RRFScore-expected) > 1e-12 {
		t.Fatalf("expected RRF score %.12f, got %.12f", expected, fused[0].RRFScore)
	}
	if fused[1].Chunk.ID != "dense-only" || fused[2].Chunk.ID != "lexical-only" {
		t.Fatalf("expected deterministic tie ordering by chunk ID, got %#v", fused)
	}
}

func TestRRFInfoDescribesTheActiveFusionConfiguration(t *testing.T) {
	info := RRFInfo()
	if info.Algorithm != "rrf" || info.Version != "rrf-v1" || info.RankConstant != 60 || info.DenseWeight != 1 || info.LexicalWeight != 1 {
		t.Fatalf("unexpected RRF metadata: %#v", info)
	}
}

func TestReciprocalRankFusionIgnoresRawRecallScores(t *testing.T) {
	items := []domain.RetrievedDocumentChunk{
		{Chunk: domain.DocumentChunk{ID: "rank-two"}, VectorRank: 2, Score: 100},
		{Chunk: domain.DocumentChunk{ID: "rank-one"}, VectorRank: 1, Score: 0.01},
	}

	fused := ReciprocalRankFusion(items)
	if fused[0].Chunk.ID != "rank-one" {
		t.Fatalf("expected source ranks, not raw score magnitude, to determine fusion: %#v", fused)
	}
}
