package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type SearchStore interface {
	SearchDocumentChunks(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	SearchDocumentChunksLexical(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	ListDocumentContextChunks(domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error)
}

type Retriever interface {
	Search(context.Context, domain.DocumentSearch, int, Embedding) (domain.DocumentSearchResponse, error)
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
	store              SearchStore
	reranker           Reranker
	relevanceGate      RelevanceGate
	requireWorkspaceID bool
}

type RetrievalOptions struct {
	RequireWorkspaceID bool
}

func NewRetrievalPipeline(store SearchStore) *RetrievalPipeline {
	return NewRetrievalPipelineWithOptions(store, RetrievalOptions{})
}

func NewRetrievalPipelineWithOptions(store SearchStore, options RetrievalOptions) *RetrievalPipeline {
	return newRetrievalPipeline(store, nil, nil, options)
}

func NewRetrievalPipelineWithReranker(store SearchStore, reranker Reranker) *RetrievalPipeline {
	return newRetrievalPipeline(store, reranker, nil, RetrievalOptions{})
}

func NewRetrievalPipelineWithStages(store SearchStore, reranker Reranker, relevanceGate RelevanceGate) *RetrievalPipeline {
	return newRetrievalPipeline(store, reranker, relevanceGate, RetrievalOptions{})
}

func newRetrievalPipeline(store SearchStore, reranker Reranker, relevanceGate RelevanceGate, options RetrievalOptions) *RetrievalPipeline {
	if reranker == nil {
		reranker = NewHeuristicReranker(DefaultHeuristicRerankerConfig())
	}
	if relevanceGate == nil {
		relevanceGate = NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig())
	}
	return &RetrievalPipeline{store: store, reranker: reranker, relevanceGate: relevanceGate, requireWorkspaceID: options.RequireWorkspaceID}
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

func (p *RetrievalPipeline) Search(ctx context.Context, search domain.DocumentSearch, requestedLimit int, embedding Embedding) (domain.DocumentSearchResponse, error) {
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return domain.DocumentSearchResponse{}, errors.New("query is required")
	}
	if p == nil || p.store == nil {
		return domain.DocumentSearchResponse{}, errors.New("retrieval store is required")
	}
	search.WorkspaceID = strings.TrimSpace(search.WorkspaceID)
	if p.requireWorkspaceID && search.WorkspaceID == "" {
		return domain.DocumentSearchResponse{}, errors.New("workspace_id is required for retrieval")
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
	items, security := GuardPromptInjection(items)
	items = ReciprocalRankFusion(items)
	reranker := p.reranker
	if reranker == nil {
		reranker = NewHeuristicReranker(DefaultHeuristicRerankerConfig())
	}
	rerankResult, err := reranker.Rerank(ctx, RerankRequest{Query: search.Query, Candidates: items, Limit: requestedLimit})
	if err != nil {
		return domain.DocumentSearchResponse{}, fmt.Errorf("rerank candidates: %w", err)
	}
	if err := validateRerankResult(RerankRequest{Query: search.Query, Candidates: items, Limit: requestedLimit}, rerankResult); err != nil {
		return domain.DocumentSearchResponse{}, fmt.Errorf("validate reranker output: %w", err)
	}
	items = rerankResult.Items
	relevanceGate := p.relevanceGate
	if relevanceGate == nil {
		relevanceGate = NewHeuristicRelevanceGate(DefaultHeuristicRelevanceGateConfig())
	}
	gateResult, err := relevanceGate.Evaluate(ctx, RelevanceGateRequest{Query: search.Query, Candidates: items, Reranker: rerankResult.Info})
	if err != nil {
		return domain.DocumentSearchResponse{}, fmt.Errorf("apply relevance gate: %w", err)
	}
	if err := validateRelevanceGateResult(items, gateResult); err != nil {
		return domain.DocumentSearchResponse{}, fmt.Errorf("validate relevance gate output: %w", err)
	}
	items = gateResult.Items
	contextItems, contextSelection, contextSecurity, err := NewContextSelector(p.store).Select(search, items)
	if err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	security = mergeKnowledgeSecurity(security, contextSecurity)
	contextItems, citationSources := AssignCitationSources(contextItems)

	if embedding.Dimensions <= 0 {
		embedding.Dimensions = len(embedding.Vector)
	}
	response := domain.DocumentSearchResponse{
		Items:            items,
		ContextItems:     contextItems,
		CitationSources:  citationSources,
		ContextSelection: contextSelection,
		Fusion:           RRFInfo(),
		Security:         security,
		Embedding: domain.EmbeddingInfo{
			Provider:   embedding.Provider,
			Model:      embedding.Model,
			Dimensions: embedding.Dimensions,
			Estimated:  embedding.Estimated,
		},
		Reranker:      rerankResult.Info,
		RelevanceGate: gateResult.Info,
		NoMatch:       len(items) == 0,
	}
	if response.NoMatch {
		if security.BlockedCandidates > 0 && security.BlockedCandidates == security.CheckedCandidates {
			response.Reason = "No safe match found. Retrieved candidates were blocked by the knowledge security policy."
		} else {
			response.Reason = "No confident match found. Retrieved candidates did not pass the relevance gate."
		}
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
