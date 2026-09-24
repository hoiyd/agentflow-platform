package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	client := newLocalFallbackOpenAIClientForTest()
	catalog, err := singleModelRouteCatalog(client, domain.ContextAssemblyConfig{}, domain.RuntimeRunBudget{})
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
		Embedding:          domain.RuntimeEmbeddingSnapshot{Provider: identity.EmbeddingProvider, BaseURL: identity.EmbeddingBaseURL, Model: identity.EmbeddingModel, Dimensions: identity.EmbeddingDimensions},
		ModelRouting:       modelRouting,
		AutonomousLimits:   &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
		ToolSecurityPolicy: policy.DefaultPolicy(),
		ToolProgressGuard:  progress.DefaultConfig(),
	}
}

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewSimulatedClient()
}
