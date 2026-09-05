package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/toolreconciliation"
)

const maxToolEffectCommandBytes = 64 << 10

func (h *Handler) listToolEffects(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	scoped := h.scopedStore(r)
	if _, ok, err := scoped.GetRun(runID); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	status := domain.ToolEffectStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	if status != "" && !toolreconciliation.ValidToolEffectStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid tool effect status")
		return
	}
	toolName := strings.TrimSpace(r.URL.Query().Get("tool"))
	if len(toolName) > 128 {
		writeError(w, http.StatusBadRequest, "tool filter is too large")
		return
	}
	records, err := scoped.ListToolEffects(runID)
	if err != nil {
		writeToolEffectFailure(w, r, err)
		return
	}
	filtered := records[:0]
	for _, record := range records {
		if (status == "" || record.Status == status) && (toolName == "" || record.ToolName == toolName) {
			filtered = append(filtered, record)
		}
	}
	catalog, err := h.tools.Catalog()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "effects": toolreconciliation.NewToolEffectViews(catalog, filtered)})
}

func (h *Handler) reconcileToolEffect(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	idempotencyKey := strings.TrimSpace(r.PathValue("idempotency_key"))
	if runID == "" || idempotencyKey == "" || len(idempotencyKey) > 256 {
		writeError(w, http.StatusBadRequest, "valid run id and idempotency key are required")
		return
	}
	var command toolreconciliation.ToolEffectReconciliationCommand
	r.Body = http.MaxBytesReader(w, r.Body, maxToolEffectCommandBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		writeError(w, http.StatusBadRequest, "invalid reconciliation command")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid reconciliation command")
		return
	}
	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(runID)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	catalog, err := h.tools.Catalog()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	outcome, err := toolreconciliation.ReconcileToolEffect(r.Context(), catalog, scoped, run, idempotencyKey, command)
	if err != nil {
		writeToolEffectFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, outcome)
}

func writeToolEffectFailure(w http.ResponseWriter, r *http.Request, err error) {
	var reconciliation *toolreconciliation.ReconciliationError
	switch {
	case store.IsNotFound(err):
		writeFailure(w, r, http.StatusNotFound, err)
	case store.IsToolEffectConflict(err):
		writeFailure(w, r, http.StatusConflict, err)
	case errors.As(err, &reconciliation):
		switch reconciliation.Code {
		case toolreconciliation.ReconciliationNotFound:
			writeFailure(w, r, http.StatusNotFound, err)
		case toolreconciliation.ReconciliationConflict, toolreconciliation.ReconciliationUnavailable, toolreconciliation.ReconciliationMismatch:
			writeFailure(w, r, http.StatusConflict, err)
		default:
			writeFailure(w, r, http.StatusBadRequest, err)
		}
	default:
		writeFailure(w, r, http.StatusInternalServerError, err)
	}
}
