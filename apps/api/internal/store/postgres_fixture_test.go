package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"time"
)

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
func testRuntimeSnapshot() domain.RuntimeSnapshot {
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion,
		RunBudget:     &domain.RuntimeRunBudget{},
		Mode:          "single",
		Agent:         domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Model:         domain.RuntimeModelSnapshot{Provider: "local", Model: "test"},
		ToolSecurityPolicy: toolpolicy.Policy{
			Version: "round-trip-policy-v1", DefaultAction: toolpolicy.ActionDeny,
		},
		ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 128000, OutputReserveTokens: 8192, SafetyMarginTokens: 4096, HistoryMaxTokens: 64000, MemoryMaxTokens: 8000, KnowledgeMaxTokens: 16000},
	}
}
