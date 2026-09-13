package httpapi

import (
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelrouting"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/toolprogress"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	client := newLocalFallbackOpenAIClientForTest()
	identity := client.RuntimeIdentity()
	catalog, err := modelrouting.NewCatalog(modelrouting.Binding{Descriptor: modelrouting.Descriptor{
		ID: "primary", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
		Capabilities:        modelrouting.Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true},
		ContextWindowTokens: 128000, MaxOutputTokens: 8192, Priority: 100,
		Pricing: modelrouting.Pricing{Source: "test_fixture"},
	}, Client: client})
	if err != nil {
		panic(err)
	}
	route := catalog.Descriptors()[0]
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: agentpkg.ChatModeAutonomous,
		RunBudget: &domain.RuntimeRunBudget{},
		Agent:     domain.RuntimeAgentSnapshot{ID: "agent_planner", Name: "Planner", SystemPrompt: "Plan carefully.", Executor: domain.DefaultAgentExecutor},
		Model:     domain.RuntimeModelSnapshot{Provider: identity.Provider, BaseURL: identity.BaseURL, Model: identity.Model, EmbeddingBaseURL: identity.EmbeddingBaseURL, EmbeddingModel: identity.EmbeddingModel, EmbeddingDimensions: identity.EmbeddingDimensions},
		ModelRouting: domain.ModelRouteCatalogSnapshot{
			PolicyRevision: modelrouting.PolicyRevision, CatalogRevision: catalog.Revision(),
			Routes: []domain.ModelRouteDescriptor{route},
		},
		AutonomousLimits:   &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
		ToolSecurityPolicy: toolpolicy.DefaultPolicy(),
		ToolProgressGuard:  toolprogress.DefaultConfig(),
	}
}

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewClientWithTimeoutAndEmbeddingModel("", "", "https://embedding.test/v1", "test", "local-test-embedding", 1536, time.Second)
}
