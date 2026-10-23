package tool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
	"agentflow-platform/apps/api/internal/tool/webcitation"
)

// This is a backend integration gate, not browser E2E or live-model quality
// evidence. The real streaming model adapter, Tool loop, Bindings, Context and
// Postgres run; only provider replies and remote HTTPS sockets are controlled.
func TestWebFetchSearchReadCitationPostgres(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	for _, scenario := range []string{"inline", "artifact", "forged-source"} {
		t.Run(scenario, func(t *testing.T) {
			conversation, err := storage.CreateConversation("Web fetch evidence: " + scenario)
			if err != nil {
				t.Fatal(err)
			}
			limits := domain.RuntimeRunBudget{MaxModelCalls: 5, MaxToolCalls: 5}
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single", RunBudget: &limits, ContextAssembly: contextassembly.DefaultConfig()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var pageHits atomic.Int32
			remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/search" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"results":[{"title":"Retention policy","url":"https://example.com/article","content":"Policy overview; retention details require the article body."}]}`)
					return
				}
				pageHits.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("credentials or browser state reached fetched page")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				padding := ""
				if scenario == "artifact" {
					padding = strings.Repeat("Policy detail. ", 100)
				}
				_, _ = io.WriteString(w, "<main><p>"+padding+"The retention period is exactly 37 days."+padding+padding+padding+padding+padding+"</p><script>must_not_execute()</script></main>")
			}))
			defer remote.Close()
			resolve := func(ctx context.Context, id string) (domain.WebCitation, error) {
				scope := event.ScopeFromContext(ctx)
				if scope.RunID != run.ID {
					return domain.WebCitation{}, fmt.Errorf("wrong Run")
				}
				events, err := storage.ListRunEvents(scope.RunID)
				if err != nil {
					return domain.WebCitation{}, err
				}
				source, ok := webcitation.FromEvents(events).Lookup(id)
				if !ok {
					return domain.WebCitation{}, fmt.Errorf("source not in Run")
				}
				return source, nil
			}
			recorder := event.NewRecorder(storage)
			catalog, err := tool.BuildCatalog(tool.DefaultConfig(), tool.WebBindingsForTest(t, remote, resolve)...)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err = catalog.CloneWith(artifact.NewService(storage, recorder).ToolBindings()...)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Messages []provider.Message `json:"messages"`
					Stream   bool               `json:"stream"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil || !input.Stream {
					t.Error("invalid model request")
					w.WriteHeader(400)
					return
				}
				round := calls.Add(1)
				name, args, answer := "", "", ""
				switch round {
				case 1:
					name, args = "web_search", `{"query":"public retention policy"}`
				case 2:
					last := input.Messages[len(input.Messages)-1]
					if last.Role != "tool" || !strings.Contains(last.Content, `"source_id":"W1"`) || strings.Contains(last.Content, "37 days") {
						t.Error("search fabricated body evidence")
					}
					name, args = "web_fetch", `{"source_id":"W1"}`
					if scenario == "forged-source" {
						args = `{"source_id":"W9"}`
					}
				case 3:
					last := input.Messages[len(input.Messages)-1]
					if scenario == "forged-source" {
						if !strings.Contains(last.Content, "security_scope_invalid") {
							t.Error("forged source was not rejected")
						}
						answer = "No verified body is available [W9]."
					} else if scenario == "artifact" {
						var result tool.ExecutionResult
						if json.Unmarshal([]byte(last.Content), &result) != nil || result.Artifact == nil || !result.Truncated {
							t.Error("body was not governed as an Artifact")
							w.WriteHeader(400)
							return
						}
						name = "artifact_read"
						data, _ := json.Marshal(map[string]any{"artifact_id": result.Artifact.ID, "offset": 0, "limit": 16384})
						args = string(data)
					} else {
						if !strings.Contains(last.Content, "37 days") || strings.Contains(last.Content, "must_not_execute") {
							t.Error("fetched text was not delivered safely")
						}
						answer = "Retention is 37 days [W1]."
					}
				default:
					last := input.Messages[len(input.Messages)-1]
					var read tool.ExecutionResult
					if json.Unmarshal([]byte(last.Content), &read) != nil || read.Truncated || read.Artifact != nil {
						t.Error("reading the Artifact recursively spilled into another Artifact")
					}
					if !strings.Contains(last.Content, "37 days") || !strings.Contains(last.Content, `"complete":true`) {
						t.Error("Artifact body missing from model Context")
					}
					answer = "Retention is 37 days [W1]."
				}
				choice := map[string]any{"role": "assistant", "content": answer}
				finish := "stop"
				if name != "" {
					finish = "tool_calls"
					choice["tool_calls"] = []provider.ToolCall{{ID: fmt.Sprintf("round-%d", round), Type: "function", Function: provider.FunctionCall{Name: name, Arguments: args}}}
				}
				stream, err := modelstream.Completion(map[string]any{"choices": []any{map[string]any{"message": choice, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 8, "total_tokens": 28}})
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, stream)
			}))
			defer model.Close()
			client := openai.NewClientWithTimeout("fixture-only", model.URL, "fixture-model", 5*time.Second)
			client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			ctx = event.WithScope(ctx, event.Scope{RunID: run.ID, ConversationID: conversation.ID, TurnID: "turn-web-fetch"})
			ctx = budget.WithController(ctx, budget.NewTracker(storage, event.StoreSink{Store: storage}, run))
			ctx = contextassembly.WithSession(ctx, contextassembly.Session{Config: contextassembly.DefaultConfig(), Sink: event.StoreSink{Store: storage}, CurrentInput: "Verify the exact retention period from the article body."})
			events, errs := toolloop.Stream(ctx, client, toolloop.Request{Latest: "Verify the exact retention period from the article body.", Catalog: catalog, Trace: provider.ChatTrace{Recorder: recorder, RunID: run.ID}, ExecutorOptions: tool.ExecutorOptions{ArtifactStore: storage, CredentialScopes: []string{tool.TavilyCredentialScope}}, RunEvents: func() ([]domain.RunEvent, error) { return storage.ListRunEvents(run.ID) }})
			var output strings.Builder
			for item := range events {
				if item.Type == "delta" {
					if item.Reset {
						output.Reset()
					}
					output.WriteString(item.Delta)
				}
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			persisted, err := storage.ListRunEvents(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			selected := map[string]bool{}
			for _, item := range persisted {
				if item.Type == domain.EventContextAssembled {
					var manifest domain.ContextManifest
					data, _ := json.Marshal(item.Payload["manifest"])
					_ = json.Unmarshal(data, &manifest)
					selected = map[string]bool{}
					for _, entry := range manifest.Entries {
						if entry.Source == contextassembly.SourceToolResult && entry.Selected && entry.Transformation == "original" && entry.IncludedBytes == entry.OriginalBytes {
							selected[entry.ReferenceID] = true
						}
					}
				}
			}
			citations, invalid := webcitation.FromEvents(persisted).Resolve(output.String(), selected)
			if scenario == "forged-source" {
				if pageHits.Load() != 0 || len(citations) != 0 || len(invalid) != 1 {
					t.Fatalf("forged source escaped: hits=%d citations=%v invalid=%v", pageHits.Load(), citations, invalid)
				}
			} else if pageHits.Load() != 1 || len(citations) != 1 || len(invalid) != 0 || citations[0].SourceID != "W1" {
				t.Fatalf("missing delivered evidence: output=%s hits=%d citations=%v invalid=%v", output.String(), pageHits.Load(), citations, invalid)
			}
			if len(citations) > 0 {
				for _, item := range persisted {
					if item.ID == citations[0].ToolEventID && item.Payload["tool_name"] != "web_fetch" && item.Payload["tool_name"] != "artifact_read" {
						t.Fatal("citation points only to search, not delivered body")
					}
				}
			}
			// A new adapter reads the same committed events and immutable content.
			reopened, err := store.NewPostgresStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			reloaded, err := reopened.ListRunEvents(run.ID)
			if err != nil || len(reloaded) != len(persisted) {
				t.Fatalf("reload: %v", err)
			}
			reloadedCitations, _ := webcitation.FromEvents(reloaded).Resolve(output.String(), selected)
			if len(reloadedCitations) != len(citations) {
				t.Fatal("citation evidence lost after reconnect")
			}
			revision, _ := exec.Command("git", "rev-parse", "HEAD").Output()
			status, _ := exec.Command("git", "status", "--porcelain", "--untracked-files=no").Output()
			report := map[string]any{
				"schema_version": "web-fetch-evidence-v1", "scenario": scenario, "test": t.Name(),
				"git_revision": strings.TrimSpace(string(revision)), "tracked_changes_present": len(status) > 0,
				"run_id": run.ID, "conversation_id": conversation.ID, "mode": "single-shared-tool-loop",
				"input": "Verify the exact retention period from the article body.", "model": "fixture-model",
				"budget": limits, "context_assembly": contextassembly.DefaultConfig(), "tool_definitions": catalog.Definitions(),
				"model_calls": calls.Load(), "page_requests": pageHits.Load(), "selected_tool_calls": selected,
				"citations": citations, "invalid_citation_ids": invalid, "events": len(reloaded), "persistence": "disposable-postgres-reopened",
				"limitations": []string{"deterministic provider; not live model quality", "HTTPS socket is a controlled fixture; public Dial policy is tested separately", "shared Tool loop, not complete Multi/Loop orchestration", "uncommitted working-tree changes are not a retained release revision"},
			}
			encoded, _ := json.MarshalIndent(report, "", "  ")
			t.Logf("WEB_FETCH_EVIDENCE %s", encoded)
			if directory := os.Getenv("EVALUATION_REPORT_DIR"); directory != "" {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "web-fetch-"+scenario+".json"), encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
