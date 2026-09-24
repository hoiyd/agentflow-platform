package httpapi

import (
	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	client := newLocalFallbackOpenAIClientForTest()
	identity := client.RuntimeIdentity()
	catalog, err := routing.NewCatalog(routing.Binding{Descriptor: routing.Descriptor{
		ID: "single", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
		Capabilities:        routing.Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true},
		ContextWindowTokens: 128000, MaxOutputTokens: 8192, Priority: 100,
		Pricing: routing.Pricing{Source: "test_fixture"},
	}, Client: client})
	if err != nil {
		panic(err)
	}
	route := catalog.Descriptors()[0]
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: agentpkg.ChatModeAutonomous,
		RunBudget: &domain.RuntimeRunBudget{},
		Agent:     domain.RuntimeAgentSnapshot{ID: "agent_planner", Name: "Planner", SystemPrompt: "Plan carefully.", Executor: domain.DefaultAgentExecutor},
		Embedding: domain.RuntimeEmbeddingSnapshot{Provider: identity.EmbeddingProvider, BaseURL: identity.EmbeddingBaseURL, Model: identity.EmbeddingModel, Dimensions: identity.EmbeddingDimensions},
		ModelRouting: domain.ModelRouteCatalogSnapshot{
			PolicyRevision: routing.PolicyRevision, CatalogRevision: catalog.Revision(),
			Routes: []domain.ModelRouteDescriptor{route},
		},
		AutonomousLimits:   &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
		ToolSecurityPolicy: policy.DefaultPolicy(),
		ToolProgressGuard:  progress.DefaultConfig(),
	}
}

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewSimulatedClient()
}
