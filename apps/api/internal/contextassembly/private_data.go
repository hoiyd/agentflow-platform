package contextassembly

import "agentflow-platform/apps/api/internal/domain"

// ContainsPrivateData uses trusted selection metadata, not a content classifier.
// Current user input and editable Agent instructions have no inferred privacy
// label; retrieved history and internal records are conservatively non-public.
func ContainsPrivateData(manifest domain.ContextManifest) bool {
	for _, entry := range manifest.Entries {
		if !entry.Selected {
			continue
		}
		switch entry.Source {
		case SourceHistory, SourceHistorySearch, SourceMemory, SourceKnowledge, SourceCompaction, SourceTaskState:
			return true
		}
	}
	return false
}
