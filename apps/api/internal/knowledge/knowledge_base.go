package knowledge

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
)

type Store interface {
	CreateDocument(domain.Document, []domain.DocumentChunk, []domain.DocumentChunkEmbedding) (domain.Document, error)
	SearchDocumentChunks(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
}

type Embedder interface {
	EmbedText(context.Context, string) (openai.Embedding, error)
}

type KnowledgeBase struct {
	store    Store
	embedder Embedder
}

func NewKnowledgeBase(store Store, embedder Embedder) *KnowledgeBase {
	return &KnowledgeBase{store: store, embedder: embedder}
}

// EmbeddingError lets transports distinguish provider availability from
// validation and persistence errors without inspecting error text.
type EmbeddingError struct {
	Err error
}

func (e EmbeddingError) Error() string { return e.Err.Error() }
func (e EmbeddingError) Unwrap() error { return e.Err }

func IsEmbeddingError(err error) bool {
	var target EmbeddingError
	return errors.As(err, &target)
}

func (s *KnowledgeBase) Ingest(ctx context.Context, request domain.DocumentIngestRequest) (domain.Document, error) {
	document, chunks, err := rag.BuildDocument(request)
	if err != nil {
		return domain.Document{}, err
	}
	return s.persist(ctx, document, chunks)
}

func (s *KnowledgeBase) persist(ctx context.Context, document domain.Document, chunks []domain.DocumentChunk) (domain.Document, error) {
	embeddings := make([]domain.DocumentChunkEmbedding, 0, len(chunks))
	for _, chunk := range chunks {
		embedding, err := s.embedder.EmbedText(ctx, chunk.Content)
		if err != nil {
			return domain.Document{}, EmbeddingError{Err: err}
		}
		embeddings = append(embeddings, domain.DocumentChunkEmbedding{
			Provider:   embedding.Provider,
			Model:      embedding.Model,
			Dimensions: len(embedding.Vector),
			Embedding:  embedding.Vector,
		})
	}
	return s.store.CreateDocument(document, chunks, embeddings)
}

func (s *KnowledgeBase) Search(ctx context.Context, search domain.DocumentSearch, requestedLimit int) (domain.DocumentSearchResponse, error) {
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return domain.DocumentSearchResponse{}, errors.New("query is required")
	}

	embedding, err := s.embedder.EmbedText(ctx, search.Query)
	if err != nil {
		return domain.DocumentSearchResponse{}, EmbeddingError{Err: err}
	}
	if requestedLimit <= 0 {
		requestedLimit = rag.NormalizeSearchLimit(search.Limit)
	}
	search.Limit = rag.CandidateLimit(requestedLimit)
	search.Embedding = embedding.Vector
	search.EmbeddingProvider = embedding.Provider
	search.EmbeddingModel = embedding.Model
	items, err := s.store.SearchDocumentChunks(search)
	if err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	for index := range items {
		items[index].VectorRank = index + 1
	}
	items = rag.Rerank(search.Query, items, requestedLimit)
	items = rag.ApplyRelevanceGate(items)

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
		response.Reason = "No confident match found. Top vector candidates did not pass the relevance gate."
	}
	return response, nil
}

func (s *KnowledgeBase) Evaluate(ctx context.Context, request domain.RAGEvaluationRunRequest) (domain.RAGEvaluationRunResponse, error) {
	if len(request.Cases) == 0 {
		return domain.RAGEvaluationRunResponse{}, errors.New("at least one evaluation case is required")
	}
	if len(request.Cases) > 50 {
		return domain.RAGEvaluationRunResponse{}, errors.New("at most 50 evaluation cases are supported")
	}

	topK := rag.NormalizeSearchLimit(request.TopK)
	results := make([]domain.RAGEvaluationCaseResult, 0, len(request.Cases))
	summary := domain.RAGEvaluationSummary{Total: len(request.Cases)}
	var embedding domain.EmbeddingInfo
	for _, evaluationCase := range request.Cases {
		response, err := s.Search(ctx, domain.DocumentSearch{
			Query:         evaluationCase.Query,
			WorkspaceID:   request.WorkspaceID,
			Metadata:      request.Metadata,
			Limit:         topK,
			MinSimilarity: request.MinSimilarity,
		}, topK)
		if err != nil {
			return domain.RAGEvaluationRunResponse{}, err
		}
		if embedding.Provider == "" {
			embedding = response.Embedding
		}
		caseResult := rag.EvaluateCase(evaluationCase, response.Items)
		results = append(results, caseResult)
		if caseResult.HitAt1 {
			summary.HitAt1++
		}
		if caseResult.HitAt3 {
			summary.HitAt3++
		}
		if caseResult.HitAt5 {
			summary.HitAt5++
		}
		if !caseResult.Hit {
			summary.Misses++
		}
	}
	return domain.RAGEvaluationRunResponse{Summary: summary, Cases: results, Embedding: embedding}, nil
}
