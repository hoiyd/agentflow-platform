package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
)

func TestCrossEncoderPipelineScopeGateAndRuntimeModes(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("RERANKER_FIXTURE_KEY", "fixture-reranker-token")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-reranker-token" {
					t.Error("reranker credential not confined to transport")
				}
				var input struct {
					Texts []string `json:"texts"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Error("invalid rerank request")
					return
				}
				scores := []map[string]any{}
				for index, text := range input.Texts {
					if strings.Contains(text, "FOREIGN_WORKSPACE_SECRET") {
						t.Error("scope leaked to reranker")
					}
					scores = append(scores, map[string]any{"index": index, "score": 1})
				}
				_ = json.NewEncoder(w).Encode(scores)
			}))
			defer server.Close()
			reranker, err := rag.NewReranker(rag.RerankerConfig{Mode: "tei", BaseURL: server.URL, Model: "fixture-cross-encoder", Revision: "fixture-revision", APIKey: credential.FromEnvironment("RERANKER_FIXTURE_KEY")})
			if err != nil {
				t.Fatal(err)
			}
			fixture := newPipelineRegressionFixtureWithReranker(t, reranker)
			fixture.seedDocument(t)
			_, err = fixture.knowledge.Ingest(t.Context(), domain.DocumentIngestRequest{WorkspaceID: "foreign-workspace", Title: "Foreign document", Content: "FOREIGN_WORKSPACE_SECRET alpha-4242 recovery protocol", SourceType: "text"})
			if err != nil {
				t.Fatal(err)
			}
			api := fixture.search(t, "alpha-4242 recovery protocol")
			if api.NoMatch || api.Reranker.Provider != "tei" || api.Reranker.Model != "fixture-cross-encoder" {
				t.Fatalf("missing reranker metadata or result: %+v", api)
			}
			encoded, _ := json.Marshal(api)
			if strings.Contains(string(encoded), "fixture-reranker-token") {
				t.Fatal("reranker credential leaked into API metadata")
			}
			for _, item := range api.Items {
				if item.Document.WorkspaceID != pipelineRegressionWorkspace {
					t.Fatal("scope lost after reranking")
				}
			}
			replay := fixture.runModeAndReplay(t, "alpha-4242 recovery protocol", mode)
			found := false
			for _, event := range replay.RunEvents {
				if event.Type != domain.EventRetrievalCompleted {
					continue
				}
				found = true
				assertPipelineSuccessConsistent(t, api, event.Payload)
			}
			if !found {
				t.Fatal("no durable retrieval event")
			}
			unrelated := fixture.search(t, "unrelated weather prediction")
			if !unrelated.NoMatch || len(unrelated.Items) != 0 {
				t.Fatal("score 1 bypassed evidence coverage gate")
			}
		})
	}
}

func TestCrossEncoderPipelineFailuresAreSafeAndBounded(t *testing.T) {
	for _, fault := range []string{"unavailable", "overloaded", "authentication", "invalid_response", "timeout", "body_timeout"} {
		t.Run(fault, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Drain the POST body before waiting for disconnect; otherwise the
				// server cannot reliably observe cancellation while body bytes remain.
				var input map[string]any
				_ = json.NewDecoder(r.Body).Decode(&input)
				if fault == "unavailable" {
					http.Error(w, "PRIVATE_PROVIDER_BODY", 503)
					return
				}
				if fault == "overloaded" {
					w.WriteHeader(429)
					return
				}
				if fault == "authentication" {
					w.WriteHeader(401)
					return
				}
				if fault == "invalid_response" {
					_, _ = w.Write([]byte(`[{"index":99,"score":1}]`))
					return
				}
				if strings.HasPrefix(fault, "body_") {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			reranker, err := rag.NewReranker(rag.RerankerConfig{Mode: "tei", BaseURL: server.URL, Model: "fixture-cross-encoder", Revision: "fixture-revision", Timeout: 50 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			fixture := newPipelineRegressionFixtureWithReranker(t, reranker)
			fixture.seedDocument(t)
			failure := fixture.request(t, http.MethodPost, "/api/rag/search", `{"query":"alpha-4242 recovery protocol"}`)
			var response apiErrorResponse
			if json.Unmarshal(failure.Body.Bytes(), &response) != nil {
				t.Fatal("invalid API error")
			}
			code := "reranker_timeout"
			if fault == "unavailable" {
				code = "reranker_unavailable"
			}
			status := 504
			if fault == "unavailable" || fault == "authentication" || fault == "invalid_response" {
				status = 502
			}
			if fault == "authentication" {
				code = "reranker_authentication"
			}
			if fault == "invalid_response" {
				code = "reranker_invalid_response"
			}
			if fault == "overloaded" {
				status, code = 503, "reranker_overloaded"
			}
			if failure.Code != status || response.Code != code || response.Source != "reranker" || strings.Contains(failure.Body.String(), "PRIVATE_PROVIDER_BODY") {
				t.Fatalf("unsafe or misclassified error: %d %s", failure.Code, failure.Body.String())
			}
			// Optional Knowledge failure is retained as rag_error while the Run
			// continues without Knowledge; it is not a heuristic reranker fallback.
			event := fixture.runAgentAndGetRetrievalEvent(t, "alpha-4242 recovery protocol", domain.EventRetrievalCompleted)
			if !strings.Contains(stringValue(event.Payload["rag_error"]), code) || strings.Contains(stringValue(event.Payload["rag_error"]), "PRIVATE_PROVIDER_BODY") || intValue(event.Payload["chunk_count"]) != 0 {
				t.Fatalf("runtime failure not retained: %+v", event)
			}
		})
	}
}
