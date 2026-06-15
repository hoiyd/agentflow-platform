package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	documentChunkSize      = 4000
	documentChunkOverlap   = 500
	documentUploadMaxBytes = 2 << 20
)

var orderedMarkdownListPattern = regexp.MustCompile(`^\d+[.)]\s+`)

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
		writeError(w, http.StatusInternalServerError, err.Error())
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
	document, chunks, err := buildDocumentChunks(req)
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
	format, mimeType, ok := documentFormatFromFilename(filename)
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
	document, chunks, err := buildDocumentChunks(domain.DocumentIngestRequest{
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
	response, err := h.runDocumentSearch(r.Context(), search, normalizeDocumentSearchLimit(search.Limit))
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
		requestedLimit = normalizeDocumentSearchLimit(search.Limit)
	}
	search.Limit = rerankCandidateLimit(requestedLimit)
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
	items = rerankDocumentChunks(search.Query, items, requestedLimit)
	return domain.DocumentSearchResponse{
		Items: items,
		Embedding: domain.EmbeddingInfo{
			Provider:   embedding.Provider,
			Model:      embedding.Model,
			Dimensions: embedding.Dimensions,
			Estimated:  embedding.Estimated,
		},
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
	topK := normalizeDocumentSearchLimit(req.TopK)
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
		caseResult := evaluateRAGCase(evalCase, response.Items)
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

func normalizeDocumentSearchLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 10 {
		return 10
	}
	return limit
}

func rerankCandidateLimit(limit int) int {
	if limit <= 0 {
		limit = 5
	}
	candidateLimit := limit * 4
	if candidateLimit < 10 {
		candidateLimit = 10
	}
	if candidateLimit > 20 {
		candidateLimit = 20
	}
	return candidateLimit
}

func rerankDocumentChunks(query string, items []domain.RetrievedDocumentChunk, limit int) []domain.RetrievedDocumentChunk {
	if len(items) == 0 {
		return items
	}
	queryTerms := queryTerms(query)
	for index := range items {
		items[index].LexicalBoost = lexicalBoost(query, queryTerms, items[index].Chunk.Content)
		items[index].MetadataBoost = metadataBoost(query, queryTerms, items[index])
		items[index].RerankScore = items[index].Score + items[index].LexicalBoost + items[index].MetadataBoost
		items[index].MatchedTerms = matchedTerms(query, queryTerms, items[index])
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].RerankScore > items[j].RerankScore
	})

	selected := make([]domain.RetrievedDocumentChunk, 0, minInt(limit, len(items)))
	usedDocuments := map[string]int{}
	for _, item := range items {
		if len(selected) >= limit {
			break
		}
		documentUses := usedDocuments[item.Document.ID]
		if documentUses > 0 && hasUnselectedDocument(items, usedDocuments) {
			item.DiversityPenalty = 0.04 * float64(documentUses)
			item.RerankScore -= item.DiversityPenalty
			if documentUses >= 2 {
				continue
			}
		}
		selected = append(selected, item)
		usedDocuments[item.Document.ID]++
	}
	if len(selected) < limit {
		seenChunks := map[string]bool{}
		for _, item := range selected {
			seenChunks[item.Chunk.ID] = true
		}
		for _, item := range items {
			if len(selected) >= limit {
				break
			}
			if seenChunks[item.Chunk.ID] {
				continue
			}
			selected = append(selected, item)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].RerankScore > selected[j].RerankScore
	})
	for index := range selected {
		selected[index].RerankRank = index + 1
	}
	return selected
}

func evaluateRAGCase(evalCase domain.RAGEvaluationCase, items []domain.RetrievedDocumentChunk) domain.RAGEvaluationCaseResult {
	result := domain.RAGEvaluationCaseResult{
		ID:                    strings.TrimSpace(evalCase.ID),
		Query:                 strings.TrimSpace(evalCase.Query),
		ExpectedDocumentIDs:   evalCase.ExpectedDocumentIDs,
		ExpectedChunkIDs:      evalCase.ExpectedChunkIDs,
		ExpectedChunkContains: evalCase.ExpectedChunkContains,
		Tags:                  evalCase.Tags,
		Items:                 items,
	}
	if result.ID == "" {
		result.ID = result.Query
	}
	if result.Query == "" {
		result.FailureReason = "query is required"
		return result
	}
	bestRank := 0
	for index, item := range items {
		if ragCaseItemMatches(evalCase, item) {
			bestRank = index + 1
			break
		}
	}
	if bestRank == 0 {
		result.FailureReason = "no result matched expected document, chunk, or content"
		return result
	}
	result.BestRank = bestRank
	result.HitAt1 = bestRank <= 1
	result.HitAt3 = bestRank <= 3
	result.HitAt5 = bestRank <= 5
	acceptableRank := evalCase.MinAcceptableRank
	if acceptableRank <= 0 {
		acceptableRank = len(items)
	}
	result.Hit = bestRank <= acceptableRank
	return result
}

func ragCaseItemMatches(evalCase domain.RAGEvaluationCase, item domain.RetrievedDocumentChunk) bool {
	hasExpectation := false
	if len(evalCase.ExpectedDocumentIDs) > 0 {
		hasExpectation = true
		if !stringInSlice(item.Document.ID, evalCase.ExpectedDocumentIDs) {
			return false
		}
	}
	if len(evalCase.ExpectedChunkIDs) > 0 {
		hasExpectation = true
		if !stringInSlice(item.Chunk.ID, evalCase.ExpectedChunkIDs) {
			return false
		}
	}
	if len(evalCase.ExpectedChunkContains) > 0 {
		hasExpectation = true
		content := strings.ToLower(item.Chunk.Content)
		for _, expected := range evalCase.ExpectedChunkContains {
			expected = strings.ToLower(strings.TrimSpace(expected))
			if expected == "" {
				continue
			}
			if !strings.Contains(content, expected) {
				return false
			}
		}
	}
	return hasExpectation
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}

func hasUnselectedDocument(items []domain.RetrievedDocumentChunk, usedDocuments map[string]int) bool {
	for _, item := range items {
		if usedDocuments[item.Document.ID] == 0 {
			return true
		}
	}
	return false
}

func lexicalBoost(query string, queryTerms []string, content string) float64 {
	content = strings.ToLower(content)
	query = strings.ToLower(strings.TrimSpace(query))
	boost := 0.0
	if query != "" && strings.Contains(content, query) {
		boost += 0.18
	}
	if len(queryTerms) == 0 {
		return boost
	}
	matches := 0
	for _, term := range queryTerms {
		if strings.Contains(content, term) {
			matches++
		}
	}
	boost += 0.14 * float64(matches) / float64(len(queryTerms))
	return boost
}

func metadataBoost(query string, queryTerms []string, item domain.RetrievedDocumentChunk) float64 {
	text := strings.Join([]string{
		item.Document.Title,
		item.Document.SourceURI,
		metadataText(item.Document.Metadata, "filename"),
		metadataText(item.Chunk.Metadata, "title"),
		metadataText(item.Chunk.Metadata, "heading_path"),
		metadataText(item.Chunk.Metadata, "chunk_type"),
	}, " ")
	text = strings.ToLower(text)
	query = strings.ToLower(strings.TrimSpace(query))
	boost := 0.0
	if query != "" && strings.Contains(text, query) {
		boost += 0.12
	}
	if len(queryTerms) == 0 {
		return boost
	}
	matches := 0
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			matches++
		}
	}
	boost += 0.10 * float64(matches) / float64(len(queryTerms))
	return boost
}

func matchedTerms(query string, queryTerms []string, item domain.RetrievedDocumentChunk) []string {
	text := strings.ToLower(strings.Join([]string{
		item.Document.Title,
		item.Document.SourceURI,
		item.Chunk.Content,
		metadataText(item.Document.Metadata, "filename"),
		metadataText(item.Chunk.Metadata, "title"),
		metadataText(item.Chunk.Metadata, "heading_path"),
		metadataText(item.Chunk.Metadata, "chunk_type"),
	}, " "))
	matches := []string{}
	seen := map[string]bool{}
	exact := strings.ToLower(strings.TrimSpace(query))
	if exact != "" && strings.Contains(text, exact) {
		seen[exact] = true
		matches = append(matches, exact)
	}
	for _, term := range queryTerms {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			continue
		}
		if strings.Contains(text, term) {
			seen[term] = true
			matches = append(matches, term)
		}
	}
	return matches
}

func metadataText(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(metadataValueString(value))
}

func metadataValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func queryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !(r >= '\u4e00' && r <= '\u9fff')
	})
	terms := make([]string, 0, len(fields)+len([]rune(query)))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
	}
	runes := []rune(strings.TrimSpace(query))
	for i := 0; i+1 < len(runes); i++ {
		if !isCJK(runes[i]) || !isCJK(runes[i+1]) {
			continue
		}
		term := string(runes[i : i+2])
		if seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func isCJK(r rune) bool {
	return r >= '\u4e00' && r <= '\u9fff'
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildDocumentChunks(req domain.DocumentIngestRequest) (domain.Document, []domain.DocumentChunk, error) {
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	if title == "" {
		return domain.Document{}, nil, errors.New("document title is required")
	}
	if content == "" {
		return domain.Document{}, nil, errors.New("document content is required")
	}
	sourceType := strings.TrimSpace(req.SourceType)
	if sourceType == "" {
		sourceType = "text"
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	now := time.Now().UTC()
	document := domain.Document{
		WorkspaceID: strings.TrimSpace(req.WorkspaceID),
		Title:       title,
		SourceType:  sourceType,
		SourceURI:   strings.TrimSpace(req.SourceURI),
		MimeType:    strings.TrimSpace(req.MimeType),
		Content:     content,
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	format := documentFormatFromRequest(req)
	metadata["format"] = format
	if sourceType == "file" {
		if filename := strings.TrimSpace(req.SourceURI); filename != "" {
			metadata["filename"] = filename
		}
	}
	parts := buildDocumentChunkParts(content, format)
	chunks := make([]domain.DocumentChunk, 0, len(parts))
	for index, part := range parts {
		chunkMetadata := copyMetadata(metadata)
		chunkMetadata["title"] = title
		chunkMetadata["chunk_index"] = index
		if _, ok := chunkMetadata["chunk_type"]; !ok {
			chunkMetadata["chunk_type"] = "text"
		}
		chunks = append(chunks, domain.DocumentChunk{
			ChunkIndex: index,
			Content:    part.Content,
			TokenCount: estimateDocumentTokens(part.Content),
			Metadata:   chunkMetadata,
			CreatedAt:  now,
		})
		for key, value := range part.Metadata {
			chunks[len(chunks)-1].Metadata[key] = value
		}
	}
	return document, chunks, nil
}

type documentChunkPart struct {
	Content  string
	Metadata map[string]any
}

func buildDocumentChunkParts(content string, format string) []documentChunkPart {
	if format == "markdown" {
		return splitMarkdownContent(content)
	}
	parts := splitDocumentContent(content, documentChunkSize, documentChunkOverlap)
	chunks := make([]documentChunkPart, 0, len(parts))
	for _, part := range parts {
		chunks = append(chunks, documentChunkPart{
			Content:  part,
			Metadata: map[string]any{"chunk_type": "text"},
		})
	}
	return chunks
}

func documentFormatFromRequest(req domain.DocumentIngestRequest) string {
	sourceType := strings.ToLower(strings.TrimSpace(req.SourceType))
	mimeType := strings.ToLower(strings.TrimSpace(req.MimeType))
	if sourceType == "markdown" || strings.Contains(mimeType, "markdown") {
		return "markdown"
	}
	if value, ok := req.Metadata["format"].(string); ok && strings.EqualFold(strings.TrimSpace(value), "markdown") {
		return "markdown"
	}
	return "text"
}

func documentFormatFromFilename(filename string) (string, string, bool) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt":
		return "text", "text/plain", true
	case ".md", ".markdown":
		return "markdown", "text/markdown", true
	default:
		return "", "", false
	}
}

func splitMarkdownContent(content string) []documentChunkPart {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	headingPath := []string{}
	parts := []documentChunkPart{}
	buffer := []string{}
	bufferType := ""
	inCode := false
	codeFence := ""
	codeLanguage := ""

	flush := func() {
		text := strings.TrimSpace(strings.Join(buffer, "\n"))
		if text == "" {
			buffer = nil
			bufferType = ""
			return
		}
		metadata := map[string]any{
			"chunk_type":    bufferType,
			"source_format": "markdown",
		}
		if len(headingPath) > 0 {
			metadata["heading_path"] = strings.Join(headingPath, " > ")
			text = markdownHeadingContext(headingPath) + "\n\n" + text
		}
		if codeLanguage != "" && bufferType == "code" {
			metadata["code_language"] = codeLanguage
		}
		parts = appendChunkPart(parts, text, metadata)
		buffer = nil
		bufferType = ""
		codeLanguage = ""
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCode {
			buffer = append(buffer, line)
			if strings.HasPrefix(trimmed, codeFence) {
				inCode = false
				flush()
			}
			continue
		}
		if fence, language, ok := markdownFenceStart(trimmed); ok {
			flush()
			inCode = true
			codeFence = fence
			codeLanguage = language
			bufferType = "code"
			buffer = append(buffer, line)
			continue
		}
		if level, title, ok := markdownHeading(trimmed); ok {
			flush()
			if level <= len(headingPath) {
				headingPath = headingPath[:level-1]
			}
			headingPath = append(headingPath, title)
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		lineType := "paragraph"
		if isMarkdownListLine(trimmed) {
			lineType = "list"
		}
		if bufferType != "" && bufferType != lineType {
			flush()
		}
		bufferType = lineType
		buffer = append(buffer, line)
	}
	flush()
	if len(parts) == 0 {
		return buildDocumentChunkParts(content, "text")
	}
	return parts
}

func markdownHeadingContext(headingPath []string) string {
	lines := make([]string, 0, len(headingPath))
	for index, heading := range headingPath {
		heading = strings.TrimSpace(heading)
		if heading == "" {
			continue
		}
		level := index + 1
		if level > 6 {
			level = 6
		}
		lines = append(lines, strings.Repeat("#", level)+" "+heading)
	}
	return strings.Join(lines, "\n")
}

func appendChunkPart(parts []documentChunkPart, content string, metadata map[string]any) []documentChunkPart {
	if len([]rune(content)) <= documentChunkSize {
		return append(parts, documentChunkPart{Content: content, Metadata: metadata})
	}
	for _, part := range splitDocumentContent(content, documentChunkSize, documentChunkOverlap) {
		partMetadata := copyMetadata(metadata)
		partMetadata["chunk_type"] = metadata["chunk_type"]
		partMetadata["split_reason"] = "oversize_markdown_block"
		parts = append(parts, documentChunkPart{Content: part, Metadata: partMetadata})
	}
	return parts
}

func markdownFenceStart(trimmed string) (string, string, bool) {
	for _, fence := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, fence) {
			return fence, strings.TrimSpace(strings.TrimPrefix(trimmed, fence)), true
		}
	}
	return "", "", false
}

func markdownHeading(trimmed string) (int, string, bool) {
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

func isMarkdownListLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "- ") ||
		strings.HasPrefix(trimmed, "* ") ||
		strings.HasPrefix(trimmed, "+ ") ||
		orderedMarkdownListPattern.MatchString(trimmed)
}

func splitDocumentContent(content string, size int, overlap int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if size <= 0 {
		size = documentChunkSize
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size / 4
	}
	runes := []rune(content)
	if len(runes) <= size {
		return []string{content}
	}
	parts := []string{}
	for start := 0; start < len(runes); {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return parts
}

func copyMetadata(metadata map[string]any) map[string]any {
	copied := make(map[string]any, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func estimateDocumentTokens(content string) int {
	runes := len([]rune(strings.TrimSpace(content)))
	if runes == 0 {
		return 0
	}
	return runes/4 + 1
}
