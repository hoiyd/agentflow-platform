package store

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreChildRunRoundTripAndReplayTopology(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("delegation")
	if err != nil {
		t.Fatal(err)
	}
	parentSnapshot := testRuntimeSnapshot()
	parent, err := fileStore.CreateRun("agent_planner", conversation.ID, parentSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	childSnapshot := parentSnapshot
	childSnapshot.Mode = "single"
	childSnapshot.AutonomousLimits = nil
	childSnapshot.Delegation = &domain.RuntimeDelegation{
		DelegationID: "delegation-1", ParentRunID: parent.ID, ParentTurnID: "turn-1",
		Depth: 1, IsolatedContext: true, TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 100,
	}
	child, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
		Delegation:      domain.RunDelegation{ID: "delegation-1", ParentRunID: parent.ID, ParentTurnID: "turn-1", AgentID: "agent_planner", Depth: 1, Task: "do work", TimeoutMS: time.Minute.Milliseconds()},
		RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{}); err == nil {
		t.Fatal("expected invalid delegation status to be rejected")
	}
	blocked, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired,
	})
	if err != nil || blocked.BlockReason != domain.DelegationBlockReasonChildRecoveryRequired {
		t.Fatalf("blocked relation=%#v err=%v", blocked, err)
	}
	if relation.ChildRunID != child.ID || relation.Status != domain.DelegationCreated {
		t.Fatalf("unexpected relation: %#v", relation)
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationCompleted, Summary: "done", OutputRef: "run://child/stages/worker",
		OutputHash: "hash", OutputBytes: 4, SummaryTruncated: true,
	}); err != nil {
		t.Fatal(err)
	}
	parentReplay, ok, err := fileStore.GetRunReplay(parent.ID)
	if err != nil || !ok {
		t.Fatalf("parent replay: ok=%v err=%v", ok, err)
	}
	if len(parentReplay.ChildDelegations) != 1 || parentReplay.ChildDelegations[0].Summary != "done" {
		t.Fatalf("parent topology = %#v", parentReplay.ChildDelegations)
	}
	childReplay, ok, err := fileStore.GetRunReplay(child.ID)
	if err != nil || !ok {
		t.Fatalf("child replay: ok=%v err=%v", ok, err)
	}
	if childReplay.ParentDelegation == nil || childReplay.ParentDelegation.ParentRunID != parent.ID {
		t.Fatalf("child topology = %#v", childReplay.ParentDelegation)
	}

	reloaded, err := NewFileStore(fileStore.path)
	if err != nil {
		t.Fatal(err)
	}
	if item, ok, err := reloaded.GetParentDelegation(child.ID); err != nil || !ok || item.ID != relation.ID {
		t.Fatalf("reloaded relation: item=%#v ok=%v err=%v", item, ok, err)
	}
}

func TestFileStoreChildRunValidationAndLookupBoundaries(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("delegation validation")
	if err != nil {
		t.Fatal(err)
	}
	parentSnapshot := testRuntimeSnapshot()
	parent, err := fileStore.CreateRun("agent_planner", conversation.ID, parentSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	valid := validChildRunRequest(parent, "delegation-validation")

	tests := []struct {
		name   string
		mutate func(*domain.ChildRunRequest)
	}{
		{name: "missing identity", mutate: func(request *domain.ChildRunRequest) { request.Delegation.ID = "" }},
		{name: "invalid frozen boundary", mutate: func(request *domain.ChildRunRequest) { request.RuntimeSnapshot.Delegation.IsolatedContext = false }},
		{name: "invalid runtime snapshot", mutate: func(request *domain.ChildRunRequest) { request.RuntimeSnapshot.RunBudget = nil }},
		{name: "missing parent", mutate: func(request *domain.ChildRunRequest) {
			request.Delegation.ParentRunID = "missing"
			request.RuntimeSnapshot.Delegation.ParentRunID = "missing"
		}},
		{name: "missing agent", mutate: func(request *domain.ChildRunRequest) {
			request.Delegation.AgentID = "missing"
			request.RuntimeSnapshot.Agent.ID = "missing"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			snapshot := *valid.RuntimeSnapshot.Delegation
			request.RuntimeSnapshot.Delegation = &snapshot
			test.mutate(&request)
			if _, _, err := fileStore.CreateChildRun(request); err == nil {
				t.Fatal("expected child run validation error")
			}
		})
	}

	child, relation, err := fileStore.CreateChildRun(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fileStore.CreateChildRun(valid); err == nil {
		t.Fatal("expected duplicate delegation to be rejected")
	}
	if _, err := fileStore.UpdateRunDelegation("missing", domain.DelegationResult{Status: domain.DelegationRunning}); err == nil {
		t.Fatal("expected missing delegation update to fail")
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationRunning, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired}); err == nil {
		t.Fatal("expected block reason on running status to fail")
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationBlocked}); err == nil {
		t.Fatal("expected blocked status without reason to fail")
	}
	if _, ok, err := fileStore.GetRunDelegation("missing"); err != nil || ok {
		t.Fatalf("missing delegation lookup: ok=%v err=%v", ok, err)
	}
	if _, ok, err := fileStore.GetParentDelegation("missing"); err != nil || ok {
		t.Fatalf("missing parent delegation lookup: ok=%v err=%v", ok, err)
	}
	active, err := fileStore.ListActiveRunDelegations()
	if err != nil || len(active) != 1 || active[0].ChildRunID != child.ID {
		t.Fatalf("active delegations=%#v err=%v", active, err)
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationCompleted}); err != nil {
		t.Fatal(err)
	}
	active, err = fileStore.ListActiveRunDelegations()
	if err != nil || len(active) != 0 {
		t.Fatalf("terminal delegation remained active: %#v err=%v", active, err)
	}
	items, err := fileStore.ListRunDelegations("missing")
	if err != nil || len(items) != 0 {
		t.Fatalf("unrelated parent delegations=%#v err=%v", items, err)
	}
}

func validChildRunRequest(parent domain.Run, delegationID string) domain.ChildRunRequest {
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = "single"
	snapshot.AutonomousLimits = nil
	snapshot.Delegation = &domain.RuntimeDelegation{
		DelegationID: delegationID, ParentRunID: parent.ID, ParentTurnID: "turn-validation",
		Depth: 1, IsolatedContext: true, TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 100,
	}
	return domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: delegationID, ParentRunID: parent.ID, ParentTurnID: "turn-validation",
			AgentID: "agent_planner", Depth: 1, Task: "validate work", TimeoutMS: time.Minute.Milliseconds(),
		},
		RuntimeSnapshot: snapshot,
	}
}
