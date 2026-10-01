package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
)

func TestRunStreamFailureUsesDurableCancellationState(t *testing.T) {
	canceled := fmt.Errorf("stream stopped: %w", &openai.ModelError{Kind: openai.ErrorCanceled, Cause: context.Canceled})
	for _, tc := range []struct {
		name       string
		status     domain.RunStatus
		err        error
		failRun    bool
		wantStatus domain.RunStatus
		wantDone   bool
	}{
		{"requested cancellation", domain.RunCanceling, canceled, true, domain.RunCanceled, true},
		{"already canceled autonomous", domain.RunCanceled, errors.New("run canceled"), true, domain.RunCanceled, true},
		{"unrequested cancellation", domain.RunRunning, canceled, true, domain.RunFailed, false},
		{"provider failure during stop", domain.RunCanceling, &openai.ModelError{Kind: openai.ErrorTransport}, true, domain.RunFailed, false},
		{"rejected continuation", domain.RunWaitingForUser, errors.New("not waiting for user input"), false, domain.RunWaitingForUser, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage, run := createHTTPTestRun(t)
			if _, err := storage.UpdateRunStatus(run.ID, tc.status, ""); err != nil {
				t.Fatal(err)
			}
			runtime := newRuntimeForTest(agentpkg.RuntimeOptions{Store: storage}, newLocalFallbackOpenAIClientForTest())
			handler := &Handler{store: storage, agentRuntime: runtime}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/resume", nil)
			handler.finishRunStreamFailure(recorder, recorder, request, run.ID, http.StatusInternalServerError, tc.err, tc.failRun)
			current, ok, err := storage.GetRun(run.ID)
			if err != nil || !ok || current.Status != tc.wantStatus {
				t.Fatalf("run status=%s ok=%t err=%v", current.Status, ok, err)
			}
			body := recorder.Body.String()
			if tc.wantDone {
				if strings.Contains(body, "event: error") || !strings.HasPrefix(body, "event: done\ndata: ") {
					t.Fatalf("unexpected canceled stream: %s", body)
				}
				var chunk domain.ChatChunk
				if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(body, "event: done\ndata: "))), &chunk); err != nil {
					t.Fatal(err)
				}
				if chunk.Status != "canceled" || chunk.RunID != run.ID || chunk.ConversationID != run.ConversationID || chunk.AgentID != run.AgentID || chunk.VerificationStatus != string(current.VerificationStatus) {
					t.Fatalf("terminal identity lost: %#v", chunk)
				}
			} else if !strings.Contains(body, "event: error") || strings.Contains(body, "event: done") {
				t.Fatalf("real error suppressed: %s", body)
			}
		})
	}
}
