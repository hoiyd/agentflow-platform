package inferencecompat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/routing"
)

func TestRunProducesCompatibilityEvidence(t *testing.T) {
	var mu sync.Mutex
	runModes := map[string]string{}
	canceled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/chat/completions":
			serveModel(w, r)
		case r.URL.Path == "/api/chat":
			var input struct {
				Mode    string `json:"mode"`
				Message string `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			if strings.HasPrefix(input.Message, "Write a detailed compatibility report") {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"type\":\"run.started\",\"run_id\":\"run-cancel\"}\n\n")
				return
			}
			runID := "run-" + input.Mode
			mu.Lock()
			runModes[runID] = input.Mode
			mu.Unlock()
			status := domain.RunCompleted
			if input.Mode == "multi_agent" {
				status = domain.RunWaitingForUser
			}
			writeRunStream(w, runID, status)
		case strings.HasSuffix(r.URL.Path, "/continue"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/continue")
			writeRunStream(w, runID, domain.RunCompleted)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			mu.Lock()
			canceled = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(domain.Run{ID: "run-cancel", Status: domain.RunCanceling})
		case strings.HasSuffix(r.URL.Path, "/replay"):
			runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/replay")
			if runID == "run-cancel" {
				mu.Lock()
				wasCanceled := canceled
				mu.Unlock()
				if wasCanceled {
					_ = json.NewEncoder(w).Encode(domain.RunReplay{Run: domain.Run{ID: runID, Status: domain.RunCanceled},
						RunEvents: []domain.RunEvent{{Type: domain.EventStageStarted, StageID: "stage-1"}, {Type: domain.EventRunCancelRequested}, {Type: domain.EventStageFailed, StageID: "stage-1"}, {Type: domain.EventRunCanceled}}})
				} else {
					_ = json.NewEncoder(w).Encode(domain.RunReplay{Run: domain.Run{ID: runID, Status: domain.RunRunning},
						RunEvents: []domain.RunEvent{{Type: domain.EventModelStarted}}})
				}
				return
			}
			mu.Lock()
			mode := runModes[runID]
			mu.Unlock()
			writeReplay(w, serverURL(r), runID, mode)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Run(t.Context(), Options{
		Target: Target{BackendVersion: "llama.cpp-b15ca938a", BaseURL: server.URL + "/v1", Model: "fixture-model",
			ModelArtifact: "fixture.gguf", Quantization: "Q4_K_M", ContextWindow: 128, MaxOutputTokens: 64, Hardware: "test-cpu",
			Capabilities: routing.Capabilities{Streaming: true, ToolCalling: true, StructuredOutput: true}},
		RouteID: "llama-cpp", RequestTimeout: time.Second, AgentFlowBaseURL: server.URL,
		WorkspaceID: "compatibility", AgentID: "agent-1", Revision: "test-revision",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed {
		t.Fatalf("expected compatibility gate to pass: %#v", report.Gate.Reasons)
	}
	if len(report.Checks) != 11 || len(report.Modes) != 3 {
		t.Fatalf("unexpected evidence surface: checks=%d modes=%d", len(report.Checks), len(report.Modes))
	}
	for _, mode := range report.Modes {
		if mode.Status != "passed" || mode.SelectedRoute != "llama-cpp" || mode.TotalTokens != 12 {
			t.Fatalf("unexpected mode evidence: %#v", mode)
		}
	}
	if report.Target.CancellationProof != "transport_run_terminal_and_capacity_probe" {
		t.Fatalf("unexpected cancellation claim: %q", report.Target.CancellationProof)
	}
}

func TestRunCancellationRejectsOpenStage(t *testing.T) {
	canceled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/chat":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"run.started\",\"run_id\":\"run-cancel\"}\n\n")
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			canceled = true
			_ = json.NewEncoder(w).Encode(domain.Run{ID: "run-cancel", Status: domain.RunCanceling})
		case strings.HasSuffix(r.URL.Path, "/replay"):
			status := domain.RunRunning
			events := []domain.RunEvent{{Type: domain.EventStageStarted, StageID: "stage-1"}, {Type: domain.EventModelStarted, StageID: "stage-1"}}
			if canceled {
				status = domain.RunCanceled
				events = append(events, domain.RunEvent{Type: domain.EventRunCancelRequested}, domain.RunEvent{Type: domain.EventRunCanceled})
			}
			_ = json.NewEncoder(w).Encode(domain.RunReplay{Run: domain.Run{ID: "run-cancel", Status: status}, RunEvents: events})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	err := checkRunCancellation(t.Context(), server.Client(), Options{AgentFlowBaseURL: server.URL, WorkspaceID: "compatibility", AgentID: "agent-1"}, "local")
	if err == nil || !strings.Contains(err.Error(), "stage.started events without terminal events") {
		t.Fatalf("expected open stage rejection, got %v", err)
	}
}

func TestRunKeepsUnsupportedToolCallingExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(serveModel))
	defer server.Close()
	report, err := Run(t.Context(), Options{Target: Target{
		BackendVersion: "version", BaseURL: server.URL, Model: "model", ModelArtifact: "model.gguf", Quantization: "Q4_K_M",
		ContextWindow: 128, MaxOutputTokens: 64, Hardware: "test-cpu", Capabilities: routing.Capabilities{Streaming: true},
	}, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range report.Checks {
		if check.Name == "tool_calling" && check.Status == "skipped" && strings.Contains(check.Detail, "explicitly false") {
			return
		}
	}
	t.Fatal("missing explicit unsupported Tool Calling evidence")
}

func TestRunRejectsIncompleteTargetIdentity(t *testing.T) {
	_, err := Run(t.Context(), Options{Target: Target{BaseURL: "http://localhost:8081/v1"}})
	if err == nil || !strings.Contains(err.Error(), "fixed target identity is incomplete") {
		t.Fatalf("expected target validation error, got %v", err)
	}
}

func TestTargetHashIgnoresMeasuredCancellationProof(t *testing.T) {
	target := Target{Model: "fixture-model"}
	before := targetHash(target)
	target.CancellationProof = "transport_and_capacity_probe"
	if targetHash(target) != before {
		t.Fatal("measured cancellation result changed the fixed target identity")
	}
}

func TestStructuredOutputRejectsInvalidSchemaContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not JSON"}}]}`))
	}))
	defer server.Close()
	options := Options{Target: Target{BaseURL: server.URL, Model: "fixture-model"}}
	if err := checkStructuredOutput(t.Context(), server.Client(), options, "local"); err == nil || !strings.Contains(err.Error(), "invalid schema content") {
		t.Fatalf("expected invalid schema evidence, got %v", err)
	}
}

func serveModel(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var input struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Stream         bool            `json:"stream"`
		Tools          []any           `json:"tools"`
		ResponseFormat json.RawMessage `json:"response_format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, message := range input.Messages {
		if len(message.Content) > 1000 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"context length exceeded","code":"context_length_exceeded"}}`))
			return
		}
	}
	if input.Stream {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one \"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"two\"}}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if len(input.Tools) > 0 {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
		return
	}
	if len(input.ResponseFormat) > 0 {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"done\":true}"}}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
		return
	}
	_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"compatibility ok"}}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
}

func writeRunStream(w http.ResponseWriter, runID string, status domain.RunStatus) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: run.started\ndata: {\"type\":\"run.started\",\"run_id\":%q}\n\n", runID)
	_, _ = fmt.Fprintf(w, "event: done\ndata: {\"type\":\"done\",\"run_id\":%q,\"status\":%q}\n\n", runID, status)
}

func writeReplay(w http.ResponseWriter, baseURL, runID, mode string) {
	replay := domain.RunReplay{
		Run: domain.Run{ID: runID, Status: domain.RunCompleted},
		RuntimeSnapshot: &domain.RuntimeSnapshot{Mode: mode, ModelRouting: domain.ModelRouteCatalogSnapshot{Routes: []domain.ModelRouteDescriptor{{
			ID: "llama-cpp", Model: "fixture-model", Endpoint: baseURL + "/v1",
		}}}},
		UsageLedger: domain.RunUsageLedger{Totals: domain.RunUsageTotals{TotalTokens: 12}},
		RunEvents:   []domain.RunEvent{{Type: domain.EventModelRouteDecided, Payload: map[string]any{"outcome": "selected", "selected_route_id": "llama-cpp"}}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(replay)
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
