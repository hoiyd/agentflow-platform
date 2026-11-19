package app

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestProductionMemoryRecallNoMatch(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("MEMORY_RECALL_QWEN3_GATE_ENABLED", "true")
	const (
		modelID       = "qwen3-embedding"
		supportedFact = "PAY-742 settled by restoring the prior build."
		wrongFact     = "PAY-7420 settled by restarting the cache."
		otherModel    = "other-embedding"
		paraphrase    = "What was the recovery step for the payments incident?"
		unrelated     = "What's the weather in Shanghai?"
		otherQuery    = "How much rain is forecast in Beijing?"
	)
	vector := func(score float64) []float64 {
		v := make([]float64, 1536)
		v[0], v[1] = score, math.Sqrt(1-score*score)
		return v
	}
	var mu sync.Mutex
	var prompts []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/embed" {
			var req struct{ Input string }
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				http.Error(w, "invalid embedding input", http.StatusBadRequest)
				return
			}
			v, model := vector(1), modelID
			if req.Input == paraphrase {
				v[0], v[2] = 0, 1
			}
			if req.Input == unrelated || req.Input == otherQuery {
				v = vector(0.58)
			}
			if req.Input == otherQuery {
				model = otherModel
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": model, "embeddings": [][]float64{v}})
			return
		}
		var req browserModelRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		var prompt strings.Builder
		for _, input := range req.Messages {
			prompt.WriteString(input.Content)
			prompt.WriteByte('\n')
		}
		mu.Lock()
		prompts = append(prompts, prompt.String())
		mu.Unlock()
		response := browserCompletion("Fixture complete.", "stop", nil, nil)
		if req.Stream {
			stream, err := modelstream.Completion(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, stream)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(provider.Close)
	cfg := durableTestConfig(t, root, database, routing.RouteConfig{ID: "recall-fixture", BaseURL: provider.URL, RequestTimeoutSeconds: 10})
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel = provider.URL+"/api/embed", modelID
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
	seed := func(content, model string, v []float64, createdAt time.Time) {
		t.Helper()
		if _, err := s.CreateMemory(domain.Memory{WorkspaceID: workspace, Kind: "fact", Content: content, CreatedAt: createdAt},
			domain.MemoryEmbedding{Provider: "ollama", Model: model, Embedding: v}); err != nil {
			t.Fatal(err)
		}
	}
	// The closer, recent wrong IDs would crowd the correct fact out of Top-K.
	now := time.Now().UTC()
	v := vector(0.72)
	v[2], v[1] = v[1], 0
	seed(supportedFact, modelID, v, now.Add(-60*24*time.Hour))
	for i := range 6 {
		seed(fmt.Sprintf("%s Example %d.", wrongFact, i), modelID, vector(1), now)
	}
	seed("Other profile keeps its existing recall behavior.", otherModel, vector(1), now)

	agent, err := s.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{
		Name: "Memory recall fixture", SystemPrompt: "Answer the question.", MemoryEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, want string
	}{
		{"related_below_unfiltered_top_k", "How do we recover PAY-742?", supportedFact},
		{"related_paraphrase_without_identifier", paraphrase, supportedFact},
		{"wrong_identifier", "How do we recover PAY-743?", ""},
		{"unrelated_with_recency_boost", unrelated, ""},
		{"another_profile_unchanged", otherQuery, "Other profile keeps its existing recall behavior."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, err := s.CreateConversationInWorkspace(workspace, "Memory recall evidence")
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			first := len(prompts)
			mu.Unlock()
			stream := durableTestRequest(t, server, http.MethodPost, "/api/chat", domain.ChatRequest{
				ConversationID: conv.ID, AgentID: agent.ID, Message: tc.query, Mode: "single",
			}, http.StatusOK, nil)
			runID, _ := durableTestDone(t, stream)["run_id"].(string)
			var replay domain.RunReplay
			durableTestRequest(t, server, http.MethodGet, "/api/runs/"+runID+"/replay", nil, http.StatusOK, &replay)
			if replay.Run.Status != domain.RunCompleted {
				t.Fatalf("ordinary response did not complete: %s", replay.Run.Status)
			}
			mu.Lock()
			inputs := append([]string(nil), prompts[first:]...)
			mu.Unlock()
			if len(inputs) == 0 {
				t.Fatal("no outgoing model input observed")
			}
			for _, input := range inputs {
				hasMemory := strings.Contains(input, `<untrusted_memory_context policy="`)
				if hasMemory != (tc.want != "") || !strings.Contains(input, tc.want) || strings.Contains(input, wrongFact) {
					t.Errorf("unexpected Memory in real model request: %s", input)
				}
			}
			completed := false
			for _, event := range replay.RunEvents {
				switch event.Type {
				case domain.EventMemoryRecallFailed:
					t.Fatal("no-match recorded as a Memory recall error")
				case domain.EventRetrievalCompleted:
					if tc.want == "" && (event.Payload["memory_count"] != float64(0) || event.Payload["memory_no_match"] != true) {
						t.Fatalf("missing no-match evidence: %+v", event.Payload)
					}
					completed = true
				}
			}
			if !completed {
				t.Fatal("missing retrieval completion event")
			}
		})
	}

	var results []domain.RetrievedMemory
	durableTestRequest(t, server, http.MethodPost, "/api/memories/search",
		domain.MemorySearch{Query: unrelated}, http.StatusOK, &results)
	if len(results) != 5 || results[0].Similarity >= 0.60 || results[0].Score <= 0.60 || !strings.Contains(results[0].Memory.Content, wrongFact) {
		t.Fatalf("manual search should still return ranked, below-gate candidates: %+v", results)
	}
}
