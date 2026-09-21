package taskstate

import (
	"context"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

const UpdateToolName = "update_task_state"

func (s *Service) ToolBinding() tool.Binding {
	return tool.Binding{
		Descriptor: tool.Descriptor{
			Name:        UpdateToolName,
			Description: "Apply a version-checked patch to durable conversation task state. Use the version shown in <task_state>; use 0 when no task state exists. Patch only facts that actually changed.",
			Concurrency: tool.ConcurrencyPolicy{Mode: tool.ConcurrencySerial},
			SideEffect:  tool.SideEffectPolicy{Mode: tool.SideEffectExternal},
			Security: policy.NormalizeCapability(policy.Capability{
				Scope: policy.Scope{Resources: []policy.ResourceScope{{
					Kind: policy.ResourceConversation, Name: "task_state", Access: policy.AccessWrite,
				}}},
				SideEffect: policy.SideEffectInternalWrite, Reversibility: policy.Compensatable,
				Visibility: policy.VisibilityUser, Audit: policy.AuditFull,
			}),
			Parameters: taskStatePatchSchema(),
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			if s == nil || s.store == nil {
				return nil, errors.New("task state service is unavailable")
			}
			var patch domain.TaskStatePatch
			if err := json.Unmarshal(arguments, &patch); err != nil {
				return nil, err
			}
			scope := eventpkg.ScopeFromContext(ctx)
			if scope.ConversationID == "" || scope.RunID == "" {
				return nil, errors.New("task state tool requires conversation and run scope")
			}
			revision, err := s.Apply(ctx, scope.ConversationID, patch, domain.TaskStateSource{
				ActorType: "model", RunID: scope.RunID, StageID: scope.StageID, TurnID: scope.TurnID,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"applied": true, "revision_id": revision.ID, "version": revision.Version,
				"state": revision.State,
			}, nil
		},
	}
}

func taskStatePatchSchema() map[string]any {
	stringID := map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	artifactRefs := map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}
	return tool.ObjectSchema(map[string]any{
		"expected_version": map[string]any{"type": "integer", "minimum": 0},
		"operations": map[string]any{
			"type": "array", "minItems": 1, "maxItems": 50,
			"items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"type"},
				"properties": map[string]any{
					"type": map[string]any{"type": "string", "enum": []string{
						"set_goal", "clear_goal", "upsert_task", "set_task_status", "remove_task",
						"add_decision", "upsert_constraint", "remove_constraint", "upsert_blocker",
						"resolve_blocker", "remove_blocker", "add_artifact_ref", "remove_artifact_ref",
					}},
					"goal":        map[string]any{"type": "string", "maxLength": 2000},
					"task_id":     stringID,
					"task_status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "canceled"}},
					"task": tool.ObjectSchema(map[string]any{
						"id": stringID, "title": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
						"details":       map[string]any{"type": "string", "maxLength": 2000},
						"status":        map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "canceled"}},
						"artifact_refs": artifactRefs,
					}, []string{"id", "title", "status"}),
					"decision": tool.ObjectSchema(map[string]any{
						"id": stringID, "statement": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
						"rationale": map[string]any{"type": "string", "maxLength": 2000}, "supersedes_id": stringID,
					}, []string{"id", "statement"}),
					"constraint_id": stringID,
					"constraint": tool.ObjectSchema(map[string]any{
						"id": stringID, "statement": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
					}, []string{"id", "statement"}),
					"blocker_id": stringID,
					"blocker": tool.ObjectSchema(map[string]any{
						"id": stringID, "description": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
						"status": map[string]any{"type": "string", "enum": []string{"open", "resolved"}},
					}, []string{"id", "description", "status"}),
					"artifact_ref": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
				},
			},
		},
	}, []string{"expected_version", "operations"})
}
