package httpapi

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"context"
)

// Failure inventory: later turns and Agent edits must not rewrite Run evidence;
// missing or invalid receipts must not borrow a neighboring answer or Stage.
func TestEpisodeReportKeepsRunFactsAcrossConversationTurns(t *testing.T) {
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
			for index, run := range runs {
				response := performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+run.ID+"/episode", "")
				var report domain.EpisodeReport
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &report) != nil {
					t.Fatalf("episode: %s", response.Body.String())
				}
				want := []string{"First task", "Second task"}[index]
				if report.Task != want || report.FinalOutput == "" || len(report.Messages) != 1 || report.Messages[0].Content != report.FinalOutput || report.Agent.Name != "Frozen persona" || report.Agent.SystemPrompt != "Original instructions" || strings.Contains(response.Body.String(), "UNRELATED_LATER_MESSAGE") {
					t.Fatalf("Run facts changed: task=%q output=%q Agent=%+v messages=%+v", report.Task, report.FinalOutput, report.Agent, report.Messages)
				}
			}
			if err := storage.ArchiveAgent(agent.ID); err != nil {
				t.Fatal(err)
			}
			response := performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+runs[0].ID+"/episode", "")
			if response.Code != 200 || !strings.Contains(response.Body.String(), "Frozen persona") {
				t.Fatalf("archived Agent: %s", response.Body.String())
			}
			if _, err := storage.CreateRunEvent(domain.RunEvent{RunID: runs[0].ID, ConversationID: conversation, Type: domain.EventCitationResolved,
				Payload: map[string]any{"protocol_version": domain.RAGCitationProtocolVersion, "message_id": "missing-candidate"}}); err != nil {
				t.Fatal(err)
			}
			response = performJSONRequest(t, routes, http.MethodGet, "/api/runs/"+runs[0].ID+"/episode", "")
			var missing domain.EpisodeReport
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &missing) != nil || missing.FinalOutput != "" || missing.Task != "" || len(missing.Messages) != 0 || len(missing.Verification.Warnings) == 0 {
				t.Fatalf("invalid receipt borrowed old evidence: %s", response.Body.String())
			}
		})
	}
}

func TestEpisodeReportDoesNotGuessUnboundAnswers(t *testing.T) {
	for _, role := range []string{"", "worker", "final", "finalizer"} {
		replay := domain.RunReplay{Run: domain.Run{ID: "run", Status: domain.RunFailed}, Messages: []domain.Message{{ID: "other", Role: "assistant", Content: "Other Run answer"}}}
		if role != "" {
			replay.Steps = []domain.CollaborationStep{{Role: role, Status: domain.CollaborationStepCompleted, Output: "Unbound Stage output"}}
		}
		report := buildEpisodeReport(replay, domain.Agent{})
		if report.Task != "" || report.FinalOutput != "" || len(report.Messages) != 0 {
			t.Fatalf("invented Run facts: %+v", report)
		}
	}
}

func TestEpisodeReportDoesNotExposeAnAgentMovedToAnotherWorkspace(t *testing.T) {
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
	(&Handler{store: storage}).getEpisodeReport(response, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/episode", nil))
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
		{path: "/api/runs/" + run.ID + "/episode", wantStatus: http.StatusNotFound},
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

func TestGetEpisodeReportAPI(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Episode report")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := fixtureStore.AddMessage(conversation.ID, "user", "Summarize my backend experience"); err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := fixtureStore.AddMessage(conversation.ID, "assistant", "You have Go and Postgres experience."); err != nil {
		t.Fatalf("add assistant message: %v", err)
	}
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	run, err = fixtureStore.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	step, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		Role:           "final",
		AgentID:        "agent_planner",
		Status:         domain.CollaborationStepCompleted,
		Input:          "Summarize my backend experience",
		Output:         "Final answer with evidence.",
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventRetrievalCompleted,
		Payload: map[string]any{
			"retrieved_memories": []any{
				map[string]any{"id": "mem_1", "content": "Built Go services", "score": 0.91},
			},
			"retrieved_chunks": []any{
				map[string]any{"chunk_id": "chunk_1", "document_title": "Resume", "content": "Postgres and Go", "score": 0.87},
			},
		},
	}); err != nil {
		t.Fatalf("create retrieval trace: %v", err)
	}
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventModelCompleted,
		Payload: map[string]any{
			"role":                  "final",
			"agent_id":              "agent_planner",
			"model":                 "local_fallback",
			"prompt_tokens":         12,
			"completion_tokens":     7,
			"total_tokens":          19,
			"token_usage_estimated": true,
			"output_chars":          27,
		},
	}); err != nil {
		t.Fatalf("create llm trace: %v", err)
	}
	if _, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventToolCompleted,
		Payload: map[string]any{"tool_name": "calculator", "tool_call_id": "call_1"},
	}); err != nil {
		t.Fatalf("create tool trace: %v", err)
	}
	runtime := newRuntimeForTest(agent.RuntimeOptions{Store: fixtureStore}, newHTTPFixtureClientForTest())
	if _, err := runcompletion.Complete(context.Background(), fixtureStore.ForWorkspace(domain.NewWorkspaceScope(run.WorkspaceID)), runcompletion.Dependencies{Runtime: runtime}, runcompletion.Request{
		RunID: run.ID, ConversationID: conversation.ID, UserInput: "Summarize my backend experience", Assistant: step.Output,
	}); err != nil {
		t.Fatalf("complete Run with bound answer: %v", err)
	}

	handler := &Handler{store: fixtureStore}
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/episode", nil)
	recorder := httptest.NewRecorder()
	handler.getEpisodeReport(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	var report domain.EpisodeReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Task != "Summarize my backend experience" {
		t.Fatalf("unexpected task: %q", report.Task)
	}
	if report.FinalOutput != "Final answer with evidence." {
		t.Fatalf("unexpected final output: %q", report.FinalOutput)
	}
	if report.Verification.Status != domain.VerificationNotRequired {
		t.Fatalf("expected verification to be not required, got %#v", report.Verification)
	}
	if len(report.Retrievals.Memories) != 1 || len(report.Retrievals.Chunks) != 1 || report.Retrievals.EventCount != 1 {
		t.Fatalf("unexpected retrievals: %#v", report.Retrievals)
	}
	if len(report.LLMCalls) != 1 || report.LLMCalls[0].TotalTokens != 19 {
		t.Fatalf("unexpected llm calls: %#v", report.LLMCalls)
	}
	if len(report.ToolCalls) != 1 || report.ToolCalls[0].ToolName != "calculator" {
		t.Fatalf("unexpected tool calls: %#v", report.ToolCalls)
	}
}

func TestBuildEpisodeReportDoesNotInventVerificationForFailedRun(t *testing.T) {
	report := buildEpisodeReport(domain.RunReplay{
		Run: domain.Run{
			Status: domain.RunFailed,
			Error:  "llm failed",
		},
		Summary: domain.RunTraceSummary{ErrorCount: 1},
		RunEvents: []domain.RunEvent{
			{ID: "event_1", Type: domain.EventModelFailed, Payload: map[string]any{
				"source": "llm", "error": "429", "error_kind": "rate_limited",
				"error_source": "model_provider", "error_category": "availability", "retryable": true,
			}},
		},
	}, domain.Agent{ID: "agent_planner"})

	if report.Verification.Status != domain.VerificationNotRequired {
		t.Fatalf("expected verification to be not required, got %#v", report.Verification)
	}
	if len(report.Errors) != 2 {
		t.Fatalf("expected run and trace errors, got %#v", report.Errors)
	}
	traceError := report.Errors[1]
	if traceError.Source != "model_provider" || traceError.Kind != "rate_limited" || traceError.Category != "availability" || traceError.Retryable == nil || !*traceError.Retryable {
		t.Fatalf("structured trace error was not projected: %#v", traceError)
	}
}

func TestBuildEpisodeReportUsesPersistedVerificationEvidence(t *testing.T) {
	contract := &domain.CompletionContract{ID: "contract_test", Version: 1, Hash: "sha256:contract"}
	report := buildEpisodeReport(domain.RunReplay{
		Run: domain.Run{Status: domain.RunCompleted, VerificationStatus: domain.VerificationPassed, CompletionContract: contract},
		VerificationEvidence: []domain.VerificationEvidence{{
			ID: "evidence_1", VerifierID: "tests", Status: domain.VerificationPassed,
			SubjectHash: "sha256:subject", Summary: "command completed successfully",
		}},
		VerificationArtifacts: []domain.VerificationArtifact{{ID: "artifact_1", EvidenceID: "evidence_1", Content: "ok"}},
	}, domain.Agent{ID: "agent_planner"})

	if report.Verification.Status != domain.VerificationPassed || report.Verification.Contract != contract || report.Verification.SubjectHash != "sha256:subject" {
		t.Fatalf("unexpected verification report: %#v", report.Verification)
	}
	if len(report.Verification.Evidence) != 1 || len(report.Verification.Records) != 1 || len(report.Verification.Artifacts) != 1 {
		t.Fatalf("persisted verification details missing: %#v", report.Verification)
	}
}
