package agent

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

type checkpointProviderStub struct {
	transitions []domain.RunEventType
}

func (s *checkpointProviderStub) RecordStageTransition(_ context.Context, step domain.CollaborationStep, eventType domain.RunEventType) (domain.RunEvent, error) {
	s.transitions = append(s.transitions, eventType)
	return domain.RunEvent{RunID: step.RunID, StageID: step.ID, Type: eventType}, nil
}

func (*checkpointProviderStub) RestoreRun(context.Context, domain.Run) (checkpoint.RestoreReport, error) {
	return checkpoint.RestoreReport{}, nil
}

func TestRuntimeUsesInjectedCheckpointProvider(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	provider := &checkpointProviderStub{}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: openai.NewClient("", "", "test"), CheckpointProvider: provider,
	})
	step := domain.CollaborationStep{ID: "stage-1", RunID: "run-1"}
	if err := runtime.publishStage(context.Background(), step, domain.EventStageStarted); err != nil {
		t.Fatalf("publish stage: %v", err)
	}
	if len(provider.transitions) != 1 || provider.transitions[0] != domain.EventStageStarted {
		t.Fatalf("injected provider was not used: %#v", provider.transitions)
	}
}

func TestRuntimeDefaultsToInternalCheckpointProvider(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: openai.NewClient("", "", "test")})
	if _, ok := runtime.checkpoints.(*checkpoint.InternalProvider); !ok {
		t.Fatalf("expected internal checkpoint provider, got %T", runtime.checkpoints)
	}
}
