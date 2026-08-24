package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
)

const documentUploadMaxBytes = 2 << 20

func (h *Handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	documents, err := h.scopedStore(r).ListDocuments()
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err)
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
	document, chunks, ok, err := h.scopedStore(r).GetDocument(id)
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err)
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
	if err := h.scopedStore(r).DeleteDocument(id); err != nil {
		status := http.StatusInternalServerError
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeFailure(w, status, err)
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
	workspaceID, matches := resolvePayloadWorkspace(r, req.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	req.WorkspaceID = workspaceID
	document, err := h.knowledge.Ingest(r.Context(), req)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, document)
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
	document, err := h.knowledge.Ingest(r.Context(), domain.DocumentIngestRequest{
		WorkspaceID: workspaceIDFromRequest(r),
		Title:       title,
		Content:     content,
		SourceType:  "file",
		SourceURI:   filename,
		MimeType:    mimeType,
		Metadata:    metadata,
	})
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, document)
}

func (h *Handler) searchDocumentChunks(w http.ResponseWriter, r *http.Request) {
	var search domain.DocumentSearch
	if err := json.NewDecoder(r.Body).Decode(&search); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	workspaceID, matches := resolvePayloadWorkspace(r, search.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	search.WorkspaceID = workspaceID
	response, err := h.knowledge.Search(r.Context(), search, rag.NormalizeSearchLimit(search.Limit))
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) runRAGEvaluation(w http.ResponseWriter, r *http.Request) {
	var req domain.RAGEvaluationRunRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	workspaceID, matches := resolvePayloadWorkspace(r, req.WorkspaceID)
	if !matches {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request scope")
		return
	}
	req.WorkspaceID = workspaceID
	response, err := h.knowledge.Evaluate(r.Context(), req)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeKnowledgeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if knowledge.IsEmbeddingError(err) {
		status = http.StatusBadGateway
	}
	writeFailure(w, status, err)
}
