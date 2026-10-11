package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

func TestModelRequirementsEstimateVisibleNotDeferredSchemas(t *testing.T) {
	var bindings []tool.Binding
	for i := range 20 {
		bindings = append(bindings, tool.Binding{Descriptor: tool.Descriptor{Name: fmt.Sprintf("report_%02d", i), Description: strings.Repeat("Describe report fields. ", 100), Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
	}
	catalog, err := tool.NewCatalog(bindings...)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly.ContextWindowTokens = 128000
	snapshot.ToolSchema = &domain.ToolSchemaConfig{Mode: "lazy", SchemaTokenThreshold: 2048}
	request := turn.Request{Catalog: catalog, ModelMode: turn.ModelModeAgentStream, Input: "Produce a report"}
	lazy := modelRequirements(request, &snapshot)
	snapshot.ToolSchema.Mode = "eager"
	eager := modelRequirements(request, &snapshot)
	if !lazy.ToolCalling || lazy.EstimatedInputTokens >= 2048 || eager.EstimatedInputTokens < 2048 {
		t.Fatalf("routing estimate counted deferred Schemas: lazy=%#v eager=%#v", lazy, eager)
	}
}
