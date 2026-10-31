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
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
)

func TestRunStreamFailureUsesDurableCancellationState(t *testing.T) {
	canceled := fmt.Errorf("stream stopped: %w", &openai.ModelError{Kind: openai.ErrorCanceled, Cause: context.Canceled})
	for _, tc := range []struct {
		name       string
		status     domain.RunStatus
		err        error
		wantStatus domain.RunStatus
		wantDone   bool
	}{
		{"requested cancellation", domain.RunCanceling, canceled, domain.RunCanceled, true},
		{"already canceled autonomous", domain.RunCanceled, errors.New("run canceled"), domain.RunCanceled, true},
		{"unrequested cancellation", domain.RunRunning, canceled, domain.RunFailed, false},
		{"provider failure during stop", domain.RunCanceling, &openai.ModelError{Kind: openai.ErrorTransport}, domain.RunFailed, false},
		{"rejected continuation", domain.RunWaitingForUser, &agentpkg.PreExecutionRejection{Err: errors.New("not waiting for user input")}, domain.RunWaitingForUser, false},
		{"execution error resembles rejection", domain.RunRunning, errors.New("not waiting for user input"), domain.RunFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage, run := createHTTPTestRun(t)
			if _, err := storage.UpdateRunStatus(run.ID, tc.status, ""); err != nil {
				t.Fatal(err)
			}
			runtime := newRuntimeForTest(agentpkg.RuntimeOptions{Store: storage}, newLocalFallbackOpenAIClientForTest())
			handler := &Handler{store: storage, agentRuntime: canceledContinuation{AgentRuntimeOperations: runtime, err: tc.err}, runController: concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/continue", strings.NewReader(`{"plan":"test"}`))
			handler.continueRun(recorder, request)
			current, ok, err := storage.GetRun(run.ID)
			if err != nil || !ok || current.Status != tc.wantStatus {
				t.Fatalf("run status=%s ok=%t err=%v", current.Status, ok, err)
			}
			body := recorder.Body.String()
			if tc.wantDone {
				if strings.Contains(body, "event: error") || !strings.Contains(body, "event: done\ndata: ") {
					t.Fatalf("unexpected canceled stream: %s", body)
				}
				var chunk domain.ChatChunk
				if err := json.Unmarshal([]byte(strings.TrimSpace(strings.Split(body, "event: done\ndata: ")[1])), &chunk); err != nil {
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

type canceledContinuation struct {
	AgentRuntimeOperations
	err error
}

func (r canceledContinuation) ContinueCollaboration(context.Context, string, string, domain.AgentRoutingRequirements) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	close(events)
	errs := make(chan error, 1)
	errs <- r.err
	close(errs)
	return events, errs
}
