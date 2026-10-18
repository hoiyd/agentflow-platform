package httpapi

import (
	"errors"
	"io"
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/rag/metadataschema"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) updateDocumentMetadata(w http.ResponseWriter, r *http.Request) {
	var update domain.DocumentMetadataUpdate
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	decoder, err := documentJSONDecoder(r.Body, 65536)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or ambiguous metadata JSON")
		return
	}
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, 400, "invalid metadata update")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid metadata update")
		return
	}
	document, err := h.knowledge.UpdateMetadata(r.Context(), workspaceIDFromRequest(r), r.PathValue("id"), update)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case store.IsNotFound(err):
			status = http.StatusNotFound
		case errors.Is(err, store.ErrDocumentMetadataConflict), errors.Is(err, store.ErrDocumentMetadataRequiresReindex):
			status = http.StatusConflict
		case failure.Describe(err).Category == failure.CategoryValidation:
			status = http.StatusBadRequest
		}
		writeFailure(w, r, status, err)
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (h *Handler) getDocumentMetadataSchema(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metadataschema.Describe())
}
