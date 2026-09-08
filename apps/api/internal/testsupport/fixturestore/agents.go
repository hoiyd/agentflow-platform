package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *Store) ListAgents() ([]domain.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := []domain.Agent{}
	for _, item := range s.data.Agents {
		if item.Archived {
			continue
		}
		items = append(items, item)
	}
	for index := range items {
		items[index] = domain.NormalizeAgentConfig(items[index])
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Store) CreateAgent(agent domain.Agent) (domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	agent.ID = strings.TrimSpace(agent.ID)
	if agent.ID == "" {
		agent.ID = store.NewID("agent")
	}
	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}
	agent.Description = strings.TrimSpace(agent.Description)
	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	agent.Tools = store.NormalizeTools(agent.Tools)
	agent = domain.NormalizeAgentConfig(agent)
	agent.Archived = false
	agent.CreatedAt = now
	agent.UpdatedAt = now
	s.data.Agents = append(s.data.Agents, agent)
	return agent, nil
}

func (s *Store) GetAgent(id string) (domain.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Agents {
		if item.ID == id {
			return domain.NormalizeAgentConfig(item), true, nil
		}
	}
	return domain.Agent{}, false, nil
}

func (s *Store) UpdateAgent(agent domain.Agent) (domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Agents {
		if s.data.Agents[i].ID == agent.ID {
			agent.Name = strings.TrimSpace(agent.Name)
			if agent.Name == "" {
				return domain.Agent{}, errors.New("agent name is required")
			}
			agent.Description = strings.TrimSpace(agent.Description)
			agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
			agent.Tools = store.NormalizeTools(agent.Tools)
			agent = domain.NormalizeAgentConfig(agent)
			agent.Archived = s.data.Agents[i].Archived
			agent.CreatedAt = s.data.Agents[i].CreatedAt
			agent.UpdatedAt = time.Now().UTC()
			s.data.Agents[i] = agent
			return agent, nil
		}
	}
	return domain.Agent{}, errors.New("agent not found")
}

func (s *Store) ArchiveAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if domain.IsDefaultAgentID(id) {
		return errors.New("default agents cannot be archived")
	}
	for i := range s.data.Agents {
		if s.data.Agents[i].ID == id {
			s.data.Agents[i].Archived = true
			s.data.Agents[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return errors.New("agent not found")
}

func (s *Store) GetDefaultAgent() (domain.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Agents {
		if item.ID == "agent_planner" && !item.Archived {
			return domain.NormalizeAgentConfig(item), true, nil
		}
	}
	for _, item := range s.data.Agents {
		if !item.Archived {
			return domain.NormalizeAgentConfig(item), true, nil
		}
	}
	return domain.Agent{}, false, nil
}

func (s *Store) seedDefaultAgentsLocked() {
	s.data.Agents = store.DefaultAgents(time.Now().UTC())
}
