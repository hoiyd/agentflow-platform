package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

const runEventKeepaliveInterval = 15 * time.Second

var errRunEventCursorAhead = errors.New("event cursor is ahead of the durable Run event sequence")

func (h *Handler) observeRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	after, err := runEventCursor(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	scoped := h.scopedStore(r)
	var replayEvents []domain.RunEvent
	subscription, err := h.runEvents.SnapshotAndSubscribe(r.Context(), runID, func() (domain.RunProjectionSnapshot, error) {
		replay, ok, loadErr := scoped.GetRunReplay(runID)
		if loadErr != nil {
			return domain.RunProjectionSnapshot{}, loadErr
		}
		if !ok {
			return domain.RunProjectionSnapshot{}, store.ErrNotFound("run")
		}
		if loadErr = h.attachRuntimeInvariants(scoped, &replay); loadErr != nil {
			return domain.RunProjectionSnapshot{}, loadErr
		}
		if after > replay.Projection.AsOfSequence {
			return domain.RunProjectionSnapshot{}, errRunEventCursorAhead
		}
		for _, item := range replay.RunEvents {
			if item.Sequence > after {
				replayEvents = append(replayEvents, item)
			}
		}
		return replay.Projection, nil
	})
	if err != nil {
		if errors.Is(err, errRunEventCursorAhead) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	defer subscription.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, item := range replayEvents {
		if err := writeSSEFrame(w, item.Sequence, string(item.Type), item); err != nil {
			return
		}
	}
	if err := writeSSEFrame(w, subscription.Snapshot.AsOfSequence, "run.snapshot", subscription.Snapshot); err != nil {
		return
	}
	flusher.Flush()
	if runObservationStopped(subscription.Snapshot.Run.Status) {
		return
	}

	keepalive := time.NewTicker(runEventKeepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case err, ok := <-subscription.Errors:
			if ok && err != nil {
				_ = writeSSEFrame(w, 0, "error", map[string]any{"type": "error", "code": "event_subscription_lost", "error": err.Error()})
				flusher.Flush()
			}
			return
		case item, ok := <-subscription.Events:
			if !ok {
				return
			}
			if err := writeSSEFrame(w, item.Sequence, string(item.Type), item); err != nil {
				return
			}
			flusher.Flush()
			if runEventStopsObservation(item) {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func runEventCursor(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if raw == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("event cursor must be a non-negative integer")
	}
	return cursor, nil
}

func writeSSEFrame(w http.ResponseWriter, sequence int64, event string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if sequence > 0 {
		if _, err = fmt.Fprintf(w, "id: %d\n", sequence); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}

func runEventStopsObservation(item domain.RunEvent) bool {
	return item.Type == domain.EventRunWaitingForUser || item.Type == domain.EventRunCompleted ||
		item.Type == domain.EventRunFailed || item.Type == domain.EventRunCanceled
}

func runObservationStopped(status domain.RunStatus) bool {
	return status == domain.RunWaitingForUser || status == domain.RunCompleted || status == domain.RunFailed ||
		status == domain.RunFailedRecoverable || status == domain.RunCanceled
}
