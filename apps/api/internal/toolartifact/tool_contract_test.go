package toolartifact

import (
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/tooltest"
)

func TestArtifactBindingsSatisfyContractHarness(t *testing.T) {
	service := &Service{}
	bindings := service.ToolBindings()
	if len(bindings) != 2 {
		t.Fatalf("artifact binding count = %d, want 2", len(bindings))
	}
	tooltest.RunBindingContract(t, tooltest.BindingContract{
		Binding:        bindings[0],
		ValidArguments: json.RawMessage(`{"artifact_id":"tool_artifact_1","offset":0,"limit":128}`),
		GoodResult: domain.ToolArtifactRead{
			Artifact: domain.ToolArtifact{ID: "tool_artifact_1"}, Offset: 0, Content: "value", NextOffset: 5, Complete: true,
		},
		InvalidCalls: []tooltest.InvalidCall{
			{Name: "missing id", Arguments: json.RawMessage(`{"limit":128}`), WantArgumentCode: "required"},
			{Name: "negative offset", Arguments: json.RawMessage(`{"artifact_id":"tool_artifact_1","offset":-1}`), WantArgumentCode: "minimum"},
			{Name: "unbounded limit", Arguments: json.RawMessage(`{"artifact_id":"tool_artifact_1","limit":999999}`), WantArgumentCode: "maximum"},
		},
		BadResults:     []tooltest.BadResult{{Name: "missing artifact", Value: domain.ToolArtifactRead{Content: "value"}}},
		ValidateResult: validateReadContractResult,
	})
	tooltest.RunBindingContract(t, tooltest.BindingContract{
		Binding:        bindings[1],
		ValidArguments: json.RawMessage(`{"artifact_id":"tool_artifact_1","query":"needle","max_matches":5}`),
		GoodResult: domain.ToolArtifactSearchResult{
			Artifact: domain.ToolArtifact{ID: "tool_artifact_1"}, Query: "needle",
			Matches: []domain.ToolArtifactSearchMatch{{Offset: 1, Preview: "needle"}}, ScannedBytes: 20,
		},
		InvalidCalls: []tooltest.InvalidCall{
			{Name: "missing query", Arguments: json.RawMessage(`{"artifact_id":"tool_artifact_1"}`), WantArgumentCode: "required"},
			{Name: "empty query", Arguments: json.RawMessage(`{"artifact_id":"tool_artifact_1","query":""}`), WantArgumentCode: "min_length"},
			{Name: "too many matches", Arguments: json.RawMessage(`{"artifact_id":"tool_artifact_1","query":"x","max_matches":999}`), WantArgumentCode: "maximum"},
		},
		BadResults:     []tooltest.BadResult{{Name: "missing query", Value: domain.ToolArtifactSearchResult{Artifact: domain.ToolArtifact{ID: "tool_artifact_1"}}}},
		ValidateResult: validateSearchContractResult,
	})
}

func validateReadContractResult(value any) error {
	result, ok := value.(domain.ToolArtifactRead)
	if !ok || result.Artifact.ID == "" || result.NextOffset < result.Offset {
		return errors.New("read result requires artifact identity and a monotonic cursor")
	}
	return nil
}

func validateSearchContractResult(value any) error {
	result, ok := value.(domain.ToolArtifactSearchResult)
	if !ok || result.Artifact.ID == "" || result.Query == "" || result.ScannedBytes < 0 {
		return errors.New("search result requires artifact identity, query, and scan size")
	}
	return nil
}
