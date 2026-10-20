package knowledge

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/store"
)

type Store interface {
	GetDocumentBySource(string, string) (domain.Document, bool, error)
	UpdateDocumentMetadata(string, string, domain.DocumentMetadataUpdate) (domain.Document, error)
	CreateDocument(domain.Document, []domain.DocumentChunk, []domain.DocumentChunkEmbedding) (domain.Document, error)
	ListDocumentIndexIdentities(string) ([]domain.DocumentIndexIdentity, error)
	SearchDocumentChunks(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	SearchDocumentChunksLexical(domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	ListDocumentContextChunks(domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error)
}

type Embedder interface {
	EmbedText(context.Context, string) (provider.Embedding, error)
}

type KnowledgeBase struct {
	workspaceAccess func(context.Context, string) error
	store           Store
	embedder        Embedder
	retriever       rag.Retriever
}

func (s *KnowledgeBase) WithWorkspaceAccess(check func(context.Context, string) error) *KnowledgeBase {
	copy := *s
	copy.workspaceAccess = check
	return &copy
}

func NewKnowledgeBase(store Store, embedder Embedder) *KnowledgeBase {
	return NewKnowledgeBaseWithRetriever(store, embedder, rag.NewRetrievalPipeline(store))
}

func NewKnowledgeBaseWithRetriever(store Store, embedder Embedder, retriever rag.Retriever) *KnowledgeBase {
	if retriever == nil {
		retriever = rag.NewRetrievalPipeline(store)
	}
	return &KnowledgeBase{store: store, embedder: embedder, retriever: retriever}
}

// EmbeddingError lets transports distinguish provider availability from
// validation and persistence errors without inspecting error text.
type EmbeddingError struct {
	Err error
}

func (e EmbeddingError) Error() string { return e.Err.Error() }
func (e EmbeddingError) Unwrap() error { return e.Err }
func (e EmbeddingError) FailureInfo() failure.Info {
	info := failure.Describe(e.Err)
	if info.Source == "application" {
		info.Source = "knowledge_embedding"
	}
	return info
}

func IsEmbeddingError(err error) bool {
	var target EmbeddingError
	return errors.As(err, &target)
}

func (s *KnowledgeBase) Ingest(ctx context.Context, request domain.DocumentIngestRequest) (domain.Document, error) {
	if err := redaction.ValidateText(request.Title, request.SourceURI, request.Content); err != nil {
		return domain.Document{}, err
	}
	if err := redaction.ValidateValue(request.Metadata); err != nil {
		return domain.Document{}, err
	}
	source := strings.TrimSpace(request.SourceKey)
	if source == "" {
		source = strings.TrimSpace(request.SourceURI)
	}
	var existing domain.Document
	var found bool
	if source != "" {
		var err error
		existing, found, err = s.store.GetDocumentBySource(request.WorkspaceID, source)
		if err != nil {
			return domain.Document{}, err
		}
		if found && request.MetadataSchemaVersion == "" {
			request.MetadataSchemaVersion = existing.MetadataSchemaVersion
		}
		if found && request.Lifecycle == nil {
			request.Lifecycle = &existing.Lifecycle
		}
	}
	document, chunks, err := rag.BuildDocument(request)
	if err != nil {
		return domain.Document{}, err
	}
	if found {
		if store.SameDocumentIndexInputs(existing, document) && (request.ExpectedUpdatedAt != nil || existing.MetadataSchemaVersion != document.MetadataSchemaVersion || !store.SameDocumentMetadata(existing.Metadata, document.Metadata) || existing.Lifecycle != document.Lifecycle) {
			if request.ExpectedUpdatedAt == nil {
				return domain.Document{}, store.ErrDocumentMetadataConflict
			}
			return s.UpdateMetadata(ctx, document.WorkspaceID, existing.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: *request.ExpectedUpdatedAt, Metadata: document.Metadata, MetadataSchemaVersion: document.MetadataSchemaVersion, Lifecycle: &document.Lifecycle, ExpectedIndexInputs: &document})
		}
	}
	if request.ExpectedUpdatedAt != nil {
		return domain.Document{}, store.ErrDocumentMetadataRequiresReindex
	}
	return s.persist(ctx, document, chunks)
}

func (s *KnowledgeBase) UpdateMetadata(ctx context.Context, workspaceID, id string, update domain.DocumentMetadataUpdate) (domain.Document, error) {
	if err := ctx.Err(); err != nil {
		return domain.Document{}, err
	}
	if err := redaction.ValidateValue(update.Metadata); err != nil {
		if errors.Is(err, redaction.ErrCredentialContent) {
			return domain.Document{}, err
		}
		// JSON representation errors are invalid input, not Store availability.
		return domain.Document{}, store.ErrInvalidDocumentMetadata
	}
	return s.store.UpdateDocumentMetadata(workspaceID, id, update)
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
	if _, err := eligibility.Compile(search.MetadataFilter); err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return domain.DocumentSearchResponse{}, errors.New("query is required")
	}
	if err := redaction.ValidateText(search.Query); err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	if err := redaction.ValidateValue(search.Metadata); err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	if err := redaction.ValidateValue(search.MetadataFilter); err != nil {
		return domain.DocumentSearchResponse{}, err
	}

	embedding, err := rag.EmbedQuery(ctx, search.Query, func(ctx context.Context, query string) (rag.Embedding, error) {
		result, embedErr := s.embedder.EmbedText(ctx, query)
		return rag.Embedding{
			Vector:     result.Vector,
			Provider:   result.Provider,
			Model:      result.Model,
			Dimensions: result.Dimensions,
			Estimated:  result.Estimated,
		}, embedErr
	})
	if err != nil {
		return domain.DocumentSearchResponse{}, EmbeddingError{Err: err}
	}
	if requestedLimit <= 0 {
		requestedLimit = search.Limit
	}
	return s.retriever.Search(ctx, search, requestedLimit, embedding)
}

func (s *KnowledgeBase) Evaluate(ctx context.Context, request domain.RAGEvaluationRunRequest) (domain.RAGEvaluationRunResponse, error) {
	cases, dataset, err := rag.ResolveEvaluationCases(request)
	if err != nil {
		return domain.RAGEvaluationRunResponse{}, err
	}

	topK := rag.NormalizeSearchLimit(request.TopK)
	results := make([]domain.RAGEvaluationCaseResult, 0, len(cases))
	summary := domain.RAGEvaluationSummary{Total: len(cases)}
	var embedding domain.EmbeddingInfo
	var fusion domain.FusionInfo
	var reranker domain.RerankerInfo
	var relevanceGate domain.RelevanceGateInfo
	for _, evaluationCase := range cases {
		if evaluationCase.Answerable == nil || *evaluationCase.Answerable {
			summary.AnswerableCases++
		} else {
			summary.UnanswerableCases++
		}
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
			fusion = response.Fusion
			reranker = response.Reranker
			relevanceGate = response.RelevanceGate
		}
		caseResult := rag.EvaluateCase(evaluationCase, response.Items)
		caseResult.Security = response.Security
		results = append(results, caseResult)
		summary.BlockedCandidates += response.Security.BlockedCandidates
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
	return domain.RAGEvaluationRunResponse{Dataset: dataset, Summary: summary, Cases: results, Embedding: embedding, Fusion: fusion, Reranker: reranker, RelevanceGate: relevanceGate}, nil
}
