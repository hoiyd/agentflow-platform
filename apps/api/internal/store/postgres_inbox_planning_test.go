package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: durable-inputs.md, Planning follow-up boundary.
// Legacy rows are seeded explicitly; production validation is never rewritten.
func TestPostgresInboxPlanningIntentBoundary(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	s, err := NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	agent, _, err := s.GetDefaultAgent()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			conv, err := s.CreateConversation("Planning inbox " + mode)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := testRuntimeSnapshot()
			snapshot.ExecutionConstraint = domain.ExecutionPlanOnly
			run, err := s.CreateRunWithContract(agent.ID, conv.ID, snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
				t.Fatal(err)
			}
			input := domain.RunInputRequest{Kind: "follow_up", RunID: run.ID, Content: "Do the proposed work", Mode: mode, AgentID: agent.ID, IdempotencyKey: "planning"}
			t.Run("enqueue", func(t *testing.T) {
				receipt, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
				if err == nil {
					_, _ = s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, receipt.ID)
				}
				if err == nil || failure.Describe(err).Code != "input_plan_only" {
					t.Fatalf("planning follow-up enqueue must explicitly reject: %v", err)
				}
				items, err := s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
				if err != nil || len(items) != 0 {
					t.Fatalf("rejection wrote receipts: %+v %v", items, err)
				}
			})
			steer := input
			steer.Kind, steer.IdempotencyKey = "steer", "planning-steer"
			if _, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, steer); err != nil {
				t.Fatal(err)
			}
			applied, err := s.ConsumeSteering(ctx, run.ID, "", "planning-turn")
			if err != nil || len(applied) != 1 || applied[0].Status != "applied" {
				t.Fatalf("planning steering changed: %+v %v", applied, err)
			}
			for _, stopped := range []bool{false, true} {
				t.Run(fmt.Sprintf("legacy-allowStopped-%t", stopped), func(t *testing.T) {
					legacy := input
					legacy.IdempotencyKey = fmt.Sprintf("legacy-%t", stopped)
					data, err := json.Marshal(legacy)
					if err != nil {
						t.Fatal(err)
					}
					id := NewID("input")
					if _, err := s.db.Exec(`INSERT INTO conversation_inputs(id,owner_user_id,workspace_id,conversation_id,run_id,request) VALUES($1,$2,$3,$4,$5,$6)`, id, identity.SuperUserID, conv.WorkspaceID, conv.ID, run.ID, data); err != nil {
						t.Fatal(err)
					}
					if _, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, legacy); err == nil || failure.Describe(err).Code != "input_plan_only" {
						t.Errorf("legacy enqueue retry bypassed policy: %v", err)
					}
					if _, err := s.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
						t.Fatal(err)
					}
					// Latest status/snapshot must not replace the receipt's source identity.
					ordinary := testRuntimeSnapshot()
					latest, err := s.CreateRunWithContract(agent.ID, conv.ID, ordinary, nil)
					if err != nil {
						t.Fatal(err)
					}
					status := domain.RunCompleted
					if stopped {
						status = domain.RunCanceled
					}
					if _, err := s.UpdateRunStatus(latest.ID, status, ""); err != nil {
						t.Fatal(err)
					}
					before, err := s.ListMessages(conv.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := s.CreateInputRun(ctx, id, identity.SuperUserID, stopped, agent.ID, conv.ID, ordinary, nil); err == nil || failure.Describe(err).Code != "input_plan_only" {
						t.Errorf("legacy consumption bypassed planning intent: %v", err)
					}
					reopened, err := NewPostgresStore(database)
					if err != nil {
						t.Fatal(err)
					}
					defer reopened.Close()
					items, err := reopened.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range items {
						if item.ID == id && (item.Status != "queued" || item.AppliedRunID != "") {
							t.Errorf("legacy receipt consumed: %+v", item)
						}
					}
					after, err := reopened.ListMessages(conv.ID)
					if err != nil || len(after) != len(before) {
						t.Errorf("rejected successor wrote messages: %+v %v", after, err)
					}
					var count int
					expected := 2
					if stopped {
						expected = 3
					}
					if err := s.db.QueryRow(`SELECT count(*) FROM runs WHERE conversation_id=$1`, conv.ID).Scan(&count); err != nil || count != expected {
						t.Errorf("rejected successor wrote Run: count=%d %v", count, err)
					}
					if _, err := s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, id); err != nil {
						t.Error(err)
					}
				})
			}
		})
	}
}
