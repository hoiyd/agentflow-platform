package checkpoint

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRestoreDoesNotAbandonCommittedEffectWithUnfinishedStage(t *testing.T) {
	for _, status := range []domain.CollaborationStepStatus{domain.CollaborationStepRunning, domain.CollaborationStepFailed, domain.CollaborationStepCompleted} {
		t.Run(string(status), func(t *testing.T) {
			storage, run := checkpointTestRun(t)
			provider := NewInternalProvider(storage)
			step := domain.CollaborationStep{ID: "worker", RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", Status: domain.CollaborationStepRunning, Input: "write then answer"}
			if _, err := provider.RecordStageTransition(context.Background(), step, domain.EventStageStarted); err != nil {
				t.Fatal(err)
			}
			if _, _, err := storage.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "committed-key", RunID: run.ID, StageID: step.ID, ToolCallID: "call-1", ToolName: "external_write", RequestHash: "hash"}); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.CompleteToolEffect("committed-key", []byte(`{"receipt":"committed"}`)); err != nil {
				t.Fatal(err)
			}
			if status != domain.CollaborationStepRunning {
				step.Status = status
				eventType := domain.EventStageFailed
				if status == domain.CollaborationStepCompleted {
					eventType = domain.EventStageCompleted
					step.Output = "confirmed answer"
				}
				if _, err := provider.RecordStageTransition(context.Background(), step, eventType); err != nil {
					t.Fatal(err)
				}
			}
			report, err := provider.RestoreRun(context.Background(), run)
			if status == domain.CollaborationStepCompleted {
				if err != nil || len(report.CommittedStageIDs) != 1 {
					t.Fatalf("completed stage should restore: report=%#v err=%v", report, err)
				}
			} else if !errors.Is(err, ErrNeedsReconciliation) || len(report.CompensatedStageIDs) != 0 {
				t.Fatalf("automatic retry may repeat committed write: report=%#v err=%v", report, err)
			}
		})
	}
}
