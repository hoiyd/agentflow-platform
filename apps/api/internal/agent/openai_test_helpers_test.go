package agent

import (
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: ChatModeAutonomous,
		Agent:            domain.RuntimeAgentSnapshot{ID: "agent_planner", Name: "Planner", SystemPrompt: "Plan carefully.", Executor: domain.DefaultAgentExecutor},
		Model:            domain.RuntimeModelSnapshot{Provider: "local", Model: "test", EmbeddingBaseURL: "https://embedding.test/v1", EmbeddingModel: "local-test-embedding", EmbeddingDimensions: 1536},
		AutonomousLimits: &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
	}
}

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewClientWithTimeoutAndEmbeddingModel("", "", "https://embedding.test/v1", "test", "local-test-embedding", 1536, time.Second)
}
