// Package modelroute constructs explicit model catalogs for deterministic fixtures.
package modelroute

import (
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/routing"
	"errors"
)

func Catalog(client provider.Client, config domain.ContextAssemblyConfig, budget domain.RuntimeRunBudget) (*routing.Catalog, error) {
	if client == nil {
		return nil, errors.Join(routing.ErrInvalidCatalog, errors.New("model route client is required"))
	}
	config = contextassembly.NormalizeConfig(config)
	identity := client.RuntimeIdentity()
	return routing.NewCatalog(routing.Binding{Descriptor: routing.Descriptor{
		ID: "single", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
		Capabilities:        routing.Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true},
		ContextWindowTokens: config.ContextWindowTokens, MaxOutputTokens: config.OutputReserveTokens,
		Priority: 100, Pricing: routing.Pricing{
			Source: "single_route", InputPerMillionTokensMicros: budget.InputCostPerMillionTokensMicros,
			OutputPerMillionTokensMicros: budget.OutputCostPerMillionTokensMicros,
		},
	}, Client: client})
}
