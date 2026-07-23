package rag

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type SearchStore interface {
	SearchDocumentChunks(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	SearchDocumentChunksLexical(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
}

type Retriever interface {
	Search(domain.DocumentSearch, int, Embedding) (domain.DocumentSearchResponse, error)
}

type EmbedFunc func(context.Context, string) (Embedding, error)

type Embedding struct {
	Vector     []float64
	Provider   string
	Model      string
	Dimensions int
	Estimated  bool
}

type RetrievalPipeline struct {
	store SearchStore
}

func NewRetrievalPipeline(store SearchStore) *RetrievalPipeline {
	return &RetrievalPipeline{store: store}
}

func EmbedQuery(ctx context.Context, query string, embed EmbedFunc) (Embedding, error) {
	if embed == nil {
		return Embedding{}, errors.New("embedding function is required")
	}
	query = EmbeddingQuery(query)
	if query == "" {
		return Embedding{}, errors.New("query is required")
	}
	return embed(ctx, query)
}

func EmbeddingQuery(query string) string {
	const maxEmbeddingQueryChars = 3000
	query = strings.TrimSpace(query)
	if len(query) <= maxEmbeddingQueryChars {
		return query
	}
	return query[:maxEmbeddingQueryChars]
}

func (p *RetrievalPipeline) Search(search domain.DocumentSearch, requestedLimit int, embedding Embedding) (domain.DocumentSearchResponse, error) {
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return domain.DocumentSearchResponse{}, errors.New("query is required")
	}
	if p == nil || p.store == nil {
		return domain.DocumentSearchResponse{}, errors.New("retrieval store is required")
	}

	requestedLimit = NormalizeSearchLimit(requestedLimit)
	search.Limit = CandidateLimit(requestedLimit)
	search.Embedding = embedding.Vector
	search.EmbeddingProvider = embedding.Provider
	search.EmbeddingModel = embedding.Model
	search.LexicalTerms = QueryTerms(search.Query)
	denseItems, err := p.store.SearchDocumentChunks(search)
	if err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	lexicalItems, err := p.store.SearchDocumentChunksLexical(search)
	if err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	items := mergeRecallCandidates(denseItems, lexicalItems, search.MinSimilarity <= 0)
	items = ReciprocalRankFusion(items)
	items = Rerank(search.Query, items, requestedLimit)
	items = ApplyRelevanceGate(items)

	if embedding.Dimensions <= 0 {
		embedding.Dimensions = len(embedding.Vector)
	}
	response := domain.DocumentSearchResponse{
		Items: items,
		Embedding: domain.EmbeddingInfo{
			Provider:   embedding.Provider,
			Model:      embedding.Model,
			Dimensions: embedding.Dimensions,
			Estimated:  embedding.Estimated,
		},
		NoMatch: len(items) == 0,
	}
	if response.NoMatch {
		response.Reason = "No confident match found. Retrieved candidates did not pass the relevance gate."
	}
	return response, nil
}

func mergeRecallCandidates(denseItems []domain.RetrievedDocumentChunk, lexicalItems []domain.RetrievedDocumentChunk, includeLexicalOnly bool) []domain.RetrievedDocumentChunk {
	items := make([]domain.RetrievedDocumentChunk, 0, len(denseItems)+len(lexicalItems))
	chunkIndexes := make(map[string]int, len(denseItems)+len(lexicalItems))
	for index, item := range denseItems {
		item.VectorRank = index + 1
		chunkIndexes[item.Chunk.ID] = len(items)
		items = append(items, item)
	}
	for index, item := range lexicalItems {
		item.LexicalRank = index + 1
		if item.LexicalScore <= 0 {
			item.LexicalScore = item.Score - item.RecencyBoost
		}
		if existingIndex, ok := chunkIndexes[item.Chunk.ID]; ok {
			items[existingIndex].LexicalRank = item.LexicalRank
			items[existingIndex].LexicalScore = item.LexicalScore
			continue
		}
		if !includeLexicalOnly {
			continue
		}
		chunkIndexes[item.Chunk.ID] = len(items)
		items = append(items, item)
	}
	return items
}
