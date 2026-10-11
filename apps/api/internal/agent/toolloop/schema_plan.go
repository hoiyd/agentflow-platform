package toolloop

import (
	"encoding/json"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

type schemaPlan struct {
	mode        string
	fullTokens  int
	definitions []map[string]any
	core        []domain.ToolSchemaIdentity
	index       tool.DiscoveryIndex
	search      tool.Descriptor
}

func schemaTokens(definitions []map[string]any) int {
	data, _ := json.Marshal(definitions)
	return contextassembly.EstimateTokens(string(data))
}

// InitialSchemaTokens shares the actual initial wire plan with model routing.
// Subsequent activation is still checked by the Context Assembler on every call.
func InitialSchemaTokens(catalog *tool.Catalog, config domain.ToolSchemaConfig) int {
	return schemaTokens(planSchemas(catalog, config.Normalize()).definitions)
}

func planSchemas(catalog *tool.Catalog, config domain.ToolSchemaConfig) schemaPlan {
	full := catalog.Definitions()
	plan := schemaPlan{mode: "eager", definitions: full, fullTokens: schemaTokens(full)}
	if len(full) == 0 || config.Mode == "eager" || config.Mode == "auto" && plan.fullTokens < config.SchemaTokenThreshold {
		return plan
	}
	plan.index = catalog.DiscoveryIndex()
	plan.search = tool.Descriptor{
		Name:        discoveryToolName,
		Description: "Search authorized tools by name, description, or parameter. Matches load their full schemas in the NEXT request. Do not call an unloaded tool in this batch. Naming groups do not grant access.\n" + plan.index.Summary(4096),
		Parameters:  tool.ObjectSchema(map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}}, []string{"query"}),
	}
	definitions := []map[string]any{plan.search.Definition()}
	for _, name := range []string{"get_current_time", "skill_load", "skill_read", "update_task_state"} {
		if binding, ready := catalog.ResolveReady(name); ready {
			plan.core = append(plan.core, domain.ToolSchemaIdentity{Name: name, Revision: binding.Descriptor.DefinitionRevision})
			definitions = append(definitions, binding.Descriptor.Definition())
		}
	}
	// Search metadata itself has a cost. Auto must not increase initial Schema
	// cost just because a catalog crossed an arbitrary tuning threshold.
	if config.Mode == "auto" && schemaTokens(definitions) >= plan.fullTokens {
		return plan
	}
	plan.mode, plan.definitions = "lazy", definitions
	return plan
}
