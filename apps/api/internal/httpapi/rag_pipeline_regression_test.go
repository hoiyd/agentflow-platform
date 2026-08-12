package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
)

const pipelineRegressionWorkspace = "workspace-pipeline-regression"

type pipelineRegressionFixture struct {
	routes         http.Handler
	dependencies   Dependencies
	knowledge      *knowledge.KnowledgeBase
	failEmbeddings atomic.Bool
}

func TestRAGPipelineAPIAndAgentRuntimeRemainConsistent(t *testing.T) {
	t.Run("relevant result", func(t *testing.T) {
		fixture := newPipelineRegressionFixture(t)
		fixture.seedDocument(t)

		query := "alpha-4242 recovery protocol"
		apiResponse := fixture.search(t, query)
		runtimeEvent := fixture.runAgentAndGetRetrievalEvent(t, query, domain.EventRetrievalCompleted)

		if apiResponse.NoMatch || len(apiResponse.Items) == 0 || len(apiResponse.ContextItems) == 0 {
			t.Fatalf("expected API pipeline match, got %#v", apiResponse)
		}
		assertPipelineSuccessConsistent(t, apiResponse, runtimeEvent.Payload)
	})

	t.Run("no answer", func(t *testing.T) {
		fixture := newPipelineRegressionFixture(t)
		fixture.seedDocument(t)

		query := "zebra astronomy portfolio"
		apiResponse := fixture.search(t, query)
		runtimeEvent := fixture.runAgentAndGetRetrievalEvent(t, query, domain.EventRetrievalCompleted)

		if !apiResponse.NoMatch || apiResponse.Reason == "" || len(apiResponse.Items) != 0 || len(apiResponse.ContextItems) != 0 {
			t.Fatalf("expected API no-match response, got %#v", apiResponse)
		}
		assertPipelineSuccessConsistent(t, apiResponse, runtimeEvent.Payload)
	})

	t.Run("embedding failure", func(t *testing.T) {
		fixture := newPipelineRegressionFixture(t)
		fixture.seedDocument(t)
		fixture.failEmbeddings.Store(true)

		query := "alpha-4242 recovery protocol"
		apiError := fixture.searchError(t, query)
		runtimeEvent := fixture.runAgentAndGetRetrievalEvent(t, query, domain.EventRetrievalFailed)

		runtimeError, _ := runtimeEvent.Payload["error"].(string)
		if runtimeError == "" || runtimeError != apiError {
			t.Fatalf("embedding error mismatch: api=%q runtime=%q payload=%#v", apiError, runtimeError, runtimeEvent.Payload)
		}
		if runtimeEvent.Payload["error_source"] != "model_provider" || runtimeEvent.Payload["error_category"] != "availability" {
			t.Fatalf("expected classified embedding failure, got %#v", runtimeEvent.Payload)
		}
	})
}

func newPipelineRegressionFixture(t *testing.T) *pipelineRegressionFixture {
	t.Helper()
	fixture := &pipelineRegressionFixture{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/embeddings":
			if fixture.failEmbeddings.Load() {
				http.Error(w, `{"error":{"message":"embedding service unavailable","code":"provider_unavailable"}}`, http.StatusServiceUnavailable)
				return
			}
			var request struct {
				Input string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "invalid embedding request", http.StatusBadRequest)
				return
			}
			vector := []float64{1, 0}
			if strings.Contains(strings.ToLower(request.Input), "zebra astronomy portfolio") {
				vector = []float64{0, 1}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"model": "pipeline-regression-embedding",
				"data":  []map[string]any{{"embedding": vector}},
			})
		case "/v1/chat/completions":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "invalid chat request", http.StatusBadRequest)
				return
			}
			if stream, _ := request["stream"].(bool); stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Pipeline regression answer.\"}}]}\n\ndata: [DONE]\n\n"))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"model":   "pipeline-regression-chat",
				"choices": []map[string]any{{"message": map[string]any{"content": "Pipeline regression"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)

	client := openai.NewClientWithTimeoutAndEmbeddingModel(
		"pipeline-regression-key",
		provider.URL+"/v1",
		provider.URL+"/v1",
		"pipeline-regression-chat",
		"pipeline-regression-embedding",
		2,
		time.Second,
	)
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})

	dependencies := completeHandlerDependencies(t)
	pipeline := rag.NewRetrievalPipeline(dependencies.Store)
	knowledgeBase := knowledge.NewKnowledgeBaseWithRetriever(dependencies.Store, client, pipeline)
	dependencies.ModelClient = client
	dependencies.Knowledge = knowledgeBase
	dependencies.AgentRuntime = agentpkg.NewRuntime(agentpkg.RuntimeOptions{
		Store:              dependencies.Store,
		ModelClient:        client,
		KnowledgeRetriever: pipeline,
	})
	handler, err := NewHandler(dependencies)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	fixture.routes = handler.Routes()
	fixture.dependencies = dependencies
	fixture.knowledge = knowledgeBase
	return fixture
}

func (f *pipelineRegressionFixture) seedDocument(t *testing.T) {
	t.Helper()
	_, err := f.knowledge.Ingest(context.Background(), domain.DocumentIngestRequest{
		WorkspaceID: pipelineRegressionWorkspace,
		Title:       "Pipeline Recovery Runbook",
		Content:     "The alpha-4242 recovery protocol requires restarting the coordinator and validating its trace.",
		SourceType:  "text",
	})
	if err != nil {
		t.Fatalf("seed pipeline document: %v", err)
	}
}

func (f *pipelineRegressionFixture) search(t *testing.T, query string) domain.DocumentSearchResponse {
	t.Helper()
	recorder := f.request(t, http.MethodPost, "/api/rag/search", `{"query":`+quotedJSON(t, query)+`,"limit":5,"knowledge_context_max_tokens":16000}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("RAG search failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response domain.DocumentSearchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode RAG search response: %v", err)
	}
	return response
}

func (f *pipelineRegressionFixture) searchError(t *testing.T, query string) string {
	t.Helper()
	recorder := f.request(t, http.MethodPost, "/api/rag/search", `{"query":`+quotedJSON(t, query)+`,"limit":5}`)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected embedding failure status 502, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Error == "" {
		t.Fatalf("decode RAG search error: response=%#v err=%v", response, err)
	}
	return response.Error
}

func (f *pipelineRegressionFixture) runAgentAndGetRetrievalEvent(t *testing.T, query string, eventType domain.RunEventType) domain.RunEvent {
	t.Helper()
	chat := f.request(t, http.MethodPost, "/api/chat", `{"message":`+quotedJSON(t, query)+`,"mode":"single"}`)
	if chat.Code != http.StatusOK || !strings.Contains(chat.Body.String(), "event: done") {
		t.Fatalf("Agent Runtime chat failed: status=%d body=%s", chat.Code, chat.Body.String())
	}
	runs, err := f.dependencies.Store.ListRunsByWorkspace(pipelineRegressionWorkspace)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list Agent Runtime runs: runs=%#v err=%v", runs, err)
	}

	replay := f.request(t, http.MethodGet, "/api/runs/"+runs[0].ID+"/replay", "")
	if replay.Code != http.StatusOK {
		t.Fatalf("get Agent Runtime replay: status=%d body=%s", replay.Code, replay.Body.String())
	}
	var decoded domain.RunReplay
	if err := json.Unmarshal(replay.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode Agent Runtime replay: %v", err)
	}
	for _, event := range decoded.RunEvents {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("expected %s in Agent Runtime replay, got %#v", eventType, decoded.RunEvents)
	return domain.RunEvent{}
}

func (f *pipelineRegressionFixture) request(t *testing.T, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(WorkspaceHeader, pipelineRegressionWorkspace)
	f.routes.ServeHTTP(recorder, request)
	return recorder
}

func assertPipelineSuccessConsistent(t *testing.T, api domain.DocumentSearchResponse, runtime map[string]any) {
	t.Helper()
	if runtime["rag_no_match"] != api.NoMatch || stringValue(runtime["rag_no_match_reason"]) != api.Reason {
		t.Fatalf("no-match mismatch: api=%v/%q runtime=%#v/%q", api.NoMatch, api.Reason, runtime["rag_no_match"], runtime["rag_no_match_reason"])
	}
	if intValue(runtime["matched_chunk_count"]) != len(api.Items) || intValue(runtime["chunk_count"]) != len(api.ContextItems) {
		t.Fatalf("retrieval count mismatch: api matched/context=%d/%d runtime=%#v/%#v", len(api.Items), len(api.ContextItems), runtime["matched_chunk_count"], runtime["chunk_count"])
	}
	if stringValue(runtime["embedding_provider"]) != api.Embedding.Provider || stringValue(runtime["embedding_model"]) != api.Embedding.Model || intValue(runtime["embedding_dimensions"]) != api.Embedding.Dimensions || boolValue(runtime["embedding_estimated"]) != api.Embedding.Estimated {
		t.Fatalf("embedding mismatch: api=%#v runtime provider/model/dimensions/estimated=%#v/%#v/%#v/%#v", api.Embedding, runtime["embedding_provider"], runtime["embedding_model"], runtime["embedding_dimensions"], runtime["embedding_estimated"])
	}
	if got, want := traceChunkIDs(runtime["matched_chunks"]), resultChunkIDs(api.Items); !equalStrings(got, want) {
		t.Fatalf("matched candidate mismatch: api=%v runtime=%v", want, got)
	}
	if got, want := traceChunkIDs(runtime["retrieved_chunks"]), resultChunkIDs(api.ContextItems); !equalStrings(got, want) {
		t.Fatalf("selected context mismatch: api=%v runtime=%v", want, got)
	}
	assertJSONFieldEqual(t, "fusion", api.Fusion, runtime["fusion"])
	assertJSONFieldEqual(t, "reranker", api.Reranker, runtime["reranker"])
	assertJSONFieldEqual(t, "relevance_gate", api.RelevanceGate, runtime["relevance_gate"])
	assertJSONFieldEqual(t, "knowledge_security", api.Security, runtime["knowledge_security"])
	assertJSONFieldEqual(t, "context_selection", api.ContextSelection, runtime["context_selection"])
	if got, want := traceSourceIDs(runtime["citation_sources"]), citationSourceIDsForRegression(api.CitationSources); !equalStrings(got, want) {
		t.Fatalf("citation source mismatch: api=%v runtime=%v", want, got)
	}
}

func traceChunkIDs(value any) []string {
	items, _ := value.([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		fields, _ := item.(map[string]any)
		if id, _ := fields["chunk_id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func resultChunkIDs(items []domain.RetrievedDocumentChunk) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Chunk.ID)
	}
	sort.Strings(ids)
	return ids
}

func traceSourceIDs(value any) []string {
	items, _ := value.([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		fields, _ := item.(map[string]any)
		if id, _ := fields["source_id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func citationSourceIDsForRegression(items []domain.RAGCitation) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.SourceID)
	}
	sort.Strings(ids)
	return ids
}

func assertJSONFieldEqual(t *testing.T, name string, want any, got any) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal expected %s: %v", name, err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal runtime %s: %v", name, err)
	}
	var wantValue any
	if err := json.Unmarshal(wantJSON, &wantValue); err != nil {
		t.Fatalf("normalize expected %s: %v", name, err)
	}
	var gotValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatalf("normalize runtime %s: %v", name, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("%s mismatch: api=%s runtime=%s", name, wantJSON, gotJSON)
	}
}

func quotedJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode JSON string: %v", err)
	}
	return string(encoded)
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
