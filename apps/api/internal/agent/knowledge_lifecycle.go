package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
)

// A checkpoint freezes execution, not permission to reuse a withdrawn source.
// The same guard runs before recovery ownership changes and subsequent Turns.
func (r *Runtime) validateKnowledgeEvidence(run domain.Run) error {
	events, err := r.store.ListRunEvents(run.ID)
	if err != nil {
		return err
	}
	return eligibility.ValidateSources(run.RuntimeSnapshot, run.WorkspaceID, rag.RunCitationReservations(events), func(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
		return r.store.GetDocumentInWorkspace(run.WorkspaceID, id)
	})
}
