package skill

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type loadResult struct {
	domain.SkillMetadata
	AgentID       string   `json:"agent_id"`
	Resources     []string `json:"resources"`
	AlreadyLoaded bool     `json:"already_loaded"`
}

func (s *Service) ToolBindings() []tool.Binding {
	if s == nil || s.store == nil {
		return nil
	}
	security := policy.NormalizeCapability(policy.Capability{Scope: policy.Scope{Resources: []policy.ResourceScope{{Kind: policy.ResourceWorkspace, Name: "trusted_skills", Access: policy.AccessRead}}}})
	makeBinding := func(name, description string, parameters map[string]any, handler tool.Handler) tool.Binding {
		return tool.Binding{Descriptor: tool.Descriptor{Name: name, Description: description, Parameters: parameters, Concurrency: tool.ConcurrencyPolicy{Mode: tool.ConcurrencySerial}, Security: security}, Policy: tool.ExecutionPolicy{Timeout: 5 * time.Second, MaxResultBytes: 12000}, Handler: handler}
	}
	nameSchema := map[string]any{"type": "string", "minLength": 1, "maxLength": 64}
	load := makeBinding(LoadToolName, "Activate a trusted Skill bound to this Agent. Its frozen instructions enter the next model Context once; this never grants Tool permissions. Use skill_read for listed text resources.", tool.ObjectSchema(map[string]any{"name": nameSchema}, []string{"name"}), s.load)
	read := makeBinding(ReadToolName, "Read a bounded UTF-8 byte page of a frozen text resource from an activated Skill. Relative references/assets paths only; no scripts, credentials or arbitrary filesystem reads.", tool.ObjectSchema(map[string]any{"name": nameSchema, "path": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxPageBytes}}, []string{"name", "path"}), s.read)
	return []tool.Binding{load, read}
}

func (s *Service) load(ctx context.Context, args json.RawMessage) (any, error) {
	run, err := s.runContext(ctx)
	if err != nil {
		return nil, err
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	caller, _ := ctx.Value(invocationKey{}).(invocation)
	bound, err := Bound(run.RuntimeSnapshot, caller.agentID)
	if err != nil {
		return nil, err
	}
	active, err := s.Active(ctx, run.RuntimeSnapshot)
	if err != nil {
		return nil, err
	}
	for _, item := range bound {
		if item.Name != input.Name {
			continue
		}
		paths := []string{}
		for _, resource := range item.Resources {
			paths = append(paths, resource.Path)
		}
		return loadResult{SkillMetadata: Metadata(item), AgentID: caller.agentID, Resources: paths, AlreadyLoaded: slices.ContainsFunc(active, func(other domain.SkillSnapshot) bool { return other.Name == item.Name })}, nil
	}
	return nil, skillError("skill_not_bound", "Skill is not bound to this Agent")
}

func (s *Service) read(ctx context.Context, args json.RawMessage) (any, error) {
	run, err := s.runContext(ctx)
	if err != nil {
		return nil, err
	}
	var input struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  *int   `json:"limit"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	limit := 2048
	if input.Limit != nil {
		limit = *input.Limit
	}
	active, err := s.Active(ctx, run.RuntimeSnapshot)
	if err != nil {
		return nil, err
	}
	for _, item := range active {
		if item.Name == input.Name {
			return ReadResource(item, input.Path, input.Offset, limit)
		}
	}
	return nil, skillError("skill_not_active", "Load a bound Skill before reading its resources")
}
