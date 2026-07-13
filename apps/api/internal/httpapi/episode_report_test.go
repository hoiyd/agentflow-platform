package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestGetEpisodeReportAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Episode report")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := fileStore.AddMessage(conversation.ID, "user", "Summarize my backend experience"); err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := fileStore.AddMessage(conversation.ID, "assistant", "You have Go and Postgres experience."); err != nil {
		t.Fatalf("add assistant message: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	run, err = fileStore.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	step, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
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
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
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
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
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
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventToolCompleted,
		Payload: map[string]any{"tool_name": "calculator", "tool_call_id": "call_1"},
	}); err != nil {
		t.Fatalf("create tool trace: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	handler := NewHandler(fileStore, nil, nil, nil)
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
	if report.Verification.Status != "passed" {
		t.Fatalf("expected passed verification, got %#v", report.Verification)
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

func TestBuildEpisodeReportFailedVerification(t *testing.T) {
	report := buildEpisodeReport(domain.RunReplay{
		Run: domain.Run{
			Status: domain.RunFailed,
			Error:  "llm failed",
		},
		Summary: domain.RunTraceSummary{ErrorCount: 1},
		RunEvents: []domain.RunEvent{
			{ID: "event_1", Type: domain.EventModelFailed, Payload: map[string]any{"source": "llm", "error": "429"}},
		},
	}, domain.Agent{ID: "agent_planner"})

	if report.Verification.Status != "failed" {
		t.Fatalf("expected failed verification, got %#v", report.Verification)
	}
	if len(report.Errors) != 2 {
		t.Fatalf("expected run and trace errors, got %#v", report.Errors)
	}
}
