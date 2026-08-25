package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestChatRoutesExecuteDirectAndCollaborationLifecycles(t *testing.T) {
	dependencies := completeHandlerDependencies(t)
	fullStore := fullStoreForTest(t, dependencies)
	handler, err := NewHandler(dependencies)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	routes := handler.Routes()

	direct := performJSONRequest(t, routes, http.MethodPost, "/api/chat", `{"message":"Summarize the run status.","mode":"single"}`)
	if direct.Code != http.StatusOK || !strings.Contains(direct.Body.String(), "event: done") || !strings.Contains(direct.Body.String(), "event: model.delta") {
		t.Fatalf("unexpected direct chat response: status=%d body=%s", direct.Code, direct.Body.String())
	}
	runs, err := fullStore.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunCompleted {
		t.Fatalf("direct run lifecycle: runs=%#v err=%v", runs, err)
	}

	multi := performJSONRequest(t, routes, http.MethodPost, "/api/chat", `{"message":"Create a reviewed diagnostic plan.","mode":"multi_agent"}`)
	if multi.Code != http.StatusOK || !strings.Contains(multi.Body.String(), "event: done") || !strings.Contains(multi.Body.String(), `"status":"waiting_for_user"`) {
		t.Fatalf("unexpected collaboration response: status=%d body=%s", multi.Code, multi.Body.String())
	}
	runs, err = fullStore.ListRuns()
	if err != nil {
		t.Fatalf("list collaboration runs: %v", err)
	}
	waiting := findRunWithStatus(runs, domain.RunWaitingForUser)
	if waiting.ID == "" {
		t.Fatalf("expected waiting collaboration run, got %#v", runs)
	}

	continued := performJSONRequest(t, routes, http.MethodPost, "/api/runs/"+waiting.ID+"/continue", `{"plan":"1. Diagnose the issue. 2. Review the result."}`)
	if continued.Code != http.StatusOK || !strings.Contains(continued.Body.String(), "event: done") {
		t.Fatalf("unexpected continuation response: status=%d body=%s", continued.Code, continued.Body.String())
	}
	completed, found, err := fullStore.GetRun(waiting.ID)
	if err != nil || !found || completed.Status != domain.RunCompleted {
		t.Fatalf("continued run lifecycle: found=%v run=%#v err=%v", found, completed, err)
	}
}

func TestChatRouteExecutesBoundedAutonomousLifecycle(t *testing.T) {
	dependencies := completeHandlerDependencies(t)
	fullStore := fullStoreForTest(t, dependencies)
	handler, err := NewHandler(dependencies)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	response := performJSONRequest(t, handler.Routes(), http.MethodPost, "/api/chat", `{"message":"Produce a concise bounded result.","mode":"autonomous"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: run.progress") || !strings.Contains(response.Body.String(), `"mode":"autonomous"`) || !strings.Contains(response.Body.String(), "event: done") {
		t.Fatalf("unexpected autonomous response: status=%d body=%s", response.Code, response.Body.String())
	}
	runs, err := fullStore.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunCompleted {
		t.Fatalf("autonomous run lifecycle: runs=%#v err=%v", runs, err)
	}
}

func performJSONRequest(t *testing.T, handler http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

func findRunWithStatus(runs []domain.Run, status domain.RunStatus) domain.Run {
	for _, run := range runs {
		if run.Status == status {
			return run
		}
	}
	return domain.Run{}
}
