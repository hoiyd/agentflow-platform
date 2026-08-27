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
