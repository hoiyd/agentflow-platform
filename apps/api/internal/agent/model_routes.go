package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	turnpkg "agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/routing"
)

func singleModelRouteCatalog(client provider.Client, config domain.ContextAssemblyConfig, budget domain.RuntimeRunBudget) (*routing.Catalog, error) {
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

func (r *Runtime) captureModelRoutingSnapshot() (domain.ModelRouteCatalogSnapshot, error) {
	if r.modelRoutesErr != nil {
		return domain.ModelRouteCatalogSnapshot{}, r.modelRoutesErr
	}
	if r.modelRoutes == nil {
		return domain.ModelRouteCatalogSnapshot{}, routing.ErrInvalidCatalog
	}
	return domain.ModelRouteCatalogSnapshot{
		PolicyRevision: routing.PolicyRevision, CatalogRevision: r.modelRoutes.Revision(), Routes: r.modelRoutes.Descriptors(),
	}, nil
}

func (r *Runtime) restoreModelRouteCatalog(snapshot domain.ModelRouteCatalogSnapshot) (*routing.Catalog, error) {
	if err := validateModelRoutingSnapshot(snapshot); err != nil {
		return nil, err
	}
	if r.modelRoutesErr != nil {
		return nil, r.modelRoutesErr
	}
	if r.modelRoutes == nil {
		return nil, routing.ErrInvalidCatalog
	}
	bindings := make([]routing.Binding, 0, len(snapshot.Routes))
	for _, frozen := range snapshot.Routes {
		current, ok := r.modelRoutes.Resolve(frozen.ID)
		if !ok {
			return nil, errors.Join(routing.ErrNoCompatibleRoute, fmt.Errorf("frozen model route %q is not installed", frozen.ID))
		}
		if current.Descriptor.CredentialEnvironment != frozen.CredentialEnvironment {
			return nil, errors.Join(routing.ErrNoCompatibleRoute, fmt.Errorf("credential reference for frozen model route %q changed", frozen.ID))
		}
		identity := current.Client.RuntimeIdentity()
		identity.Provider = frozen.Provider
		identity.BaseURL = frozen.Endpoint
		identity.Model = frozen.Model
		bindings = append(bindings, routing.Binding{
			Descriptor: frozen, Client: current.Client.WithRuntimeIdentity(identity),
		})
	}
	catalog, err := routing.NewCatalog(bindings...)
	if err != nil {
		return nil, err
	}
	if catalog.Revision() != snapshot.CatalogRevision {
		return nil, errors.Join(routing.ErrInvalidCatalog, errors.New("frozen model route catalog revision does not match"))
	}
	return catalog, nil
}

func validateModelRoutingSnapshot(snapshot domain.ModelRouteCatalogSnapshot) error {
	if snapshot.PolicyRevision != routing.PolicyRevision || strings.TrimSpace(snapshot.CatalogRevision) == "" || len(snapshot.Routes) == 0 {
		return errors.New("runtime snapshot has no valid model route catalog")
	}
	descriptors := make([]routing.Descriptor, 0, len(snapshot.Routes))
	seen := make(map[string]bool, len(snapshot.Routes))
	for _, frozen := range snapshot.Routes {
		descriptor, err := routing.ValidateDescriptor(frozen)
		if err != nil {
			return fmt.Errorf("runtime snapshot model route is invalid: %w", err)
		}
		if seen[descriptor.ID] {
			return fmt.Errorf("runtime snapshot model route %q is duplicated", descriptor.ID)
		}
		seen[descriptor.ID] = true
		descriptors = append(descriptors, descriptor)
	}
	if snapshot.PinnedRouteID != "" && !seen[snapshot.PinnedRouteID] {
		return fmt.Errorf("runtime snapshot pinned model route %q is absent from the catalog", snapshot.PinnedRouteID)
	}
	revision, err := routing.CatalogRevision(descriptors)
	if err != nil || revision != snapshot.CatalogRevision {
		return errors.New("runtime snapshot model route catalog revision does not match")
	}
	return nil
}

func (r *Runtime) selectModelRoute(ctx context.Context, request turnpkg.Request, snapshot *domain.RuntimeSnapshot) (routing.Decision, error) {
	catalog, err := r.restoreModelRouteCatalog(snapshot.ModelRouting)
	if err != nil {
		return routing.Decision{}, err
	}
	requirements := modelRequirements(request, snapshot)
	selectedRouteID := snapshot.ModelRouting.PinnedRouteID
	if selectedRouteID == "" {
		if selected, ok, selectedErr := r.selectedModelRoute(request.RunID, snapshot); selectedErr != nil {
			return routing.Decision{}, selectedErr
		} else if ok {
			selectedRouteID = selected.Route.ID
		}
	}
	var decision routing.Decision
	var routeErr error
	if selectedRouteID == "" {
		decision, routeErr = catalog.Select(requirements)
	} else {
		decision, routeErr = catalog.SelectRoute(selectedRouteID, requirements)
	}
	if eventErr := publishModelRouteDecision(ctx, request, decision, routeErr); eventErr != nil {
		return decision, errors.Join(routeErr, eventErr)
	}
	return decision, routeErr
}

// ModelClientForRun returns the Chat LLM selected by the first successful
// routing decision. Auxiliary work uses this method instead of a default model.
func (r *Runtime) ModelClientForRun(runID string) (provider.Client, error) {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil {
		return nil, err
	}
	decision, ok, err := r.selectedModelRoute(runID, snapshot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.Join(routing.ErrNoCompatibleRoute, errors.New("run has no selected model route"))
	}
	return decision.Client, nil
}

func (r *Runtime) selectedModelRoute(runID string, snapshot *domain.RuntimeSnapshot) (routing.Decision, bool, error) {
	catalog, err := r.restoreModelRouteCatalog(snapshot.ModelRouting)
	if err != nil {
		return routing.Decision{}, false, err
	}
	routeID := strings.TrimSpace(snapshot.ModelRouting.PinnedRouteID)
	if routeID == "" {
		if r.store == nil {
			return routing.Decision{}, false, nil
		}
		events, listErr := r.store.ListRunEvents(runID)
		if listErr != nil {
			return routing.Decision{}, false, listErr
		}
		for _, item := range events {
			if item.Type == domain.EventModelRouteDecided && item.Payload["outcome"] == "selected" {
				routeID, _ = item.Payload["selected_route_id"].(string)
				routeID = strings.TrimSpace(routeID)
				if routeID != "" {
					break
				}
			}
		}
	}
	if routeID == "" {
		return routing.Decision{}, false, nil
	}
	binding, ok := catalog.Resolve(routeID)
	if !ok {
		return routing.Decision{}, false, errors.Join(routing.ErrNoCompatibleRoute, fmt.Errorf("selected model route %q is unavailable", routeID))
	}
	return routing.Decision{
		PolicyRevision: routing.PolicyRevision, CatalogRevision: catalog.Revision(),
		Route: binding.Descriptor, Client: binding.Client,
	}, true, nil
}

func modelRequirements(request turnpkg.Request, snapshot *domain.RuntimeSnapshot) routing.Requirements {
	purpose := strings.TrimSpace(request.Role)
	if purpose == "" {
		purpose = "answer"
	}
	systemPrompt := request.SystemPrompt
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = request.Agent.SystemPrompt
	}
	estimated := contextassembly.EstimateTokens(systemPrompt) + contextassembly.EstimateTokens(request.Input)
	for _, message := range request.History {
		estimated += contextassembly.EstimateTokens(message.Role) + contextassembly.EstimateTokens(message.Content) + 4
	}
	for _, memory := range request.Context.Memories {
		estimated += contextassembly.EstimateTokens(memory.Memory.Content)
	}
	for _, chunk := range request.Context.Chunks {
		estimated += contextassembly.EstimateTokens(chunk.Chunk.Content)
	}
	toolCalling := request.Catalog != nil && len(request.Catalog.EnabledNames()) > 0
	structuredOutput := purpose == "router" || purpose == "decide"
	streaming := request.ModelMode != turnpkg.ModelModeText
	if snapshot.Mode == ChatModeMultiAgent {
		structuredOutput = structuredOutput || NormalizeRouterMode(snapshot.RouterMode) == RouterModeAuto
		streaming = true
	}
	if toolCalling {
		if request.Catalog != nil {
			definitions, _ := json.Marshal(request.Catalog.Definitions())
			estimated += contextassembly.EstimateTokens(string(definitions))
		}
	}
	maxInput := snapshot.ContextAssembly.ContextWindowTokens - snapshot.ContextAssembly.OutputReserveTokens - snapshot.ContextAssembly.SafetyMarginTokens
	if maxInput > 0 && estimated > maxInput {
		estimated = maxInput
	}
	return routing.Requirements{
		Purpose: purpose, ToolCalling: toolCalling, StructuredOutput: structuredOutput,
		Streaming: streaming, EstimatedInputTokens: estimated,
		MaxOutputTokens: snapshot.ContextAssembly.OutputReserveTokens,
	}
}

func publishModelRouteDecision(ctx context.Context, request turnpkg.Request, decision routing.Decision, routeErr error) error {
	if request.Sink == nil {
		return nil
	}
	outcome := "selected"
	if routeErr != nil {
		outcome = "no_compatible_route"
	}
	payload := eventpkg.ModelRouteDecisionPayload{
		Outcome: outcome, PolicyRevision: decision.PolicyRevision, CatalogRevision: decision.CatalogRevision,
		Purpose: decision.Requirements.Purpose, SelectedRouteID: decision.Route.ID,
		Provider: decision.Route.Provider, Model: decision.Route.Model,
		Requirements: decision.Requirements, Candidates: decision.Candidates,
	}
	item, err := eventpkg.NewRunEvent(domain.EventModelRouteDecided, eventpkg.EventMetadata{
		RunID: request.RunID, ConversationID: request.ConversationID, StageID: request.StepID, TurnID: request.TurnID,
	}, payload)
	if err != nil {
		return err
	}
	return request.Sink.Publish(ctx, item)
}
