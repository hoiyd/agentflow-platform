package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

var errDelegationStoreTest = errors.New("delegation store test failure")

func TestRunDelegatedWorkerPropagatesDurableBoundaryFailures(t *testing.T) {
	tests := []struct {
		name   string
		fault  runtimeStoreFault
		mutate func(*PreparedCollaborationRun)
	}{
		{name: "create parent stage", fault: runtimeStoreFault{failCreateStep: true}},
		{name: "publish parent stage", fault: runtimeStoreFault{failEventType: domain.EventStageStarted}},
		{name: "freeze selected worker", mutate: func(parent *PreparedCollaborationRun) { parent.WorkerAgent.ID = "missing-agent" }},
		{name: "create child run", fault: runtimeStoreFault{failCreateChild: true}},
		{name: "publish delegation", fault: runtimeStoreFault{failEventType: domain.EventDelegationCreated}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseRuntime, fileStore, prepared := preparedCollaborationForChildTest(t)
			fault := test.fault
			fault.FileStore = fileStore
			runtime := NewRuntime(RuntimeOptions{
				Store: &fault, ModelClient: baseRuntime.modelClient, RouterMode: RouterModeQuery,
				ChildRuns: baseRuntime.childRunLimits,
			})
			if test.mutate != nil {
				test.mutate(&prepared)
			}
			if _, err := runtime.runDelegatedWorker(context.Background(), nil, prepared, "delegated task", noopChildReservation{}); err == nil {
				t.Fatal("expected delegated worker boundary error")
			}
		})
	}
}

func TestRunChildWorkerStepPropagatesStagePersistenceFailures(t *testing.T) {
	tests := []struct {
		name  string
		fault runtimeStoreFault
	}{
		{name: "create stage", fault: runtimeStoreFault{failCreateStep: true}},
		{name: "publish started stage", fault: runtimeStoreFault{failEventType: domain.EventStageStarted}},
		{name: "complete stage", fault: runtimeStoreFault{failUpdateStepStatus: domain.CollaborationStepCompleted}},
		{name: "publish completed stage", fault: runtimeStoreFault{failEventType: domain.EventStageCompleted}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseRuntime, fileStore, prepared := preparedCollaborationForChildTest(t)
			fault := test.fault
			fault.FileStore = fileStore
			runtime := NewRuntime(RuntimeOptions{Store: &fault, ModelClient: baseRuntime.modelClient})
			if _, err := runtime.runChildWorkerStep(context.Background(), prepared.Run, prepared.WorkerAgent, tools.DefaultCatalog(), "delegated task"); err == nil {
				t.Fatal("expected child stage persistence error")
			}
		})
	}
}

func TestChildRuntimeSnapshotRejectsInvalidParentAndUsesLegacyFallbackPolicy(t *testing.T) {
	runtime, _, prepared := preparedCollaborationForChildTest(t)
	invalid := prepared.Run
	invalid.RuntimeSnapshot = nil
	if _, err := runtime.childRuntimeSnapshot(invalid, prepared.WorkerAgent, "delegation", "turn", "stage"); err == nil {
		t.Fatal("expected invalid parent snapshot to be rejected")
	}
	if _, err := runtime.childRuntimeSnapshot(prepared.Run, domain.Agent{ID: "missing"}, "delegation", "turn", "stage"); err == nil || !strings.Contains(err.Error(), "absent from the frozen parent candidates") {
		t.Fatalf("expected missing frozen worker error, got %v", err)
	}

	legacy := prepared.Run
	legacy.RuntimeSnapshot.SchemaVersion = domain.TaskStateRuntimeSnapshotVersion
	legacy.RuntimeSnapshot.ChildRunPolicy = nil
	snapshot, err := runtime.childRuntimeSnapshot(legacy, prepared.WorkerAgent, "delegation", "turn", "stage")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Delegation == nil || snapshot.Delegation.TimeoutMS != runtime.childRunLimits.Timeout.Milliseconds() || snapshot.RunBudget == nil {
		t.Fatalf("legacy fallback child policy=%#v budget=%#v", snapshot.Delegation, snapshot.RunBudget)
	}
}

func preparedCollaborationForChildTest(t *testing.T) (*Runtime, *store.FileStore, PreparedCollaborationRun) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("delegated child boundary")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(), RouterMode: RouterModeQuery,
		ChildRuns: ChildRunLimits{
			MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000},
		},
	})
	prepared, err := runtime.PrepareCollaborationRun(context.Background(), "agent_planner", conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, fileStore, prepared
}

type noopChildReservation struct{}

func (noopChildReservation) Bind(string, context.CancelCauseFunc) {}

type runtimeStoreFault struct {
	*store.FileStore
	failCreateStep       bool
	failCreateChild      bool
	failUpdateStepStatus domain.CollaborationStepStatus
	failUpdateRunStatus  domain.RunStatus
	failDelegationStatus domain.DelegationStatus
	failListSteps        bool
	failEventType        domain.RunEventType
	failListDelegations  bool
	failGetRunID         string
	runDelegations       []domain.RunDelegation
}

func (s *runtimeStoreFault) CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error) {
	if s.failCreateStep {
		return domain.CollaborationStep{}, errDelegationStoreTest
	}
	return s.FileStore.CreateCollaborationStep(step)
}

func (s *runtimeStoreFault) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output, errorMessage string) (domain.CollaborationStep, error) {
	if status == s.failUpdateStepStatus && status != "" {
		return domain.CollaborationStep{}, errDelegationStoreTest
	}
	return s.FileStore.UpdateCollaborationStep(id, status, output, errorMessage)
}

func (s *runtimeStoreFault) CreateChildRun(request domain.ChildRunRequest) (domain.Run, domain.RunDelegation, error) {
	if s.failCreateChild {
		return domain.Run{}, domain.RunDelegation{}, errDelegationStoreTest
	}
	return s.FileStore.CreateChildRun(request)
}

func (s *runtimeStoreFault) UpdateRunStatus(id string, status domain.RunStatus, message string) (domain.Run, error) {
	if status == s.failUpdateRunStatus && status != "" {
		return domain.Run{}, errDelegationStoreTest
	}
	return s.FileStore.UpdateRunStatus(id, status, message)
}

func (s *runtimeStoreFault) UpdateRunDelegation(id string, result domain.DelegationResult) (domain.RunDelegation, error) {
	if result.Status == s.failDelegationStatus && result.Status != "" {
		return domain.RunDelegation{}, errDelegationStoreTest
	}
	return s.FileStore.UpdateRunDelegation(id, result)
}

func (s *runtimeStoreFault) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
	if s.failListSteps {
		return nil, errDelegationStoreTest
	}
	return s.FileStore.ListCollaborationSteps(runID)
}

func (s *runtimeStoreFault) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	if event.Type == s.failEventType && event.Type != "" {
		return domain.RunEvent{}, errDelegationStoreTest
	}
	return s.FileStore.CreateRunEvent(event)
}

func (s *runtimeStoreFault) ListRunDelegations(parentRunID string) ([]domain.RunDelegation, error) {
	if s.failListDelegations {
		return nil, errDelegationStoreTest
	}
	if s.runDelegations != nil {
		return s.runDelegations, nil
	}
	return s.FileStore.ListRunDelegations(parentRunID)
}

func (s *runtimeStoreFault) GetRun(id string) (domain.Run, bool, error) {
	if id == s.failGetRunID && id != "" {
		return domain.Run{}, false, errDelegationStoreTest
	}
	return s.FileStore.GetRun(id)
}
