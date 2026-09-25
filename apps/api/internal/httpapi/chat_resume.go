package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (h *Handler) continueRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/continue"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	var req apicontract.ContinueRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if rejectCredentialContent(w, r, req) {
		return
	}

	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	reservation, admitted := h.reserveRunCapacity(w, r)
	if !admitted {
		return
	}
	defer reservation.Cancel()
	releaseRun, admitted := h.acquireRunSlot(w, r, reservation, run.ConversationID)
	if !admitted {
		return
	}
	defer releaseRun()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeRunStateSSE(w, flusher, run.ConversationID, run.ID, run.AgentID, run.Status)

	runCtx := runExecutionContext(r)
	events, errs := h.agentRuntime.ContinueCollaboration(runCtx, id, req.Plan, routingRequirementsFromInput(req.RoutingRequirements))
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		status, failRun := continuationFailurePolicy(err)
		if failRun {
			_, _ = h.agentRuntime.FailRun(id, err)
		}
		writeSSE(w, "error", failureChatChunk(w, r, status, err))
		flusher.Flush()
		return
	}

	h.completeStreamingRun(w, flusher, r, runCtx, runCompletionRequest{
		WorkspaceID: run.WorkspaceID, RunID: id, ConversationID: run.ConversationID, Assistant: assistant.String(),
	})
}

func continuationFailurePolicy(err error) (status int, failRun bool) {
	if errors.Is(err, agentpkg.ErrNoEligibleAgent) || errors.Is(err, agentpkg.ErrNoSuitableAgent) || errors.Is(err, agentpkg.ErrInvalidRoutingRequirements) {
		return http.StatusUnprocessableEntity, true
	}
	return http.StatusInternalServerError, !strings.Contains(err.Error(), "not waiting for user input")
}

func (h *Handler) resumeRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/resume"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	var req apicontract.ResumeRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.UserInput = strings.TrimSpace(req.UserInput)
	if rejectCredentialContent(w, r, req) {
		return
	}

	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status == domain.RunWaitingForUser && req.UserInput == "" {
		writeError(w, http.StatusBadRequest, "user input is required")
		return
	}
	if run.Status != domain.RunWaitingForUser && run.Status != domain.RunFailedRecoverable {
		writeError(w, http.StatusConflict, "run is not resumable in its current state")
		return
	}
	toolEffects, err := scoped.ListToolEffects(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	for _, effect := range toolEffects {
		if domain.ToolEffectRequiresReconciliation(effect.Status) {
			writeError(w, http.StatusConflict, "run has unresolved tool effects; reconcile them before resuming")
			return
		}
	}
	reservation, admitted := h.reserveRunCapacity(w, r)
	if !admitted {
		return
	}
	defer reservation.Cancel()
	releaseRun, admitted := h.acquireRunSlot(w, r, reservation, run.ConversationID)
	if !admitted {
		return
	}
	defer releaseRun()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeRunStateSSE(w, flusher, run.ConversationID, run.ID, run.AgentID, run.Status)

	resumeCtx := runExecutionContext(r)
	events, errs := h.agentRuntime.ResumeAutonomous(resumeCtx, id, req.UserInput)
	if run.Status == domain.RunFailedRecoverable {
		if run.RuntimeSnapshot != nil && run.RuntimeSnapshot.Mode == agentpkg.ChatModeMultiAgent {
			events, errs = h.agentRuntime.ResumeRecoverableCollaboration(resumeCtx, id)
		} else {
			events, errs = h.agentRuntime.ResumeRecoverableAutonomous(resumeCtx, id, req.UserInput)
		}
	}
	var assistant strings.Builder
	for event := range events {
		writeUnifiedRunEvent(w, flusher, event, &assistant)
	}

	if err := <-errs; err != nil {
		status, failRun := resumeFailurePolicy(err)
		if failRun {
			_, _ = h.agentRuntime.FailRun(id, err)
		}
		writeSSE(w, "error", failureChatChunk(w, r, status, err))
		flusher.Flush()
		return
	}

	current, ok, err := scoped.GetRun(id)
	if err != nil {
		_, _ = h.agentRuntime.FailRun(id, err)
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusInternalServerError, err))
		flusher.Flush()
		return
	}
	if !ok {
		writeSSE(w, "error", failureChatChunk(w, r, http.StatusNotFound, store.ErrNotFound("run")))
		flusher.Flush()
		return
	}
	if current.Status == domain.RunWaitingForUser || current.Status == domain.RunCanceled {
		writeTerminalRunDone(w, flusher, current)
		return
	}

	h.completeStreamingRun(w, flusher, r, resumeCtx, runCompletionRequest{
		WorkspaceID: current.WorkspaceID, RunID: id, ConversationID: current.ConversationID, Assistant: assistant.String(),
	})
}

func resumeFailurePolicy(err error) (status int, failRun bool) {
	if errors.Is(err, agentpkg.ErrRuntimeSnapshotResumeUnsupported) ||
		errors.Is(err, agentpkg.ErrRuntimeSnapshotUnavailable) ||
		errors.Is(err, agentpkg.ErrRuntimeExecutorUnsupported) {
		return http.StatusConflict, false
	}
	if strings.Contains(err.Error(), "not waiting for user input") || strings.Contains(err.Error(), "not recoverable") {
		return http.StatusBadRequest, false
	}
	return http.StatusInternalServerError, true
}
