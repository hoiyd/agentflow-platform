package httpapi

import (
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/toolprogress"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: agentpkg.ChatModeAutonomous,
		RunBudget:          &domain.RuntimeRunBudget{},
		Agent:              domain.RuntimeAgentSnapshot{ID: "agent_planner", Name: "Planner", SystemPrompt: "Plan carefully.", Executor: domain.DefaultAgentExecutor},
		Model:              domain.RuntimeModelSnapshot{Provider: "local", Model: "test", EmbeddingBaseURL: "https://embedding.test/v1", EmbeddingModel: "local-test-embedding", EmbeddingDimensions: 1536},
		AutonomousLimits:   &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
		ToolSecurityPolicy: toolpolicy.DefaultPolicy(),
		ToolProgressGuard:  toolprogress.DefaultConfig(),
	}
}

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewClientWithTimeoutAndEmbeddingModel("", "", "https://embedding.test/v1", "test", "local-test-embedding", 1536, time.Second)
}
