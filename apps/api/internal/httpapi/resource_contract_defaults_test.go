package httpapi

import (
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Go nil collections and decimal BIGINT IDs are wire contracts, not UI guesses.
func TestSerializedResourceContractDefaults(t *testing.T) {
	contract := loadResourceContract(t)
	const workspace = "9007199254740993"
	for name, value := range map[string]any{
		"Memory":                   domain.Memory{ID: "memory", WorkspaceID: workspace},
		"MemoryDetail":             domain.MemoryDetail{Memory: domain.Memory{ID: "memory", WorkspaceID: workspace}},
		"RetrievedMemory":          domain.RetrievedMemory{},
		"ToolArtifactRead":         domain.ToolArtifactRead{},
		"ToolArtifactSearchResult": domain.ToolArtifactSearchResult{},
		"KnowledgeDocumentChunk":   domain.DocumentChunk{},
		"RetrievedDocumentChunk":   domain.RetrievedDocumentChunk{},
		"DocumentSearchResponse":   domain.DocumentSearchResponse{},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			contract.validate(t, "/components/schemas/"+name, encoded)
		})
	}
	for _, name := range []string{"Memory", "KnowledgeDocument", "TaskStateRevision"} {
		t.Run(name+"/numeric-workspace-rejected", func(t *testing.T) {
			schema, err := contract.compiler.Compile("https://agentflow.test/openapi#/components/schemas/" + name + "/properties/workspace_id")
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(workspace); err != nil {
				t.Fatal(err)
			}
			if schema.Validate(float64(9007199254740992)) == nil {
				t.Fatal("numeric BIGINT Workspace ID accepted")
			}
		})
	}
}
