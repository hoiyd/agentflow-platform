package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/knowledge"
	"agentflow-platform/apps/api/internal/rag"
)

func TestScopedKnowledgeToolsSearchReadCitedAnswerAcrossModes(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/embeddings") {
					var request struct {
						Input string `json:"input"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					vector := []float64{1, 0}
					if strings.Contains(request.Input, "preserves durable state") || request.Input == "gamma-7733 checksum" {
						vector = []float64{0, 1}
					}
					writeJSON(w, 200, map[string]any{"model": "knowledge-fixture-embedding", "data": []any{map[string]any{"embedding": vector}}})
					return
				}
				var request struct {
					Messages []provider.Message `json:"messages"`
					Tools    []any              `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				content, reason := "Inspect the gamma-7733 checksum procedure.", "stop"
				var calls []provider.ToolCall
				observations := []provider.Message{}
				for _, message := range request.Messages {
					if message.Role == "tool" {
						observations = append(observations, message)
					}
				}
				if len(request.Tools) > 0 {
					name, args := "knowledge_search", `{"query":"gamma-7733 checksum","max_results":1}`
					if len(observations) == 1 {
						var envelope struct {
							Result domain.KnowledgeToolSearchResult `json:"result"`
						}
						if err := json.Unmarshal([]byte(observations[0].Content), &envelope); err != nil || len(envelope.Result.Items) != 1 {
							t.Errorf("search observation=%s err=%v", observations[0].Content, err)
							w.WriteHeader(400)
							return
						}
						name, args = "knowledge_read", fmt.Sprintf(`{"reference":%q}`, envelope.Result.Items[0].Reference)
					}
					if len(observations) < 2 {
						content, reason = "", "tool_calls"
						calls = []provider.ToolCall{{ID: "fixture-call", Type: "function", Function: provider.FunctionCall{Name: name, Arguments: args}}}
					} else {
						if !strings.Contains(observations[1].Content, "preserves durable state") {
							t.Error("read evidence was not delivered")
						}
						content = "Gamma-7733 preserves durable state [S2]."
					}
				} else if strings.Contains(fmt.Sprint(request.Messages), "<untrusted_knowledge_read>") {
					content = "Gamma-7733 preserves durable state [S2]."
				}
				if len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "Decide stage") {
					content = `{"decision":"stop","reason":"evidence read","final_answer":"Gamma-7733 preserves durable state [S2]."}`
				}
				writeJSON(w, 200, map[string]any{"model": "knowledge-fixture-model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content, "tool_calls": calls}, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 10, "total_tokens": 40}})
			}))
			t.Cleanup(server.Close)
			dependencies := completeHandlerDependencies(t)
			storage := fullStoreForTest(t, dependencies)
			if _, err := storage.CreateAgent(domain.Agent{Name: "Knowledge reader", Description: "Inspect release manuals and recovery procedures.", SystemPrompt: "Search then read Knowledge evidence.", Tools: []string{}, RetrievalEnabled: true, RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"alpha-4242", "release", "manual", "inspect", "recovery", "procedure"}}}); err != nil {
				t.Fatal(err)
			}
			client := openai.NewClientWithTimeoutAndEmbeddingModel("fixture-not-a-secret", server.URL, server.URL, "knowledge-fixture-model", "knowledge-fixture-embedding", 2, 2*time.Second)
			client.SetRequestRecorder(capture.NewRecorder(storage, capture.Options{Mode: domain.ModelRequestCaptureFull}))
			base := knowledge.NewKnowledgeBase(storage, client)
			for _, request := range []domain.DocumentIngestRequest{
				{WorkspaceID: pipelineRegressionWorkspace, Title: "Release manual", Content: "The alpha-4242 release manual references gamma-7733 for the checksum procedure.", Version: "v1"},
				{WorkspaceID: pipelineRegressionWorkspace, Title: "Checksum protocol", Content: "Gamma-7733 checksum preserves durable state.", Version: "v2"},
				{WorkspaceID: "workspace-foreign", Title: "Foreign checksum", Content: "Gamma-7733 checksum preserves durable state with FOREIGN_SECRET.", Version: "v1"},
			} {
				if _, err := base.Ingest(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			}
			dependencies.Knowledge = base
			dependencies.AgentRuntime = agentpkg.NewRuntime(agentpkg.RuntimeOptions{Store: storage, ModelClient: client, EmbeddingClient: client, Knowledge: base, KnowledgeRetriever: rag.NewRetrievalPipeline(storage), RouterMode: agentpkg.RouterModeQuery, Autonomous: agentpkg.AutonomousLimits{MaxIterations: 1}, RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 16, MaxToolCalls: 4, MaxRuntimeMS: 20000}})
			handler, err := NewHandler(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			fixture := &pipelineRegressionFixture{routes: handler.Routes(), store: storage}
			replay := fixture.runModeAndReplay(t, "Explain the alpha-4242 release manual.", mode)
			if replay.Run.Status != domain.RunCompleted {
				t.Fatalf("run=%#v", replay.Run)
			}
			var cited *domain.Message
			for index := range replay.Messages {
				if strings.Contains(replay.Messages[index].Content, "[S2]") {
					cited = &replay.Messages[index]
				}
			}
			if cited == nil || len(cited.Citations) != 1 {
				t.Fatalf("messages=%#v", replay.Messages)
			}
			source := cited.Citations[0]
			if source.SourceID != "S2" || source.DocumentTitle != "Checksum protocol" || source.ToolEventID == "" || source.ToolCallID == "" || source.RunID != replay.Run.ID {
				t.Fatalf("citation=%#v", source)
			}
			readEvent, found := domain.RunEvent{}, false
			for _, item := range replay.RunEvents {
				if item.ID == source.ToolEventID {
					readEvent, found = item, true
				}
				if item.Type == domain.EventRetrievalCompleted && item.Payload["query"] != "Explain the alpha-4242 release manual." {
					t.Fatalf("automatic query boundary changed: %#v", item.Payload)
				}
			}
			if !found || readEvent.Payload["tool_name"] != "knowledge_read" {
				t.Fatal("citation cannot trace to read event")
			}
			if err := eventpkg.ValidateLifecycle(replay.RunEvents); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprint(replay.RunEvents), "FOREIGN_SECRET") {
				t.Fatal("foreign document entered Run")
			}
			if _, citations, invalid, err := handler.resolveRunCitations(storage.ForWorkspace(domain.NewWorkspaceScope(pipelineRegressionWorkspace)), replay.Run.ID, "[S2] [S99]"); err != nil || len(citations) != 1 || len(invalid) != 1 || invalid[0] != "S99" {
				t.Fatalf("resolution citations=%#v invalid=%#v err=%v", citations, invalid, err)
			}
			records, err := storage.ListModelRequestRecords(replay.Run.ID)
			if err != nil || len(records) < 3 {
				t.Fatalf("capture records=%d err=%v", len(records), err)
			}
			evidence, _ := json.Marshal(map[string]any{"mode": mode, "run_id": replay.Run.ID, "input": "Explain the alpha-4242 release manual.", "tool_query": "gamma-7733 checksum", "workspace_id": pipelineRegressionWorkspace, "source": source, "model_calls": len(records), "outcome": "completed", "limitations": "deterministic local provider and fixture store; no live quality or authenticated ACL claim"})
			t.Logf("knowledge_tool_evidence=%s", evidence)
		})
	}
}
