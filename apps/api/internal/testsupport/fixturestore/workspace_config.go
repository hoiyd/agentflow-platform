package fixturestore

import (
	"slices"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (s *Store) CheckWorkspaceExecution(string) error { return nil }

func (s *Store) ListAgentsByWorkspace(workspaceID string) ([]domain.Agent, error) {
	items, err := s.ListAgents()
	result := []domain.Agent{}
	for _, item := range items {
		if item.WorkspaceID == workspaceID || item.IsTemplate {
			result = append(result, item)
		}
	}
	return result, err
}
func (s *Store) GetAgentInWorkspace(workspaceID, id string) (domain.Agent, bool, error) {
	item, ok, err := s.GetAgent(id)
	return item, ok && (item.WorkspaceID == workspaceID || item.IsTemplate), err
}
func (s *Store) EnsureWorkspaceToolConfig(workspaceID string, initial []string) (domain.WorkspaceToolConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolConfigs == nil {
		s.toolConfigs = map[string]domain.WorkspaceToolConfig{}
	}
	config, ok := s.toolConfigs[workspaceID]
	if !ok {
		config = domain.WorkspaceToolConfig{WorkspaceID: workspaceID, AllowedTools: store.NormalizeTools(initial), Revision: 1}
		s.toolConfigs[workspaceID] = config
	}
	config.AllowedTools = slices.Clone(config.AllowedTools)
	return config, nil
}
func (s *Store) SetWorkspaceToolEnabled(workspaceID, name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, ok := s.toolConfigs[workspaceID]
	if !ok {
		return store.ErrNotFound("workspace tool config")
	}
	config.AllowedTools = slices.DeleteFunc(slices.Clone(config.AllowedTools), func(value string) bool { return value == name })
	if enabled {
		config.AllowedTools = append(config.AllowedTools, name)
	}
	config.Revision++
	s.toolConfigs[workspaceID] = config
	return nil
}
