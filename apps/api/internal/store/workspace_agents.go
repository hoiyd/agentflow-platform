package store

import "agentflow-platform/apps/api/internal/domain"

func (s workspaceStore) ListAgents() ([]domain.Agent, error) {
	return s.backend.ListAgentsByWorkspace(s.workspaceID)
}
func (s workspaceStore) GetAgent(id string) (domain.Agent, bool, error) {
	return s.backend.GetAgentInWorkspace(s.workspaceID, id)
}
func (s workspaceStore) CreateAgent(agent domain.Agent) (domain.Agent, error) {
	// Request IDs and ownership are never accepted as authority, including copies.
	agent.ID = NewID("agent")
	agent.WorkspaceID = s.workspaceID
	agent.IsTemplate = false
	return s.backend.CreateAgent(agent)
}
func (s workspaceStore) UpdateAgent(agent domain.Agent) (domain.Agent, error) {
	existing, ok, err := s.GetAgent(agent.ID)
	if err != nil {
		return domain.Agent{}, err
	}
	if !ok || existing.IsTemplate || existing.Archived {
		return domain.Agent{}, ErrNotFound("editable agent")
	}
	agent.WorkspaceID = existing.WorkspaceID
	agent.IsTemplate = false
	return s.backend.UpdateAgent(agent)
}
func (s workspaceStore) ArchiveAgent(id string) error {
	existing, ok, err := s.GetAgent(id)
	if err != nil {
		return err
	}
	if !ok || existing.IsTemplate {
		return ErrNotFound("editable agent")
	}
	return s.backend.ArchiveAgent(id)
}
