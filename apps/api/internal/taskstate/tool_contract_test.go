package taskstate

import (
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/testsupport/tooltest"
)

func TestUpdateTaskStateBindingSatisfiesContractHarness(t *testing.T) {
	service := &Service{}
	tooltest.RunBindingContract(t, tooltest.BindingContract{
		Binding: service.ToolBinding(),
		ValidArguments: json.RawMessage(`{
			"expected_version":0,
			"operations":[{"type":"set_goal","goal":"Keep facts durable"}]
		}`),
		GoodResult: map[string]any{
			"applied": true, "revision_id": "revision-1", "version": float64(1),
			"state": map[string]any{"goal": "Keep facts durable"},
		},
		InvalidCalls: []tooltest.InvalidCall{
			{Name: "missing operations", Arguments: json.RawMessage(`{"expected_version":0}`), WantArgumentCode: "required"},
			{Name: "unknown operation", Arguments: json.RawMessage(`{"expected_version":0,"operations":[{"type":"invent_state"}]}`), WantArgumentCode: "enum"},
			{Name: "nested extra field", Arguments: json.RawMessage(`{"expected_version":0,"operations":[{"type":"set_goal","goal":"goal","secret":"value"}]}`), WantArgumentCode: "additional_properties"},
		},
		BadResults: []tooltest.BadResult{
			{Name: "not applied", Value: map[string]any{"applied": false, "revision_id": "revision-1", "version": float64(1)}},
			{Name: "missing revision", Value: map[string]any{"applied": true, "version": float64(1)}},
		},
		ValidateResult: validateTaskStateToolResult,
	})
}

func validateTaskStateToolResult(value any) error {
	result, ok := value.(map[string]any)
	if !ok {
		return errors.New("result must be an object")
	}
	if applied, ok := result["applied"].(bool); !ok || !applied {
		return errors.New("result must confirm the applied mutation")
	}
	if revisionID, ok := result["revision_id"].(string); !ok || revisionID == "" {
		return errors.New("result requires revision identity")
	}
	if version, ok := result["version"].(float64); !ok || version < 1 {
		return errors.New("result requires a positive version")
	}
	if _, ok := result["state"].(map[string]any); !ok {
		return errors.New("result requires durable state")
	}
	return nil
}
