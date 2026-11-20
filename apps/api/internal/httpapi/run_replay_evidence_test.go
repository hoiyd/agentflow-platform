package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Failure inventory: later turns and Agent edits must not rewrite Run evidence;
// missing or invalid receipts must not borrow a neighboring answer or Stage.
func TestReplayKeepsRunFactsAcrossConversationTurns(t *testing.T) {
	contract := loadResourceContract(t)
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			dependencies := completeHandlerDependencies(t)
			storage := fullStoreForTest(t, dependencies)
			agent, err := storage.CreateAgent(domain.Agent{Name: "Frozen persona", SystemPrompt: "Original instructions", Executor: domain.DefaultAgentExecutor,
				RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"task", "first", "second"}, TaskExamples: []string{"First task", "Second task"}}})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(dependencies)
			if err != nil {
				t.Fatal(err)
			}
			routes := handler.Routes()
			conversation := ""
			var runs []domain.Run
			for _, question := range []string{"First task", "Second task"} {
				body, _ := json.Marshal(domain.ChatRequest{Message: question, AgentID: agent.ID, ConversationID: conversation, Mode: mode})
				response := performJSONRequest(t, routes, http.MethodPost, "/api/chat", string(body))
				if response.Code != 200 {
					t.Fatalf("chat: %d %s", response.Code, response.Body.String())
				}
				items, err := storage.ListRunsByWorkspace(domain.DefaultWorkspaceID)
				if err != nil || len(items) == 0 {
					t.Fatalf("runs: %v %v", items, err)
				}
				run := items[0]
				conversation = run.ConversationID
				if mode == "multi_agent" {
					response = performJSONRequest(t, routes, http.MethodPost, "/api/runs/"+run.ID+"/continue", `{"plan":"Execute the task."}`)
					if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"completed"`) {
						t.Fatalf("continue: %s", response.Body.String())
					}
				}
				current, found, err := storage.GetRun(run.ID)
				if err != nil || !found || current.Status != domain.RunCompleted {
					t.Fatalf("Run did not complete: %+v %v; %s", current, err, response.Body.String())
				}
				runs = append(runs, run)
			}
			for _, role := range []string{"user", "assistant"} {
				if _, err := storage.AddMessage(conversation, role, "UNRELATED_LATER_MESSAGE"); err != nil {
					t.Fatal(err)
				}
			}
			agent.Name, agent.SystemPrompt = "Edited persona", "EDITED_INSTRUCTIONS"
			if _, err := storage.UpdateAgent(agent); err != nil {
				t.Fatal(err)
			}
			previousAnswerID := ""
			for index, run := range runs {
				response := performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+run.ID+"/replay", "")
				var replay domain.RunReplay
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &replay) != nil {
					t.Fatalf("replay: %s", response.Body.String())
				}
				contract.validate(t, "/components/schemas/RunReplay", response.Body.Bytes())
				want := []string{"First task", "Second task"}[index]
				var answer domain.Message
				if replay.Candidate != nil {
					for _, message := range replay.Messages {
						if message.ID == replay.Candidate.MessageID {
							answer = message
						}
					}
				}
				if replay.Candidate == nil || replay.Candidate.Question != want || answer.Role != "assistant" ||
					answer.ID == previousAnswerID || answer.Content == "" || answer.Content == "UNRELATED_LATER_MESSAGE" || replay.RuntimeSnapshot == nil ||
					!strings.Contains(response.Body.String(), "Original instructions") || strings.Contains(response.Body.String(), "EDITED_INSTRUCTIONS") {
					t.Fatalf("Run facts changed: candidate=%+v answer=%+v; %s", replay.Candidate, answer, response.Body.String())
				}
				previousAnswerID = answer.ID
			}
			if err := storage.ArchiveAgent(agent.ID); err != nil {
				t.Fatal(err)
			}
			response := performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+runs[0].ID+"/replay", "")
			if response.Code != 200 || !strings.Contains(response.Body.String(), "Frozen persona") {
				t.Fatalf("archived Agent: %s", response.Body.String())
			}
			if _, err := storage.CreateRunEvent(domain.RunEvent{RunID: runs[0].ID, ConversationID: conversation, Type: domain.EventCitationResolved,
				Payload: map[string]any{"protocol_version": domain.RAGCitationProtocolVersion, "message_id": "missing-candidate"}}); err != nil {
				t.Fatal(err)
			}
			response = performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+runs[0].ID+"/replay", "")
			var missing map[string]json.RawMessage
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &missing) != nil || missing["candidate"] != nil {
				t.Fatalf("invalid receipt borrowed old evidence: %s", response.Body.String())
			}
			retired := performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+runs[0].ID+"/episode", "")
			if retired.Code != http.StatusNotFound || !strings.Contains(retired.Body.String(), "route not found") {
				t.Fatalf("retired endpoint: %d %s", retired.Code, retired.Body.String())
			}
		})
	}
}

func TestReplayDoesNotExposeAnAgentMovedToAnotherWorkspace(t *testing.T) {
	storage := fixturestore.New()
	agent, err := storage.CreateAgent(domain.Agent{Name: "Original", SystemPrompt: "Original Run guidance"})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversation("Retained history")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Agent.ID = agent.ID
	snapshot.Agent.Name = agent.Name
	snapshot.Agent.SystemPrompt = agent.SystemPrompt
	run, err := storage.CreateRunWithContract(agent.ID, conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	agent.WorkspaceID = "foreign"
	agent.SystemPrompt = "Foreign private instructions"
	if _, err = storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(&Handler{store: storage}).getRunReplay(response, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/replay", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Foreign private instructions") || !strings.Contains(response.Body.String(), "Original Run guidance") {
		t.Fatalf("historical Agent privacy: %d %s", response.Code, response.Body.String())
	}
}

func TestEvidenceComparisonReadsEnforceWorkspaceScope(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversationInWorkspace("workspace-a", "private comparison")
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Handler{store: fixtureStore}).Routes()
	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/api/runs", wantStatus: http.StatusOK},
		{path: "/api/runs/" + run.ID + "/replay", wantStatus: http.StatusNotFound},
		{path: "/api/runs/" + run.ID + "/model_requests", wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set(WorkspaceHeader, "workspace-b")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus || strings.Contains(response.Body.String(), run.ID) {
			t.Fatalf("cross-workspace read leaked run: path=%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}
}
