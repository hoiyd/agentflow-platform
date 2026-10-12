package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"encoding/json"
	"strings"
	"testing"
)

func TestClarificationSchema(t *testing.T) {
	if !strings.Contains(strings.Join(postgresMigrations, "\n"), "CREATE TABLE IF NOT EXISTS run_clarifications") {
		t.Fatal("missing durable clarification migration")
	}
}

func TestPostgresClarificationProtocol(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conv, err := s.CreateConversation("clarification")
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.GetDefaultAgent()
	run, err := s.CreateRunWithContract(a.ID, conv.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRunEvent(domain.RunEvent{Type: domain.EventTurnStarted, RunID: run.ID, ConversationID: conv.ID, TurnID: "turn-real", Payload: map[string]any{"agent_id": a.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRunEvent(domain.RunEvent{Type: domain.EventToolCompleted, RunID: run.ID, ConversationID: conv.ID, TurnID: "turn-real", Payload: map[string]any{"tool_name": "request_user_input", "tool_call_id": "call-real"}}); err != nil {
		t.Fatal(err)
	}
	pending := domain.Clarification{RunID: run.ID, ConversationID: conv.ID, TurnID: "turn-real", ToolCallID: "call-real", Questions: []domain.ClarificationQuestion{{ID: "audience", Question: "Who is the audience?"}}}
	saved, err := s.SuspendClarification(t.Context(), pending, json.RawMessage(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	state, _, _ := s.GetRun(run.ID)
	if state.Status != domain.RunWaitingForUser || state.ExecutionStartedAt != nil {
		t.Fatalf("not released: %+v", state)
	}
	submit := domain.ClarificationAnswer{Version: saved.Version, IdempotencyKey: "answer-once", Answers: map[string]string{"audience": "Engineers"}}
	if _, err = s.AnswerClarification(t.Context(), "stranger", conv.WorkspaceID, run.ID, saved.ID, submit); err == nil {
		t.Fatal("cross-owner answer")
	}
	answered, err := s.AnswerClarification(t.Context(), identity.SuperUserID, conv.WorkspaceID, run.ID, saved.ID, submit)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.AnswerClarification(t.Context(), identity.SuperUserID, conv.WorkspaceID, run.ID, saved.ID, submit)
	if err != nil || duplicate.ID != answered.ID {
		t.Fatalf("duplicate: %v", err)
	}
	submit.Answers["audience"] = "Managers"
	if _, err = s.AnswerClarification(t.Context(), identity.SuperUserID, conv.WorkspaceID, run.ID, saved.ID, submit); err == nil {
		t.Fatal("conflicting answer")
	}
	claimed, err := s.ClaimClarification(t.Context(), identity.SuperUserID, conv.WorkspaceID, run.ID, saved.ID)
	if err != nil || len(claimed.Checkpoint) == 0 || claimed.Status != "applied" {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err = s.ClaimClarification(t.Context(), identity.SuperUserID, conv.WorkspaceID, run.ID, saved.ID); err == nil {
		t.Fatal("double resume")
	}
	output, _ := json.Marshal(claimed)
	if strings.Contains(string(output), "checkpoint") {
		t.Fatal("internal checkpoint exposed")
	}
	messages, err := s.ListMessages(conv.ID)
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].Content, "Engineers") {
		t.Fatalf("answer not durable: %+v %v", messages, err)
	}
}
