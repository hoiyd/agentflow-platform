package httpapi

import (
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/tool/availability"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

func (h *Handler) toolCompensationPreview(w http.ResponseWriter, r *http.Request) {
	runID, key := strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("idempotency_key"))
	if runID == "" || key == "" || len(key) > 256 {
		writeError(w, http.StatusBadRequest, "valid run and effect identities are required")
		return
	}
	scoped := h.scopedStore(r)
	if _, found, err := scoped.GetRun(runID); err != nil {
		writeToolEffectFailure(w, r, err)
		return
	} else if !found {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	records, err := scoped.ListToolEffects(runID)
	if err != nil {
		writeToolEffectFailure(w, r, err)
		return
	}
	for _, record := range records {
		if record.IdempotencyKey != key {
			continue
		}
		catalog, err := h.tools.CatalogForWorkspace(workspaceIDFromRequest(r))
		if err != nil {
			writeToolEffectFailure(w, r, err)
			return
		}
		catalog, _, err = availability.Resolve(h.store, workspaceIDFromRequest(r), catalog)
		if err != nil {
			writeToolEffectFailure(w, r, err)
			return
		}
		preview, err := reconciliation.PreviewCompensation(r.Context(), catalog, record)
		if err != nil {
			writeToolEffectFailure(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, preview)
		return
	}
	writeError(w, http.StatusNotFound, "tool effect not found")
}
