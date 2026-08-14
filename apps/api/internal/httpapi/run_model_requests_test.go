package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrequest"
	"agentflow-platform/apps/api/internal/requestcapture"
)

func TestListModelRequestsHidesContentByDefaultAndReturnsManifest(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	manifest := domain.ContextManifest{
		ID: "ctx-http", RunID: run.ID, ModelCallID: "call-http", Model: "test",
		Entries: []domain.ContextManifestEntry{
			{Source: "system", Selected: true, EstimatedTokens: 5},
			{Source: "history", Selected: false, EstimatedTokens: 3},
		},
	}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventContextAssembled, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"manifest": manifest},
	}); err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	recorder := requestcapture.NewRecorder(fileStore, requestcapture.Options{Mode: domain.ModelRequestCaptureFull})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	payload := []byte(`{"model":"test","messages":[{"role":"user","content":"debug me"}]}`)
	if err := recorder.Record(ctx, modelrequest.Observation{
		ModelCallID: "call-http", Operation: "chat.completion", Provider: "local", Model: "test",
		ContextManifestID: manifest.ID, SourceTokenBreakdown: map[string]int{"system": 5}, Payload: payload,
	}); err != nil {
		t.Fatalf("record model request: %v", err)
	}
	handler := &Handler{store: fileStore}

	responseRecorder := httptest.NewRecorder()
	handler.listModelRequests(responseRecorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/model_requests", nil))
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("list requests status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var response modelRequestDebugResponse
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ReconstructabilityStatus != "valid" || len(response.Records) != 1 ||
		response.Records[0].Capture.Content != "" || response.Records[0].Manifest == nil ||
		response.Records[0].Manifest.ID != manifest.ID || !response.Records[0].Capture.Reconstructable ||
		!response.Records[0].SourceDiff.MatchesEnvelope || response.Records[0].SourceDiff.ManifestExcludedTokens["history"] != 3 {
		t.Fatalf("unexpected metadata response: %#v", response)
	}

	contentRecorder := httptest.NewRecorder()
	handler.listModelRequests(contentRecorder, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/model_requests?include_content=true", nil))
	var withContent modelRequestDebugResponse
	_ = json.Unmarshal(contentRecorder.Body.Bytes(), &withContent)
	if len(withContent.Records) != 1 || withContent.Records[0].Capture.Content != string(payload) {
		t.Fatalf("explicit content response missing capture: %#v", withContent)
	}
}

func TestListModelRequestsEnforcesWorkspaceScope(t *testing.T) {
	fileStore, run := createHTTPTestRun(t)
	handler := &Handler{store: fileStore}
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/model_requests", nil)
	request.Header.Set(WorkspaceHeader, "another-workspace")
	recorder := httptest.NewRecorder()

	handler.listModelRequests(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected workspace-scoped 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
