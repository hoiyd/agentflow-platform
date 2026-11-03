package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// Failure inventory: an interrupted Stage must receive the same input on Resume;
// committed predecessors must not rerun, and the persisted router must not reroute.
func TestCollaborationTailRecoveryPreservesRequests(t *testing.T) {
	for _, interrupted := range []string{"worker", "reviewer", "finalizer", "none"} {
		t.Run(interrupted, func(t *testing.T) {
			var mu sync.Mutex
			inputs := map[string][]string{}
			var order []string
			failed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct{ Role, Content string }
					Stream   bool
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", 400)
					return
				}
				role, input := "", ""
				for _, message := range request.Messages {
					if message.Role == "system" {
						for _, candidate := range []string{"planner", "worker", "reviewer", "finalizer"} {
							if strings.Contains(message.Content, "You are the "+strings.ToUpper(candidate[:1])+candidate[1:]+" collaboration role") {
								role = candidate
							}
						}
					}
					if message.Role == "user" {
						input = message.Content
					}
				}
				mu.Lock()
				defer mu.Unlock()
				if role == "" {
					t.Error("unexpected model request without Stage role")
					http.Error(w, "unexpected role", 400)
					return
				}
				inputs[role] = append(inputs[role], input)
				order = append(order, role)
				if role == interrupted && !failed {
					failed = true
					http.Error(w, `{"error":{"message":"synthetic Stage interruption","type":"invalid_request_error"}}`, 400)
					return
				}
				completion := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": role + " result"}, "finish_reason": "stop"}}}
				if request.Stream {
					stream, err := modelstream.Completion(completion)
					if err != nil {
						t.Error(err)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, stream)
				} else {
					json.NewEncoder(w).Encode(completion)
				}
			}))
			t.Cleanup(server.Close)
			storage := fixturestore.New()
			client := openai.NewClient("fixture", server.URL, "fixture")
			options := RuntimeOptions{Store: storage, RouterMode: RouterModeQuery, EmbeddingClient: newLocalFallbackOpenAIClientForTest()}
			runtime := newRuntimeForTest(options, client)
			conversation, err := storage.CreateConversation("Synthetic collaboration recovery")
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := runtime.PrepareCollaborationRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			drain := func(events <-chan domain.RunEvent, failures <-chan error) error {
				for range events {
				}
				return <-failures
			}
			if err := drain(runtime.RunCollaboration(t.Context(), prepared, "Plan a backend implementation")); err != nil {
				t.Fatal(err)
			}
			err = drain(runtime.ContinueCollaboration(t.Context(), prepared.Run.ID, "approved plan", domain.AgentRoutingRequirements{}))
			if (err != nil) != (interrupted != "none") {
				t.Fatalf("unexpected initial result: %v", err)
			}
			if _, err := storage.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "synthetic interruption"); err != nil {
				t.Fatal(err)
			}
			// A new Runtime cannot rely on the original in-memory route decision.
			restarted := newRuntimeForTest(options, client)
			if err := drain(restarted.ResumeRecoverableCollaboration(t.Context(), prepared.Run.ID)); err != nil {
				t.Fatal(err)
			}
			steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			wantOrder := []string{"planner"}
			for _, role := range []string{"worker", "reviewer", "finalizer"} {
				wantOrder = append(wantOrder, role)
				wantCalls := 1
				if role == interrupted {
					wantOrder = append(wantOrder, role)
					wantCalls = 2
				}
				if len(inputs[role]) != wantCalls {
					t.Fatalf("%s calls=%d want=%d", role, len(inputs[role]), wantCalls)
				}
				if wantCalls == 2 && inputs[role][0] != inputs[role][1] {
					t.Errorf("%s request changed on recovery:\nbefore=%s\nafter=%s", role, inputs[role][0], inputs[role][1])
				}
				completed := 0
				for _, step := range steps {
					if step.Role == role && step.Status == domain.CollaborationStepCompleted && step.Output == role+" result" {
						completed++
					}
				}
				if completed != 1 {
					t.Fatalf("%s durable completions=%d", role, completed)
				}
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("Stage order=%v want=%v", order, wantOrder)
			}
			if !strings.Contains(inputs["worker"][0], "Selection reason:") {
				t.Fatal("fixture did not exercise persisted routing reason")
			}
			t.Logf("run=%s conversation=%s interrupted=%s order=%v checks=identical-inputs,durable-output,no-reroute,no-committed-rerun limitations=local-HTTP-provider,in-memory-store,no-browser", prepared.Run.ID, conversation.ID, interrupted, order)
		})
	}
}
