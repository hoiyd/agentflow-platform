package agent

import (
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/toolprogress"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	client := newLocalFallbackOpenAIClientForTest()
	catalog, err := defaultModelRouteCatalog(client, domain.ContextAssemblyConfig{}, domain.RuntimeRunBudget{})
	if err != nil {
		panic(err)
	}
	runtime := &Runtime{modelRoutes: catalog}
	modelRouting, err := runtime.captureModelRoutingSnapshot()
	if err != nil {
		panic(err)
	}
	identity := client.RuntimeIdentity()
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: ChatModeAutonomous,
		RunBudget:          &domain.RuntimeRunBudget{},
		Agent:              domain.RuntimeAgentSnapshot{ID: "agent_planner", Name: "Planner", SystemPrompt: "Plan carefully.", Executor: domain.DefaultAgentExecutor},
		Model:              domain.RuntimeModelSnapshot{Provider: identity.Provider, BaseURL: identity.BaseURL, Model: identity.Model, EmbeddingBaseURL: identity.EmbeddingBaseURL, EmbeddingModel: identity.EmbeddingModel, EmbeddingDimensions: identity.EmbeddingDimensions},
		ModelRouting:       modelRouting,
		AutonomousLimits:   &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
		ToolSecurityPolicy: toolpolicy.DefaultPolicy(),
		ToolProgressGuard:  toolprogress.DefaultConfig(),
	}
}

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewClientWithTimeoutAndEmbeddingModel("", "", "https://embedding.test/v1", "test", "local-test-embedding", 1536, time.Second)
}
