package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// A disabled source must not reach any Stage's model input, and an enabled
// source must reach it. With both off there must be no retrieval embedding.
// Recovery must honor the snapshot even when both live settings are reversed.
func TestProductionBoundedLoopContextPolicy(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	const (
		task            = "Summarize the ROLLBACK-742 recovery procedure."
		memoryMarker    = "MEMORY-62-FIXTURE"
		knowledgeMarker = "KNOWLEDGE-62-FIXTURE"
	)
	type modelInput struct {
		prompt                        string
		memory, knowledge, searchTool bool
	}
	var mu sync.Mutex
	var inputs []modelInput
	var embeddings int
	fixture := newBrowserProvider()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			mu.Lock()
			embeddings++
			mu.Unlock()
			fixture.respond(w, r)
			return
		}
		var req browserModelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		prompt := req.Messages[0].Content
		answer := "Fixture response."
		if strings.Contains(prompt, "in a bounded autonomous agent loop") {
			seen := modelInput{prompt: prompt}
			for _, message := range req.Messages {
				seen.memory = seen.memory || strings.Contains(message.Content, memoryMarker)
				seen.knowledge = seen.knowledge || strings.Contains(message.Content, knowledgeMarker)
			}
			for _, definition := range req.Tools {
				seen.searchTool = seen.searchTool || definition.Function.Name == domain.KnowledgeSearchToolName
			}
			mu.Lock()
			inputs = append(inputs, seen)
			mu.Unlock()
			if strings.Contains(prompt, "Decide stage") {
				answer = `{"decision":"stop","reason":"verified","final_answer":"Fixture complete."}`
			}
		}
		response := browserCompletion(answer, "stop", nil, nil)
		if req.Stream {
			stream, err := modelstream.Completion(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(provider.Close)
	cfg := durableTestConfig(t, root, database, routing.RouteConfig{ID: "loop-context", BaseURL: provider.URL, RequestTimeoutSeconds: 10})
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeDurableTestApplication(application); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	s, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	workspace, err := s.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	scoped := s.ForWorkspace(domain.NewWorkspaceScope(workspace))
	vector := make([]float64, 1536)
	vector[0] = 1
	if _, err = s.CreateMemory(domain.Memory{WorkspaceID: workspace, Kind: "fact", Content: memoryMarker + " ROLLBACK-742 recovery preserves the latest checkpoint."}, domain.MemoryEmbedding{Provider: "openai_compatible", Model: "fixture-embedding", Embedding: vector}); err != nil {
		t.Fatal(err)
	}
	durableTestRequest(t, server, "POST", "/api/documents", map[string]string{"title": "ROLLBACK-742 recovery procedure", "content": knowledgeMarker + " ROLLBACK-742 recovery procedure restores the latest durable checkpoint."}, 201, nil)

	for _, tc := range []struct {
		name              string
		memory, knowledge bool
	}{
		{"both_off", false, false}, {"memory_only", true, false}, {"knowledge_only", false, true}, {"both_on", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := scoped.CreateAgent(domain.Agent{Name: tc.name, SystemPrompt: "Summarize checkpoint evidence.", Tools: []string{domain.KnowledgeSearchToolName}, MemoryEnabled: tc.memory, RetrievalEnabled: tc.knowledge})
			if err != nil {
				t.Fatal(err)
			}
			verify := func(t *testing.T, conversationID, path string, body any) domain.Run {
				t.Helper()
				mu.Lock()
				start, embedStart := len(inputs), embeddings
				mu.Unlock()
				stream := durableTestRequest(t, server, "POST", path, body, 200, nil)
				runs, err := s.ListRunsByWorkspace(workspace)
				if err != nil {
					t.Fatal(err)
				}
				var run domain.Run
				for _, item := range runs {
					if item.ConversationID == conversationID {
						run = item
						break
					}
				}
				if strings.Contains(stream, "event: error") || run.Status != domain.RunCompleted || run.RuntimeSnapshot == nil {
					t.Fatalf("Loop did not complete: status=%s stream=%s", run.Status, stream)
				}
				frozen := run.RuntimeSnapshot.Agent
				if frozen.MemoryEnabled != tc.memory || frozen.RetrievalEnabled != tc.knowledge {
					t.Fatalf("unexpected frozen switches: memory=%t knowledge=%t", frozen.MemoryEnabled, frozen.RetrievalEnabled)
				}
				mu.Lock()
				defer mu.Unlock()
				stages := []string{"Observe", "Plan", "Act", "Review", "Decide"}
				if got := len(inputs) - start; got != len(stages) {
					t.Fatalf("model Stage requests=%d, want %d", got, len(stages))
				}
				for i, stage := range stages {
					in := inputs[start+i]
					if !strings.Contains(in.prompt, "the "+stage+" stage") || in.memory != tc.memory || in.knowledge != tc.knowledge || in.searchTool != (stage == "Act") {
						t.Errorf("%s: memory=%t knowledge=%t explicit search Tool=%t", stage, in.memory, in.knowledge, in.searchTool)
					}
				}
				if !tc.memory && !tc.knowledge && embeddings != embedStart {
					t.Errorf("both disabled: got %d unexpected embedding requests", embeddings-embedStart)
				}
				t.Logf("run=%s memory=%t knowledge=%t model_stages=%d embeddings=%d", run.ID, frozen.MemoryEnabled, frozen.RetrievalEnabled, len(inputs)-start, embeddings-embedStart)
				return run
			}
			conv, err := s.CreateConversationInWorkspace(workspace, "Loop context policy")
			if err != nil {
				t.Fatal(err)
			}
			initial := verify(t, conv.ID, "/api/chat", map[string]string{"conversation_id": conv.ID, "agent_id": agent.ID, "message": task, "mode": "autonomous"})
			t.Run("resume_after_live_edit", func(t *testing.T) {
				agent.MemoryEnabled, agent.RetrievalEnabled = !tc.memory, !tc.knowledge
				if _, err := scoped.UpdateAgent(agent); err != nil {
					t.Fatal(err)
				}
				conv, err := s.CreateConversationInWorkspace(workspace, "Loop recovery policy")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.AddMessage(conv.ID, "user", task); err != nil {
					t.Fatal(err)
				}
				run, err := s.CreateRunWithContract(agent.ID, conv.ID, *initial.RuntimeSnapshot, nil)
				if err != nil {
					t.Fatal(err)
				}
				// Seed a durable observation at the crash boundary; the HTTP Resume
				// runs a fresh iteration using the persisted snapshot.
				step, err := s.CreateCollaborationStep(domain.CollaborationStep{RunID: run.ID, ConversationID: conv.ID, AgentID: agent.ID, Role: "observe", Status: domain.CollaborationStepCompleted, Input: "User task:\n" + task + "\n\nCurrent state: none", Output: "Saved observation."})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := checkpoint.NewInternalProvider(s).RecordStageTransition(t.Context(), step, domain.EventStageCompleted); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UpdateRunStatus(run.ID, domain.RunFailedRecoverable, "fixture crash boundary"); err != nil {
					t.Fatal(err)
				}
				resumed := verify(t, conv.ID, "/api/runs/"+run.ID+"/resume", map[string]string{"user_input": ""})
				if resumed.ID != run.ID {
					t.Fatalf("Resume created a different Run: %s", resumed.ID)
				}
			})
		})
	}
}
