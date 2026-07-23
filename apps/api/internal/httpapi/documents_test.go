package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/store"
)

func TestDocumentIngestAndRAGSearchAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	handler := &Handler{store: fileStore, knowledge: knowledge.NewKnowledgeBase(fileStore, client)}

	createBody := []byte(`{
		"title": "Launch Notes",
		"content": "The launch password is amber-9137. Keep it in the deployment notes.",
		"metadata": {"project": "agentflow"}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewReader(createBody))
	createRecorder := httptest.NewRecorder()
	handler.createDocument(createRecorder, createReq)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var document domain.Document
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if document.ChunkCount != 1 || document.EmbeddingCount != 1 {
		t.Fatalf("expected one chunk and embedding, got %#v", document)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/documents/"+document.ID, nil)
	detailRecorder := httptest.NewRecorder()
	handler.getDocument(detailRecorder, detailReq)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("expected detail status 200, got %d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var detail struct {
		Document domain.Document        `json:"document"`
		Chunks   []domain.DocumentChunk `json:"chunks"`
	}
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode document detail: %v", err)
	}
	if detail.Document.ID != document.ID || len(detail.Chunks) != 1 {
		t.Fatalf("expected created document detail, got %#v", detail)
	}

	searchBody := []byte(`{"query":"What is the launch password?","metadata":{"project":"agentflow"},"limit":3,"min_similarity":0}`)
	searchReq := httptest.NewRequest(http.MethodPost, "/api/rag/search", bytes.NewReader(searchBody))
	searchRecorder := httptest.NewRecorder()
	handler.searchDocumentChunks(searchRecorder, searchReq)
	if searchRecorder.Code != http.StatusOK {
		t.Fatalf("expected search status 200, got %d body=%s", searchRecorder.Code, searchRecorder.Body.String())
	}
	if strings.HasPrefix(strings.TrimSpace(searchRecorder.Body.String()), "[") {
		t.Fatalf("expected object search response with embedding metadata, got legacy array: %s", searchRecorder.Body.String())
	}
	var searchResponse domain.DocumentSearchResponse
	if err := json.Unmarshal(searchRecorder.Body.Bytes(), &searchResponse); err != nil {
		t.Fatalf("decode search results: %v", err)
	}
	items := searchResponse.Items
	if len(items) != 1 {
		t.Fatalf("expected one result, got %d", len(items))
	}
	if searchResponse.Embedding.Provider != "local" || !searchResponse.Embedding.Estimated {
		t.Fatalf("expected local estimated embedding metadata, got %#v", searchResponse.Embedding)
	}
	if searchResponse.Embedding.Model == "" {
		t.Fatalf("expected embedding model metadata, got %#v", searchResponse.Embedding)
	}
	if searchResponse.Embedding.Dimensions != 1536 {
		t.Fatalf("expected embedding dimensions metadata, got %#v", searchResponse.Embedding)
	}
	if !strings.Contains(items[0].Chunk.Content, "amber-9137") {
		t.Fatalf("expected launch password chunk, got %#v", items[0])
	}
	if items[0].RerankScore <= 0 {
		t.Fatalf("expected rerank score on search result, got %#v", items[0])
	}
	if items[0].VectorRank <= 0 || items[0].RerankRank <= 0 {
		t.Fatalf("expected vector and rerank ranks on search result, got %#v", items[0])
	}
	if items[0].LexicalRank <= 0 || items[0].LexicalScore <= 0 {
		t.Fatalf("expected independent lexical recall evidence on search result, got %#v", items[0])
	}
	if !strings.Contains(searchRecorder.Body.String(), `"lexical_rank"`) || !strings.Contains(searchRecorder.Body.String(), `"lexical_score"`) {
		t.Fatalf("expected lexical recall fields in API JSON, got %s", searchRecorder.Body.String())
	}
	if len(items[0].MatchedTerms) == 0 {
		t.Fatalf("expected matched terms on search result, got %#v", items[0])
	}
	if items[0].Confidence == "" || items[0].Confidence == "low" {
		t.Fatalf("expected relevant search result to pass confidence gate, got %#v", items[0])
	}
	if items[0].EvidenceScore <= 0 {
		t.Fatalf("expected evidence score on relevant search result, got %#v", items[0])
	}

	unrelatedBody := []byte(`{"query":"dinner recipe ideas","metadata":{"project":"agentflow"},"limit":3,"min_similarity":0}`)
	unrelatedReq := httptest.NewRequest(http.MethodPost, "/api/rag/search", bytes.NewReader(unrelatedBody))
	unrelatedRecorder := httptest.NewRecorder()
	handler.searchDocumentChunks(unrelatedRecorder, unrelatedReq)
	if unrelatedRecorder.Code != http.StatusOK {
		t.Fatalf("expected unrelated search status 200, got %d body=%s", unrelatedRecorder.Code, unrelatedRecorder.Body.String())
	}
	var unrelatedResponse domain.DocumentSearchResponse
	if err := json.Unmarshal(unrelatedRecorder.Body.Bytes(), &unrelatedResponse); err != nil {
		t.Fatalf("decode unrelated search results: %v", err)
	}
	if !unrelatedResponse.NoMatch || unrelatedResponse.Reason == "" || len(unrelatedResponse.Items) != 0 {
		t.Fatalf("expected unrelated query to be filtered by relevance gate, got %#v", unrelatedResponse)
	}

	evalBody := []byte(`{
		"top_k": 5,
		"min_similarity": 0,
		"metadata": {"project": "agentflow"},
		"cases": [
			{
				"id": "launch_password",
				"query": "What is the launch password?",
				"expected_document_ids": ["` + document.ID + `"],
				"expected_chunk_contains": ["amber-9137"],
				"min_acceptable_rank": 3,
				"tags": ["smoke"]
			},
			{
				"id": "missing_case",
				"query": "What is the launch password?",
				"expected_chunk_contains": ["not-in-the-document"],
				"min_acceptable_rank": 3
			}
		]
	}`)
	evalReq := httptest.NewRequest(http.MethodPost, "/api/rag/evaluations/run", bytes.NewReader(evalBody))
	evalRecorder := httptest.NewRecorder()
	handler.runRAGEvaluation(evalRecorder, evalReq)
	if evalRecorder.Code != http.StatusOK {
		t.Fatalf("expected evaluation status 200, got %d body=%s", evalRecorder.Code, evalRecorder.Body.String())
	}
	var evalResponse domain.RAGEvaluationRunResponse
	if err := json.Unmarshal(evalRecorder.Body.Bytes(), &evalResponse); err != nil {
		t.Fatalf("decode evaluation response: %v", err)
	}
	if evalResponse.Summary.Total != 2 || evalResponse.Summary.HitAt1 != 1 || evalResponse.Summary.HitAt3 != 1 || evalResponse.Summary.HitAt5 != 1 || evalResponse.Summary.Misses != 1 {
		t.Fatalf("unexpected evaluation summary: %#v", evalResponse.Summary)
	}
	if evalResponse.Embedding.Provider != "local" || evalResponse.Embedding.Model == "" {
		t.Fatalf("expected evaluation embedding metadata, got %#v", evalResponse.Embedding)
	}
	if len(evalResponse.Cases) != 2 {
		t.Fatalf("expected two evaluation cases, got %#v", evalResponse.Cases)
	}
	if !evalResponse.Cases[0].Hit || evalResponse.Cases[0].BestRank != 1 || len(evalResponse.Cases[0].Items) == 0 {
		t.Fatalf("expected first evaluation case to hit at rank 1, got %#v", evalResponse.Cases[0])
	}
	if evalResponse.Cases[1].Hit || evalResponse.Cases[1].FailureReason == "" {
		t.Fatalf("expected second evaluation case to miss with reason, got %#v", evalResponse.Cases[1])
	}

	thresholdBody := []byte(`{"query":"What is the launch password?","metadata":{"project":"agentflow"},"limit":3,"min_similarity":1.01}`)
	thresholdReq := httptest.NewRequest(http.MethodPost, "/api/rag/search", bytes.NewReader(thresholdBody))
	thresholdRecorder := httptest.NewRecorder()
	handler.searchDocumentChunks(thresholdRecorder, thresholdReq)
	if thresholdRecorder.Code != http.StatusOK {
		t.Fatalf("expected threshold search status 200, got %d body=%s", thresholdRecorder.Code, thresholdRecorder.Body.String())
	}
	var thresholdResponse domain.DocumentSearchResponse
	if err := json.Unmarshal(thresholdRecorder.Body.Bytes(), &thresholdResponse); err != nil {
		t.Fatalf("decode threshold search results: %v", err)
	}
	if len(thresholdResponse.Items) != 0 {
		t.Fatalf("expected high min_similarity to filter results, got %d", len(thresholdResponse.Items))
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/documents/"+document.ID, nil)
	deleteRecorder := httptest.NewRecorder()
	handler.deleteDocument(deleteRecorder, deleteReq)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected delete status 204, got %d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	deletedDetailRecorder := httptest.NewRecorder()
	handler.getDocument(deletedDetailRecorder, detailReq)
	if deletedDetailRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected deleted detail status 404, got %d body=%s", deletedDetailRecorder.Code, deletedDetailRecorder.Body.String())
	}

	deletedSearchReq := httptest.NewRequest(http.MethodPost, "/api/rag/search", bytes.NewReader(searchBody))
	deletedSearchRecorder := httptest.NewRecorder()
	handler.searchDocumentChunks(deletedSearchRecorder, deletedSearchReq)
	if deletedSearchRecorder.Code != http.StatusOK {
		t.Fatalf("expected deleted search status 200, got %d body=%s", deletedSearchRecorder.Code, deletedSearchRecorder.Body.String())
	}
	var deletedSearchResponse domain.DocumentSearchResponse
	if err := json.Unmarshal(deletedSearchRecorder.Body.Bytes(), &deletedSearchResponse); err != nil {
		t.Fatalf("decode deleted search results: %v", err)
	}
	if len(deletedSearchResponse.Items) != 0 {
		t.Fatalf("expected deleted document to disappear from search, got %d results", len(deletedSearchResponse.Items))
	}
}

func TestDeleteDocumentReturnsNotFoundForMissingDocument(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/documents/missing", nil)

	handler.deleteDocument(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUploadDocumentAPIAcceptsTxtAndMarkdown(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	handler := &Handler{store: fileStore, knowledge: knowledge.NewKnowledgeBase(fileStore, client)}

	for _, tc := range []struct {
		name       string
		filename   string
		content    string
		wantFormat string
	}{
		{
			name:       "txt",
			filename:   "launch.txt",
			content:    "The text upload password is blue-4281.",
			wantFormat: "text",
		},
		{
			name:       "markdown",
			filename:   "runbook.md",
			content:    "# Runbook\n\n## Deploy\n\nUse the markdown upload password green-7312.\n",
			wantFormat: "markdown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := multipartUploadBody(t, tc.filename, tc.content)
			req := httptest.NewRequest(http.MethodPost, "/api/documents/upload", body)
			req.Header.Set("Content-Type", contentType)
			recorder := httptest.NewRecorder()

			handler.uploadDocument(recorder, req)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("expected upload status 201, got %d body=%s", recorder.Code, recorder.Body.String())
			}
			var document domain.Document
			if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
				t.Fatalf("decode document: %v", err)
			}
			if document.SourceType != "file" || document.SourceURI != tc.filename {
				t.Fatalf("expected file source metadata, got %#v", document)
			}
			if document.Metadata["format"] != tc.wantFormat || document.Metadata["filename"] != tc.filename {
				t.Fatalf("expected upload metadata, got %#v", document.Metadata)
			}
			if document.ChunkCount == 0 || document.EmbeddingCount == 0 {
				t.Fatalf("expected chunks and embeddings, got %#v", document)
			}
		})
	}
}

func TestUploadDocumentAPIRejectsUnsupportedAndEmptyFiles(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	handler := &Handler{store: fileStore, knowledge: knowledge.NewKnowledgeBase(fileStore, client)}

	for _, tc := range []struct {
		name     string
		filename string
		content  string
	}{
		{name: "unsupported", filename: "notes.pdf", content: "not a pdf"},
		{name: "empty", filename: "empty.txt", content: "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := multipartUploadBody(t, tc.filename, tc.content)
			req := httptest.NewRequest(http.MethodPost, "/api/documents/upload", body)
			req.Header.Set("Content-Type", contentType)
			recorder := httptest.NewRecorder()

			handler.uploadDocument(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected upload status 400, got %d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func multipartUploadBody(t *testing.T, filename string, content string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, writer.FormDataContentType()
}
