package toolloop

import (
	"encoding/json"
	"fmt"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Restore exposure from the existing event log, including earlier Stages and
// process restarts. No second persisted Run state machine is introduced.
func privateRunEvidence(events []domain.RunEvent, catalog *tool.Catalog) (bool, error) {
	for _, item := range events {
		switch item.Type {
		case domain.EventContextAssembled:
			encoded, err := json.Marshal(item.Payload["manifest"])
			if err != nil {
				return false, fmt.Errorf("decode stored Context Manifest: %w", err)
			}
			var manifest domain.ContextManifest
			if err := json.Unmarshal(encoded, &manifest); err != nil || manifest.ID == "" {
				return false, fmt.Errorf("stored Context Manifest is invalid")
			}
			if contextassembly.ContainsPrivateData(manifest) {
				return true, nil
			}
		case domain.EventToolCompleted, domain.EventToolFailed:
			if private, ok := item.Payload["private_data"].(bool); ok {
				if private {
					return true, nil
				}
				continue
			}
			// Legacy evidence has no classification. Consult the trusted local
			// descriptor; an unknown prior Binding cannot prove a public result.
			name, _ := item.Payload["tool_name"].(string)
			binding, ok := catalog.Installed(name)
			if !ok || policy.UsesPrivateResources(binding.Descriptor.Security) {
				return true, nil
			}
		}
	}
	return false, nil
}
