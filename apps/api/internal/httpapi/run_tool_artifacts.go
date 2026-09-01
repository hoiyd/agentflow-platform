package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

type scopedToolArtifactStore interface {
	ListToolArtifacts(runID string) ([]domain.ToolArtifact, error)
	ReadToolArtifact(runID string, artifactID string, offset int, limit int) (domain.ToolArtifactRead, error)
	SearchToolArtifact(runID string, artifactID string, query string, maxMatches int) (domain.ToolArtifactSearchResult, error)
}

func (h *Handler) listToolArtifacts(w http.ResponseWriter, r *http.Request) {
	artifactStore, ok := h.scopedStore(r).(scopedToolArtifactStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tool artifact storage is unavailable")
		return
	}
	items, err := artifactStore.ListToolArtifacts(r.PathValue("id"))
	if err != nil {
		writeToolArtifactFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": r.PathValue("id"), "artifacts": items})
}

func (h *Handler) readToolArtifact(w http.ResponseWriter, r *http.Request) {
	artifactStore, ok := h.scopedStore(r).(scopedToolArtifactStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tool artifact storage is unavailable")
		return
	}
	offset, err := optionalIntQuery(r, "offset", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := optionalIntQuery(r, "limit", 8*1024)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if offset < 0 || limit <= 0 || limit > store.MaxToolArtifactReadBytes {
		writeError(w, http.StatusBadRequest, "artifact read range is invalid")
		return
	}
	result, err := artifactStore.ReadToolArtifact(r.PathValue("id"), r.PathValue("artifact_id"), offset, limit)
	if err != nil {
		writeToolArtifactFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) searchToolArtifact(w http.ResponseWriter, r *http.Request) {
	artifactStore, ok := h.scopedStore(r).(scopedToolArtifactStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "tool artifact storage is unavailable")
		return
	}
	maxMatches, err := optionalIntQuery(r, "max_matches", 5)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len([]byte(query)) > store.MaxToolArtifactSearchQuery || maxMatches <= 0 || maxMatches > store.MaxToolArtifactMatches {
		writeError(w, http.StatusBadRequest, "artifact search parameters are invalid")
		return
	}
	result, err := artifactStore.SearchToolArtifact(
		r.PathValue("id"), r.PathValue("artifact_id"), query, maxMatches,
	)
	if err != nil {
		writeToolArtifactFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func optionalIntQuery(r *http.Request, name string, fallback int) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New(name + " must be an integer")
	}
	return parsed, nil
}

func writeToolArtifactFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case store.IsNotFound(err):
		writeError(w, http.StatusNotFound, "tool artifact not found")
	case errors.Is(err, store.ErrToolArtifactExpired):
		writeError(w, http.StatusGone, "tool artifact has expired")
	case errors.Is(err, store.ErrToolArtifactRange):
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "tool artifact range is invalid")
	default:
		writeFailure(w, r, http.StatusInternalServerError, err)
	}
}
