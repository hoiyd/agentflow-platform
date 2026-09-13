package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/modelrouting"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

func singleModelRouteCatalog(client modelprovider.Client, config domain.ContextAssemblyConfig, budget domain.RuntimeRunBudget) (*modelrouting.Catalog, error) {
	if client == nil {
		return nil, errors.Join(modelrouting.ErrInvalidCatalog, errors.New("model route client is required"))
	}
	config = contextassembly.NormalizeConfig(config)
	identity := client.RuntimeIdentity()
	return modelrouting.NewCatalog(modelrouting.Binding{Descriptor: modelrouting.Descriptor{
		ID: "single", Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
		Capabilities:        modelrouting.Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true},
		ContextWindowTokens: config.ContextWindowTokens, MaxOutputTokens: config.OutputReserveTokens,
		Priority: 100, Pricing: modelrouting.Pricing{
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
		return domain.ModelRouteCatalogSnapshot{}, modelrouting.ErrInvalidCatalog
	}
	return domain.ModelRouteCatalogSnapshot{
		PolicyRevision: modelrouting.PolicyRevision, CatalogRevision: r.modelRoutes.Revision(), Routes: r.modelRoutes.Descriptors(),
	}, nil
}

func (r *Runtime) restoreModelRouteCatalog(snapshot domain.ModelRouteCatalogSnapshot) (*modelrouting.Catalog, error) {
	if err := validateModelRoutingSnapshot(snapshot); err != nil {
		return nil, err
	}
	if r.modelRoutesErr != nil {
		return nil, r.modelRoutesErr
	}
	if r.modelRoutes == nil {
		return nil, modelrouting.ErrInvalidCatalog
	}
	bindings := make([]modelrouting.Binding, 0, len(snapshot.Routes))
	for _, frozen := range snapshot.Routes {
		current, ok := r.modelRoutes.Resolve(frozen.ID)
		if !ok {
			return nil, errors.Join(modelrouting.ErrNoCompatibleRoute, fmt.Errorf("frozen model route %q is not installed", frozen.ID))
		}
		if current.Descriptor.CredentialEnvironment != frozen.CredentialEnvironment {
			return nil, errors.Join(modelrouting.ErrNoCompatibleRoute, fmt.Errorf("credential reference for frozen model route %q changed", frozen.ID))
		}
		identity := current.Client.RuntimeIdentity()
		identity.Provider = frozen.Provider
		identity.BaseURL = frozen.Endpoint
		identity.Model = frozen.Model
		bindings = append(bindings, modelrouting.Binding{
			Descriptor: frozen, Client: current.Client.WithRuntimeIdentity(identity),
		})
	}
	catalog, err := modelrouting.NewCatalog(bindings...)
	if err != nil {
		return nil, err
	}
	if catalog.Revision() != snapshot.CatalogRevision {
		return nil, errors.Join(modelrouting.ErrInvalidCatalog, errors.New("frozen model route catalog revision does not match"))
	}
	return catalog, nil
}

func validateModelRoutingSnapshot(snapshot domain.ModelRouteCatalogSnapshot) error {
	if snapshot.PolicyRevision != modelrouting.PolicyRevision || strings.TrimSpace(snapshot.CatalogRevision) == "" || len(snapshot.Routes) == 0 {
		return errors.New("runtime snapshot has no valid model route catalog")
	}
	descriptors := make([]modelrouting.Descriptor, 0, len(snapshot.Routes))
	seen := make(map[string]bool, len(snapshot.Routes))
	for _, frozen := range snapshot.Routes {
		descriptor, err := modelrouting.ValidateDescriptor(frozen)
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
	revision, err := modelrouting.CatalogRevision(descriptors)
	if err != nil || revision != snapshot.CatalogRevision {
		return errors.New("runtime snapshot model route catalog revision does not match")
	}
	return nil
}

func (r *Runtime) selectModelRoute(ctx context.Context, request turnpkg.Request, snapshot *domain.RuntimeSnapshot) (modelrouting.Decision, error) {
	catalog, err := r.restoreModelRouteCatalog(snapshot.ModelRouting)
	if err != nil {
		return modelrouting.Decision{}, err
	}
	requirements := modelRequirements(request, snapshot)
	selectedRouteID := snapshot.ModelRouting.PinnedRouteID
	if selectedRouteID == "" {
		if selected, ok, selectedErr := r.selectedModelRoute(request.RunID, snapshot); selectedErr != nil {
			return modelrouting.Decision{}, selectedErr
		} else if ok {
			selectedRouteID = selected.Route.ID
		}
	}
	var decision modelrouting.Decision
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
func (r *Runtime) ModelClientForRun(runID string) (modelprovider.Client, error) {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil {
		return nil, err
	}
	decision, ok, err := r.selectedModelRoute(runID, snapshot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.Join(modelrouting.ErrNoCompatibleRoute, errors.New("run has no selected model route"))
	}
	return decision.Client, nil
}

func (r *Runtime) selectedModelRoute(runID string, snapshot *domain.RuntimeSnapshot) (modelrouting.Decision, bool, error) {
	catalog, err := r.restoreModelRouteCatalog(snapshot.ModelRouting)
	if err != nil {
		return modelrouting.Decision{}, false, err
	}
	routeID := strings.TrimSpace(snapshot.ModelRouting.PinnedRouteID)
	if routeID == "" {
		if r.store == nil {
			return modelrouting.Decision{}, false, nil
		}
		events, listErr := r.store.ListRunEvents(runID)
		if listErr != nil {
			return modelrouting.Decision{}, false, listErr
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
		return modelrouting.Decision{}, false, nil
	}
	binding, ok := catalog.Resolve(routeID)
	if !ok {
		return modelrouting.Decision{}, false, errors.Join(modelrouting.ErrNoCompatibleRoute, fmt.Errorf("selected model route %q is unavailable", routeID))
	}
	return modelrouting.Decision{
		PolicyRevision: modelrouting.PolicyRevision, CatalogRevision: catalog.Revision(),
		Route: binding.Descriptor, Client: binding.Client,
	}, true, nil
}

func modelRequirements(request turnpkg.Request, snapshot *domain.RuntimeSnapshot) modelrouting.Requirements {
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
		toolCalling = toolCalling || len(snapshot.Tools) > 0
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
	return modelrouting.Requirements{
		Purpose: purpose, ToolCalling: toolCalling, StructuredOutput: structuredOutput,
		Streaming: streaming, EstimatedInputTokens: estimated,
		MaxOutputTokens: snapshot.ContextAssembly.OutputReserveTokens,
	}
}

func publishModelRouteDecision(ctx context.Context, request turnpkg.Request, decision modelrouting.Decision, routeErr error) error {
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
