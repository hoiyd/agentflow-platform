package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	route, err := routing.ValidateDescriptor(domain.ModelRouteDescriptor{
		ID: "primary", Provider: "local", Model: "test", Endpoint: "https://models.test/v1",
		Capabilities:        domain.ModelRouteCapabilities{ToolCalling: true, StructuredOutput: true, Streaming: true},
		ContextWindowTokens: 128000, MaxOutputTokens: 8192, Priority: 100,
		Pricing: domain.ModelRoutePricing{Source: "test_fixture"},
	})
	if err != nil {
		panic(err)
	}
	catalogRevision, err := routing.CatalogRevision([]domain.ModelRouteDescriptor{route})
	if err != nil {
		panic(err)
	}
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion,
		RunBudget:     &domain.RuntimeRunBudget{},
		Mode:          "single",
		Agent:         domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Embedding:     domain.RuntimeEmbeddingSnapshot{Provider: "local", BaseURL: "http://localhost:11434/api/embed", Model: "test-embedding", Dimensions: 1536},
		ModelRouting:  domain.ModelRouteCatalogSnapshot{PolicyRevision: routing.PolicyRevision, CatalogRevision: catalogRevision, Routes: []domain.ModelRouteDescriptor{route}},
		ToolSecurityPolicy: policy.Policy{
			Version: "round-trip-policy-v1", DefaultAction: policy.ActionDeny,
		},
		ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 128000, OutputReserveTokens: 8192, SafetyMarginTokens: 4096, HistoryMaxTokens: 64000, MemoryMaxTokens: 8000, KnowledgeMaxTokens: 16000},
	}
}
