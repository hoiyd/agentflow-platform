package toolloop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

const (
	discoveryToolName = "tool_search"
	maxSearchCalls    = 8
	maxSearchResults  = 3
	maxActiveTools    = 32
)

type toolVisibility struct {
	catalog *tool.Catalog
	request Request
	state   domain.ToolDiscoveryState
	search  tool.Binding
}

func prepareToolVisibility(ctx context.Context, catalog *tool.Catalog, request Request) (*toolVisibility, error) {
	config := request.SchemaConfig.Normalize()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var latest *domain.RunEvent
	if config.Mode != "eager" && request.RunEvents != nil {
		items, err := request.RunEvents()
		if err != nil {
			return nil, fmt.Errorf("restore Tool discovery: %w", err)
		}
		for i := range items {
			item := &items[i]
			if item.Type == domain.EventToolDiscoveryUpdated && item.RunID == request.Trace.RunID && item.StageID == request.Trace.StepID && item.Payload["agent_id"] == request.AgentID && (latest == nil || item.Sequence > latest.Sequence) {
				latest = item
			}
		}
		if latest != nil {
			// A recovered scope keeps its chosen mode even if the ready set
			// now falls below auto's threshold; drift is validated below.
			config.Mode = "lazy"
		}
	}
	plan := planSchemas(catalog, config)
	if latest != nil && plan.mode != "lazy" {
		return nil, fmt.Errorf("Tool discovery state no longer matches frozen candidates")
	}
	v := &toolVisibility{catalog: catalog, request: request, state: domain.ToolDiscoveryState{
		AgentID: request.AgentID, Mode: plan.mode, FullSchemaEstimatedTokens: plan.fullTokens, Active: []domain.ToolSchemaIdentity{},
	}}
	if plan.mode == "eager" {
		return v, nil
	}
	if request.Sink == nil || request.RunEvents == nil || request.Trace.RunID == "" || request.AgentID == "" {
		return nil, fmt.Errorf("lazy Tool Schema requires durable events and Run/Agent identity")
	}
	if _, exists := catalog.Installed(discoveryToolName); exists {
		return nil, fmt.Errorf("%q is reserved for runtime discovery", discoveryToolName)
	}
	identities := []domain.ToolSchemaIdentity{}
	for _, name := range catalog.EnabledNames() {
		binding, _ := catalog.ResolveReady(name)
		identities = append(identities, domain.ToolSchemaIdentity{Name: name, Revision: binding.Descriptor.DefinitionRevision})
	}
	data, _ := json.Marshal(identities)
	v.state.CandidateDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	v.state.Active = plan.core
	v.search = tool.Binding{Descriptor: plan.search, Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
		if v.state.SearchCalls >= maxSearchCalls {
			return nil, &tool.ExecutionError{Code: tool.ErrorBudgetExceeded, Message: "Tool discovery search limit reached"}
		}
		v.state.SearchCalls++
		if err := v.save(ctx, "search_admitted"); err != nil {
			return nil, &tool.ExecutionError{Code: tool.ErrorSecurityAudit, Message: "Tool discovery state could not be persisted", Cause: err}
		}
		var input struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, err
		}
		matches := plan.index.Search(input.Query, maxSearchResults)
		for _, match := range matches {
			if err := v.checkCurrent(ctx, match.Name); err != nil {
				return nil, &tool.ExecutionError{Code: tool.ErrorSecurityPolicyDenied, Message: "discovered Tool is no longer available", Cause: err}
			}
		}
		return matches, nil
	}}
	// This runtime-only read of a filtered index needs no external capability.
	// Its exact rule does not grant any authority to a discovered target.
	security := catalog.SecurityPolicy()
	security.Rules = append([]policy.Rule{{ID: "runtime-tool-discovery", Tool: discoveryToolName, Action: policy.ActionAllow, Capability: policy.NormalizeCapability(policy.Capability{})}}, security.Rules...)
	bindings := []tool.Binding{v.search}
	for _, name := range catalog.EnabledNames() {
		binding, _ := catalog.ResolveReady(name)
		bindings = append(bindings, binding)
	}
	var err error
	v.catalog, err = tool.NewCatalogWithPolicy(security, bindings...)
	if err != nil {
		return nil, err
	}
	v.search, _ = v.catalog.ResolveReady(discoveryToolName)
	if latest != nil {
		data, err := json.Marshal(latest.Payload)
		if err != nil {
			return nil, err
		}
		var restored domain.ToolDiscoveryState
		if err := json.Unmarshal(data, &restored); err != nil {
			return nil, err
		}
		if restored.Mode != "lazy" || restored.CandidateDigest != v.state.CandidateDigest || restored.SearchCalls < 0 || restored.SearchCalls > maxSearchCalls || len(restored.Active) > maxActiveTools {
			return nil, fmt.Errorf("Tool discovery state no longer matches frozen candidates or bounds")
		}
		seen := map[string]bool{}
		for _, active := range restored.Active {
			binding, ok := catalog.ResolveReady(active.Name)
			if !ok || seen[active.Name] || binding.Descriptor.DefinitionRevision != active.Revision {
				return nil, fmt.Errorf("activated Tool %q no longer matches its frozen definition", active.Name)
			}
			seen[active.Name] = true
		}
		v.state = restored
	}
	if err := v.validateActive(ctx); err != nil {
		return nil, err
	}
	if latest == nil {
		if err := v.save(ctx, "initial_visibility"); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (v *toolVisibility) definitions() []map[string]any {
	if v.state.Mode != "lazy" {
		return v.catalog.Definitions()
	}
	definitions := []map[string]any{v.search.Descriptor.Definition()}
	for _, active := range v.state.Active {
		binding, _ := v.catalog.ResolveReady(active.Name)
		definitions = append(definitions, binding.Descriptor.Definition())
	}
	return definitions
}

func definitionNames(definitions []map[string]any) []string {
	names := make([]string, 0, len(definitions))
	for _, item := range definitions {
		names = append(names, item["function"].(map[string]any)["name"].(string))
	}
	return names
}

func (v *toolVisibility) authorize(ctx context.Context, call tool.ExecutionRequest, scope policy.Scope) error {
	if v.state.Mode == "lazy" {
		if call.Tool == discoveryToolName {
			return nil
		}
		if !slices.ContainsFunc(v.state.Active, func(item domain.ToolSchemaIdentity) bool { return item.Name == call.Tool }) {
			return &unloadedToolError{}
		}
	}
	if v.request.ExecutorOptions.Authorize != nil {
		return v.request.ExecutorOptions.Authorize(ctx, call, scope)
	}
	return nil
}

type unloadedToolError struct{}

func (*unloadedToolError) Error() string {
	return "tool_not_loaded: use tool_search then call in the next round"
}
func (*unloadedToolError) AvailabilityReason() string { return "tool_not_loaded" }

func (v *toolVisibility) checkCurrent(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binding, ok := v.catalog.ResolveReady(name)
	if !ok {
		return fmt.Errorf("Tool %q is unavailable", name)
	}
	if v.request.ExecutorOptions.Authorize == nil {
		return nil
	}
	return v.request.ExecutorOptions.Authorize(ctx, tool.ExecutionRequest{
		RunID: v.request.Trace.RunID, StageID: v.request.Trace.StepID, TurnID: eventpkg.ScopeFromContext(ctx).TurnID,
		Tool: name, DefinitionRevision: binding.Descriptor.DefinitionRevision, CredentialScopes: v.request.ExecutorOptions.CredentialScopes,
	}, binding.Descriptor.Security.Scope)
}

func (v *toolVisibility) validateActive(ctx context.Context) error {
	if v.state.Mode != "lazy" {
		return nil
	}
	for _, active := range v.state.Active {
		if err := v.checkCurrent(ctx, active.Name); err != nil {
			return err
		}
	}
	return nil
}

func (v *toolVisibility) activate(ctx context.Context, results []tool.ExecutionResult) error {
	if v.state.Mode != "lazy" {
		return nil
	}
	active := append([]domain.ToolSchemaIdentity{}, v.state.Active...)
	for _, result := range results {
		if result.Tool != discoveryToolName || result.Error != nil {
			continue
		}
		if result.Truncated {
			return fmt.Errorf("Tool discovery result was truncated; no schemas activated")
		}
		data, err := json.Marshal(result.Result)
		if err != nil {
			return err
		}
		var matches []tool.DiscoveryMatch
		if err := json.Unmarshal(data, &matches); err != nil {
			return err
		}
		for _, match := range matches {
			if err := v.checkCurrent(ctx, match.Name); err != nil {
				return err
			}
			binding, _ := v.catalog.ResolveReady(match.Name)
			if match.Revision != binding.Descriptor.DefinitionRevision {
				return fmt.Errorf("discovered Tool revision changed")
			}
			if !slices.ContainsFunc(active, func(item domain.ToolSchemaIdentity) bool { return item.Name == match.Name }) {
				active = append(active, domain.ToolSchemaIdentity{Name: match.Name, Revision: match.Revision})
			}
		}
	}
	if len(active) > maxActiveTools {
		return &tool.ExecutionError{Code: tool.ErrorBudgetExceeded, Message: "active Tool schema limit reached"}
	}
	if len(active) == len(v.state.Active) {
		return nil
	}
	v.state.Active = active
	return v.save(ctx, "search_match")
}

func (v *toolVisibility) save(ctx context.Context, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.state.Reason = reason
	v.state.VisibleSchemaEstimatedTokens = schemaTokens(v.definitions())
	scope := eventpkg.ScopeFromContext(ctx)
	item, err := eventpkg.NewRunEvent(domain.EventToolDiscoveryUpdated, eventpkg.EventMetadata{
		RunID: v.request.Trace.RunID, ConversationID: scope.ConversationID, StageID: v.request.Trace.StepID, TurnID: scope.TurnID,
	}, eventpkg.ToolDiscoveryPayload{ToolDiscoveryState: v.state})
	if err != nil {
		return err
	}
	return v.request.Sink.Publish(ctx, item)
}
