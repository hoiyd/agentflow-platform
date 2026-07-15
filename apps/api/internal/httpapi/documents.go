package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
)

const documentUploadMaxBytes = 2 << 20

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	documents, err := h.store.ListDocuments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, documents)
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/documents/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "document id is required")
		return
	}
	document, chunks, ok, err := h.store.GetDocument(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"document": document,
		"chunks":   chunks,
	})
}

func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/documents/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "document id is required")
		return
	}
	if err := h.store.DeleteDocument(id); err != nil {
		status := http.StatusInternalServerError
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createDocument(w http.ResponseWriter, r *http.Request) {
	var req domain.DocumentIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	document, chunks, err := rag.BuildDocument(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.persistDocument(w, r, document, chunks)
}

func (h *Handler) uploadDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadMaxBytes)
	if err := r.ParseMultipartForm(documentUploadMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "document upload must be multipart/form-data and at most 2MB")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	filename := strings.TrimSpace(header.Filename)
	format, mimeType, ok := rag.FormatFromFilename(filename)
	if !ok {
		writeError(w, http.StatusBadRequest, "only .txt, .md, and .markdown files are supported")
		return
	}
	contentBytes, err := io.ReadAll(io.LimitReader(file, documentUploadMaxBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	if len(contentBytes) > documentUploadMaxBytes {
		writeError(w, http.StatusBadRequest, "document upload must be at most 2MB")
		return
	}
	content := strings.TrimSpace(string(contentBytes))
	if content == "" {
		writeError(w, http.StatusBadRequest, "document content is required")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	metadata := map[string]any{
		"filename": filename,
		"format":   format,
		"source":   "upload",
	}
	if rawMetadata := strings.TrimSpace(r.FormValue("metadata")); rawMetadata != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(rawMetadata), &extra); err != nil {
			writeError(w, http.StatusBadRequest, "metadata must be a JSON object")
			return
		}
		for key, value := range extra {
			metadata[key] = value
		}
	}
	document, chunks, err := rag.BuildDocument(domain.DocumentIngestRequest{
		Title:      title,
		Content:    content,
		SourceType: "file",
		SourceURI:  filename,
		MimeType:   mimeType,
		Metadata:   metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.persistDocument(w, r, document, chunks)
}

func (h *Handler) persistDocument(w http.ResponseWriter, r *http.Request, document domain.Document, chunks []domain.DocumentChunk) {
	embeddings := make([]domain.DocumentChunkEmbedding, 0, len(chunks))
	for _, chunk := range chunks {
		embedding, err := h.openAI.EmbedText(r.Context(), chunk.Content)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		embeddings = append(embeddings, domain.DocumentChunkEmbedding{
			Provider:   embedding.Provider,
			Model:      embedding.Model,
			Dimensions: len(embedding.Vector),
			Embedding:  embedding.Vector,
		})
	}
	created, err := h.store.CreateDocument(document, chunks, embeddings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) searchDocumentChunks(w http.ResponseWriter, r *http.Request) {
	var search domain.DocumentSearch
	if err := json.NewDecoder(r.Body).Decode(&search); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	response, err := h.runDocumentSearch(r.Context(), search, rag.NormalizeSearchLimit(search.Limit))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errDocumentSearchEmbedding) {
			status = http.StatusBadGateway
		}
		writeError(w, status, documentSearchErrorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

var errDocumentSearchEmbedding = errors.New("document search embedding")

func documentSearchErrorMessage(err error) string {
	message := strings.TrimSpace(err.Error())
	if errors.Is(err, errDocumentSearchEmbedding) {
		message = strings.TrimSpace(strings.TrimPrefix(message, errDocumentSearchEmbedding.Error()))
	}
	if message == "" {
		return "document search failed"
	}
	return message
}

func (h *Handler) runDocumentSearch(ctx context.Context, search domain.DocumentSearch, requestedLimit int) (domain.DocumentSearchResponse, error) {
	search.Query = strings.TrimSpace(search.Query)
	if search.Query == "" {
		return domain.DocumentSearchResponse{}, errors.New("query is required")
	}
	embedding, err := h.openAI.EmbedText(ctx, search.Query)
	if err != nil {
		return domain.DocumentSearchResponse{}, errors.Join(errDocumentSearchEmbedding, err)
	}
	if requestedLimit <= 0 {
		requestedLimit = rag.NormalizeSearchLimit(search.Limit)
	}
	search.Limit = rag.CandidateLimit(requestedLimit)
	search.Embedding = embedding.Vector
	search.EmbeddingProvider = embedding.Provider
	search.EmbeddingModel = embedding.Model
	items, err := h.store.SearchDocumentChunks(search)
	if err != nil {
		return domain.DocumentSearchResponse{}, err
	}
	for index := range items {
		items[index].VectorRank = index + 1
	}
	items = rag.Rerank(search.Query, items, requestedLimit)
	items = rag.ApplyRelevanceGate(items)
	noMatch := len(items) == 0
	reason := ""
	if noMatch {
		reason = "No confident match found. Top vector candidates did not pass the relevance gate."
	}
	return domain.DocumentSearchResponse{
		Items: items,
		Embedding: domain.EmbeddingInfo{
			Provider:   embedding.Provider,
			Model:      embedding.Model,
			Dimensions: embedding.Dimensions,
			Estimated:  embedding.Estimated,
		},
		NoMatch: noMatch,
		Reason:  reason,
	}, nil
}

func (h *Handler) runRAGEvaluation(w http.ResponseWriter, r *http.Request) {
	var req domain.RAGEvaluationRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(req.Cases) == 0 {
		writeError(w, http.StatusBadRequest, "at least one evaluation case is required")
		return
	}
	if len(req.Cases) > 50 {
		writeError(w, http.StatusBadRequest, "at most 50 evaluation cases are supported")
		return
	}
	topK := rag.NormalizeSearchLimit(req.TopK)
	results := make([]domain.RAGEvaluationCaseResult, 0, len(req.Cases))
	summary := domain.RAGEvaluationSummary{Total: len(req.Cases)}
	var embedding domain.EmbeddingInfo
	for _, evalCase := range req.Cases {
		search := domain.DocumentSearch{
			Query:         evalCase.Query,
			WorkspaceID:   req.WorkspaceID,
			Metadata:      req.Metadata,
			Limit:         topK,
			MinSimilarity: req.MinSimilarity,
		}
		response, err := h.runDocumentSearch(r.Context(), search, topK)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errDocumentSearchEmbedding) {
				status = http.StatusBadGateway
			}
			writeError(w, status, documentSearchErrorMessage(err))
			return
		}
		if embedding.Provider == "" {
			embedding = response.Embedding
		}
		caseResult := rag.EvaluateCase(evalCase, response.Items)
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
	writeJSON(w, http.StatusOK, domain.RAGEvaluationRunResponse{
		Summary:   summary,
		Cases:     results,
		Embedding: embedding,
	})
}
