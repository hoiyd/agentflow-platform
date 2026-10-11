package store

import (
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"sync"
	"testing"
	"time"
)

func TestPostgresRunUsageReservationIsAtomic(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()
	conversation, err := store.CreateConversation("Postgres usage concurrency")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteConversation(conversation.ID) })
	snapshot := testRuntimeSnapshot()
	snapshot.RunBudget = &domain.RuntimeRunBudget{MaxToolCalls: 1}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, _, applyErr := store.ApplyRunUsage(domain.RunUsageEntry{
				ID: "usage-tool-" + run.ID + "-" + string(rune('a'+index)), RunID: run.ID,
				OperationID: "tool-call-" + string(rune('a'+index)), Kind: domain.UsageToolExecution,
				Purpose: domain.UsagePurposePrimary, ToolName: "calculator", ToolCalls: 1,
				Timestamp: time.Now().UTC(),
			})
			errs <- applyErr
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded, rejected := 0, 0
	for applyErr := range errs {
		if applyErr == nil {
			succeeded++
			continue
		}
		if exceeded, ok := budget.AsExceeded(applyErr); ok && exceeded.Resource == budget.ResourceToolCalls {
			rejected++
			continue
		}
		t.Fatalf("unexpected apply error: %v", applyErr)
	}
	ledger, ok, err := store.GetRunUsageLedger(run.ID)
	if err != nil || !ok || succeeded != 1 || rejected != 1 || ledger.Totals.ToolCalls != 1 || len(ledger.Entries) != 1 {
		t.Fatalf("atomic tool reservation failed: succeeded=%d rejected=%d ledger=%#v err=%v", succeeded, rejected, ledger, err)
	}
}

func TestPostgresActiveRuntimeExcludesWaitingForUser(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()
	conversation, err := store.CreateConversation("Postgres runtime segments")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteConversation(conversation.ID) })
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	emptyRequests, err := store.ListModelRequestRecords(run.ID)
	if err != nil || len(emptyRequests) != 0 {
		t.Fatalf("list empty model requests: items=%#v err=%v", emptyRequests, err)
	}
	if _, err := store.ListModelRequestRecords("missing-run"); err == nil {
		t.Fatal("expected missing run model request error")
	}
	if _, err = store.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	time.Sleep(8 * time.Millisecond)
	waiting, err := store.UpdateRunStatus(run.ID, domain.RunWaitingForUser, "")
	if err != nil || waiting.ActiveRuntimeMS <= 0 || waiting.ExecutionStartedAt != nil {
		t.Fatalf("pause run: run=%#v err=%v", waiting, err)
	}
	activeBeforeWait := waiting.ActiveRuntimeMS
	time.Sleep(20 * time.Millisecond)
	resumed, err := store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil || resumed.ActiveRuntimeMS != activeBeforeWait || resumed.ExecutionStartedAt == nil {
		t.Fatalf("waiting time leaked into postgres active runtime: before=%d resumed=%#v err=%v", activeBeforeWait, resumed, err)
	}
}
