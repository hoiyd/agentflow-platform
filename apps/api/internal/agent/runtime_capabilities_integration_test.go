package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: uncertain effects and foreign Workspaces must reject
// before claim, preserving the answered clarification, Run, events and checkpoints.
func TestRuntimeNarrowStoreRejectsClarificationBeforeClaim(t *testing.T) {
	for _, mode := range []string{"single", "staged"} {
		for _, rejection := range []string{"uncertain", "foreign workspace"} {
			t.Run(mode+"/"+rejection, func(t *testing.T) {
				database := pgfixture.DatabaseURL(t)
				storage, err := store.NewPostgresStore(database)
				if err != nil {
					t.Fatal(err)
				}
				defer storage.Close()
				runtime := newRuntimeForTest(RuntimeOptions{Store: runtimeStoreCapabilitiesOnly{storage}, Clarifications: storage, CheckpointProvider: checkpoint.NewInternalProvider(storage)}, nil)
				assertOptionalBindingsAbsent(t, runtime)
				conversation, err := storage.CreateConversation("Synthetic preclaim boundary")
				if err != nil {
					t.Fatal(err)
				}
				var run domain.Run
				stageID := ""
				if mode == "single" {
					prepared, err := runtime.PrepareChatRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					run = prepared.Run
				} else {
					prepared, err := runtime.PrepareAutonomousRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					run = prepared.Run
					step, err := storage.CreateCollaborationStep(domain.CollaborationStep{RunID: run.ID, ConversationID: conversation.ID, Role: "act", AgentID: run.AgentID, Status: domain.CollaborationStepRunning, Iteration: 1})
					if err != nil {
						t.Fatal(err)
					}
					stageID = step.ID
					if err := runtime.publishStage(t.Context(), step, domain.EventStageStarted); err != nil {
						t.Fatal(err)
					}
				}
				for _, event := range []domain.RunEvent{
					{Type: domain.EventTurnStarted, Payload: map[string]any{"agent_id": run.AgentID}},
					{Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": clarification.ToolName, "tool_call_id": "clarification-call"}},
				} {
					event.RunID, event.ConversationID, event.StageID, event.TurnID = run.ID, conversation.ID, stageID, "turn-preclaim"
					if _, err := storage.CreateRunEvent(event); err != nil {
						t.Fatal(err)
					}
				}
				request := turn.Request{RunID: run.ID, ConversationID: conversation.ID, StepID: stageID, TurnID: "turn-preclaim", Role: "act"}
				boundary := runtime.clarificationBoundary(request)
				err = boundary(t.Context(), []domain.ClarificationQuestion{{ID: "audience", Question: "Who is the audience?"}}, toolloop.Continuation{Identity: "preclaim", NextRound: 2, PendingCallID: "clarification-call"})
				if err != nil {
					t.Fatal(err)
				}
				questions, err := storage.ListClarifications(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID)
				if err != nil || len(questions) != 1 {
					t.Fatalf("questions=%+v err=%v", questions, err)
				}
				answered, err := storage.AnswerClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, questions[0].ID, domain.ClarificationAnswer{Version: questions[0].Version, IdempotencyKey: "answer-once", Answers: map[string]string{"audience": "Engineers"}})
				if err != nil {
					t.Fatal(err)
				}
				workspace, wantErr := run.WorkspaceID, checkpoint.ErrNeedsReconciliation
				if rejection == "uncertain" {
					effect, _, err := storage.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "uncertain-effect", RunID: run.ID, StageID: stageID, TurnID: "turn-preclaim", ToolCallID: "write-call", ToolName: "fixture_write", RequestHash: "fixture"})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := storage.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "unknown outcome"); err != nil {
						t.Fatal(err)
					}
				} else {
					workspace, wantErr = "foreign-workspace", clarification.ErrConflict
				}
				before, found, err := storage.GetRun(run.ID)
				if err != nil || !found {
					t.Fatalf("load Run: found=%v err=%v", found, err)
				}
				priorEvents, err := storage.ListRunEvents(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				priorCheckpoints, err := storage.ListStageCheckpoints(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				priorEffects, err := storage.ListToolEffects(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				priorSteps, err := storage.ListCollaborationSteps(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				events, errs := runtime.ResumeClarification(t.Context(), identity.SuperUserID, workspace, run.ID, answered.ID)
				for event := range events {
					t.Errorf("rejected resume emitted %s", event.Type)
				}
				if err := <-errs; !errors.Is(err, wantErr) {
					t.Fatalf("resume error=%v want=%v", err, wantErr)
				}
				reopened, err := store.NewPostgresStore(database)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				after, _, err := reopened.GetRun(run.ID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("Run mutated before claim: %+v err=%v", after, err)
				}
				later, err := reopened.ListRunEvents(run.ID)
				if err != nil || !reflect.DeepEqual(priorEvents, later) {
					t.Fatalf("events mutated before claim: %+v err=%v", later, err)
				}
				checkpoints, err := reopened.ListStageCheckpoints(run.ID)
				if err != nil || !reflect.DeepEqual(priorCheckpoints, checkpoints) || (mode == "single" && len(checkpoints) != 0) {
					t.Fatalf("checkpoints changed: %+v err=%v", checkpoints, err)
				}
				effects, err := reopened.ListToolEffects(run.ID)
				if err != nil || !reflect.DeepEqual(priorEffects, effects) {
					t.Fatalf("Tool Effects changed: %+v err=%v", effects, err)
				}
				steps, err := reopened.ListCollaborationSteps(run.ID)
				if err != nil || !reflect.DeepEqual(priorSteps, steps) || (mode == "single" && len(steps) != 0) {
					t.Fatalf("Stages changed: %+v err=%v", steps, err)
				}
				questions, err = reopened.ListClarifications(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID)
				if err != nil || len(questions) != 1 || !reflect.DeepEqual(answered.Clarification, questions[0]) {
					t.Fatalf("clarification claimed or mutated: %+v err=%v", questions, err)
				}
				data, _ := json.Marshal(map[string]any{"schema": "ref1101-04-preclaim-v1", "mode": mode, "rejection": rejection, "run": after, "clarification": questions[0], "events": later, "checkpoints": checkpoints, "effects": effects, "stages": steps, "limitations": []string{"real Runtime/Postgres preclaim boundary only", "no model execution or browser transport"}})
				t.Log("REF1101_04_EVIDENCE " + string(data))
			})
		}
	}
}
