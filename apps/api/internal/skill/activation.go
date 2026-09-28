package skill

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

type Store interface {
	GetRun(string) (domain.Run, bool, error)
	ListRunEvents(string) ([]domain.RunEvent, error)
}

type Service struct{ store Store }

func NewService(storage Store) *Service { return &Service{store: storage} }

type invocation struct{ agentID, input string }
type invocationKey struct{}

// WithAgent carries server-owned Turn identity; Tool arguments cannot supply it.
func WithAgent(ctx context.Context, agentID, input string) context.Context {
	return context.WithValue(ctx, invocationKey{}, invocation{agentID: agentID, input: input})
}

func Bound(snapshot *domain.RuntimeSnapshot, agentID string) ([]domain.SkillSnapshot, error) {
	if snapshot == nil {
		return nil, skillError("skill_snapshot_unavailable", "Skill runtime snapshot is unavailable")
	}
	if err := ValidateFrozen(snapshot.Skills); err != nil {
		return nil, err
	}
	agents := append([]domain.RuntimeAgentSnapshot{snapshot.Agent}, snapshot.CandidateAgents...)
	for _, agent := range agents {
		if agent.ID != agentID {
			continue
		}
		items := []domain.SkillSnapshot{}
		for _, name := range agent.Skills {
			found := false
			for _, item := range snapshot.Skills {
				if item.Name == name {
					items = append(items, item)
					found = true
					break
				}
			}
			if !found {
				return nil, skillError("skill_snapshot_unavailable", "Bound Skill content is missing from the frozen snapshot")
			}
		}
		return items, nil
	}
	return nil, skillError("skill_scope_invalid", "Agent is not part of this frozen Run")
}

// Active deduplicates successful, Agent-owned activation events. Explicit
// /skill:name invokes frozen instructions without waiting for a model decision.
func (s *Service) Active(ctx context.Context, snapshot *domain.RuntimeSnapshot) ([]domain.SkillSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	caller, _ := ctx.Value(invocationKey{}).(invocation)
	bound, err := Bound(snapshot, caller.agentID)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	fields := strings.Fields(caller.input)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/skill:") {
		name := strings.TrimPrefix(fields[0], "/skill:")
		if !slices.ContainsFunc(bound, func(item domain.SkillSnapshot) bool { return item.Name == name }) {
			return nil, skillError("skill_not_bound", "Requested Skill is not bound to this Agent")
		}
		selected[name] = true
	}
	// ponytail: scan the bounded Run log rather than persist a second activation
	// state; add an event index only if long Runs make this measurable.
	events, err := s.store.ListRunEvents(eventpkg.ScopeFromContext(ctx).RunID)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.Type != domain.EventToolCompleted || event.Payload["tool_name"] != LoadToolName || event.Payload["truncated"] == true || event.Payload["error"] != nil && event.Payload["error"] != "" {
			continue
		}
		var result loadResult
		encoded, _ := json.Marshal(event.Payload["result"])
		if json.Unmarshal(encoded, &result) != nil || result.AgentID != caller.agentID {
			continue
		}
		for _, item := range bound {
			if item.Name != result.Name {
				continue
			}
			if item.Hash != result.Hash {
				return nil, skillError("skill_content_changed", "Activation hash does not match the frozen Skill")
			}
			selected[item.Name] = true
		}
	}
	active := []domain.SkillSnapshot{}
	for _, item := range bound {
		if selected[item.Name] {
			active = append(active, item)
		}
	}
	return active, nil
}

func (s *Service) runContext(ctx context.Context) (domain.Run, error) {
	if err := ctx.Err(); err != nil {
		return domain.Run{}, err
	}
	scope := eventpkg.ScopeFromContext(ctx)
	caller, _ := ctx.Value(invocationKey{}).(invocation)
	if scope.RunID == "" || scope.ConversationID == "" || caller.agentID == "" {
		return domain.Run{}, skillError("skill_scope_invalid", "Skill tools require trusted Run, Conversation and Agent scope")
	}
	run, ok, err := s.store.GetRun(scope.RunID)
	if err != nil {
		return domain.Run{}, err
	}
	if !ok || run.ConversationID != scope.ConversationID || run.WorkspaceID == "" {
		return domain.Run{}, skillError("skill_scope_invalid", "Skill tool scope does not match the persisted Run")
	}
	return run, nil
}
