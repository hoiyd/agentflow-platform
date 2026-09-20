package httpapi

import (
	"net/http"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/projection"
)

func (h *Handler) listRunAttention(w http.ResponseWriter, r *http.Request) {
	scoped := h.scopedStore(r)
	runs, err := scoped.ListRuns()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	// ponytail: reuse canonical Replay assembly until measured Run volume
	// justifies a paginated store-level attention query.
	replays := make([]domain.RunReplay, 0, len(runs))
	for _, run := range runs {
		replay, ok, err := scoped.GetRunReplay(run.ID)
		if err != nil {
			writeFailure(w, r, http.StatusInternalServerError, err)
			return
		}
		if ok {
			replays = append(replays, replay)
		}
	}
	writeJSON(w, http.StatusOK, projection.BuildOperatorAttentionList(replays))
}
