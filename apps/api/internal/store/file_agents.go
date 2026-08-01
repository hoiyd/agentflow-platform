package store

import (
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) ListAgents() ([]domain.Agent, error) {
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

func (s *FileStore) CreateAgent(agent domain.Agent) (domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	agent.ID = strings.TrimSpace(agent.ID)
	if agent.ID == "" {
		agent.ID = newID("agent")
	}
	agent.Name = strings.TrimSpace(agent.Name)
	if agent.Name == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}
	agent.Description = strings.TrimSpace(agent.Description)
	agent.SystemPrompt = strings.TrimSpace(agent.SystemPrompt)
	agent.Tools = normalizeTools(agent.Tools)
	agent = domain.NormalizeAgentConfig(agent)
	agent.Archived = false
	agent.CreatedAt = now
	agent.UpdatedAt = now
	s.data.Agents = append(s.data.Agents, agent)
	return agent, s.saveLocked()
}

func (s *FileStore) GetAgent(id string) (domain.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Agents {
		if item.ID == id {
			return domain.NormalizeAgentConfig(item), true, nil
		}
	}
	return domain.Agent{}, false, nil
}

func (s *FileStore) UpdateAgent(agent domain.Agent) (domain.Agent, error) {
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
			agent.Tools = normalizeTools(agent.Tools)
			agent = domain.NormalizeAgentConfig(agent)
			agent.Archived = s.data.Agents[i].Archived
			agent.CreatedAt = s.data.Agents[i].CreatedAt
			agent.UpdatedAt = time.Now().UTC()
			s.data.Agents[i] = agent
			return agent, s.saveLocked()
		}
	}
	return domain.Agent{}, errors.New("agent not found")
}

func (s *FileStore) ArchiveAgent(id string) error {
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
			return s.saveLocked()
		}
	}
	return errors.New("agent not found")
}

func (s *FileStore) GetDefaultAgent() (domain.Agent, bool, error) {
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

func (s *FileStore) seedDefaultAgentsLocked() {
	s.data.Agents = defaultAgents(time.Now().UTC())
}

func defaultAgents(now time.Time) []domain.Agent {
	agents := []domain.Agent{
		{
			ID:           "agent_research",
			Name:         "Field Researcher",
			Description:  "Analyzes supplied research material, separates supported facts from open questions, and identifies evidence gaps.",
			SystemPrompt: "You are AgentFlow's Field Researcher. Analyze the supplied context, distinguish supported facts from assumptions, compare evidence carefully, and call out uncertainty instead of filling gaps with guesses.",
			Tools:        nil,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_coding",
			Name:         "Systems Builder",
			Description:  "Turns implementation requests into concrete technical steps, debugging hypotheses, and maintainable code changes.",
			SystemPrompt: "You are AgentFlow's Systems Builder. Focus on software behavior, interfaces, edge cases, and implementation tradeoffs. Give direct engineering guidance, identify risks, and prefer concrete next steps over broad advice.",
			Tools:        []string{"calculator", "get_current_time"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_data",
			Name:         "Operations Analyst",
			Description:  "Evaluates budgets, schedules, capacity, and tradeoffs with explicit assumptions and calculation-backed reasoning.",
			SystemPrompt: "You are AgentFlow's Operations Analyst. Treat questions as operational decisions involving cost, time, capacity, or prioritization. Show assumptions, calculate carefully, compare scenarios, and make the practical tradeoff visible.",
			Tools:        []string{"calculator"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_planner",
			Name:         "Narrative Strategist",
			Description:  "Shapes messy goals into audience-aware briefs, storylines, launch plans, and decision-ready next actions.",
			SystemPrompt: "You are AgentFlow's Narrative Strategist. Clarify the audience, intent, and constraints behind a request. Convert goals into concise briefs, storylines, communication plans, or ordered next actions with dependencies and risks.",
			Tools:        []string{"get_current_time"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	for index := range agents {
		agents[index] = domain.NormalizeAgentConfig(agents[index])
	}
	return agents
}

func (s *FileStore) migrateDefaultAgentsLocked() bool {
	now := time.Now().UTC()
	defaults := defaultAgents(now)
	defaultByID := make(map[string]domain.Agent, len(defaults))
	for _, agent := range defaults {
		defaultByID[agent.ID] = agent
	}

	changed := false
	for i := range s.data.Agents {
		next, ok := defaultByID[s.data.Agents[i].ID]
		if !ok {
			continue
		}
		if updateDefaultAgentText(&s.data.Agents[i], next) {
			s.data.Agents[i].UpdatedAt = now
			changed = true
		}
	}
	return changed
}

func updateDefaultAgentText(agent *domain.Agent, next domain.Agent) bool {
	changed := false
	old := oldDefaultAgentText(agent.ID)
	if agent.Name == "" || agent.Name == old.Name {
		agent.Name = next.Name
		changed = true
	}
	if agent.Description == "" || agent.Description == old.Description {
		agent.Description = next.Description
		changed = true
	}
	if agent.SystemPrompt == "" || agent.SystemPrompt == old.SystemPrompt {
		agent.SystemPrompt = next.SystemPrompt
		changed = true
	}
	return changed
}

func oldDefaultAgentText(id string) domain.Agent {
	switch id {
	case "agent_research":
		return domain.Agent{
			Name:         "Research Agent",
			Description:  "Finds, compares, and summarizes information using available search and remote data tools.",
			SystemPrompt: "You are AgentFlow's Research Agent. Be precise, cite tool-derived facts when available, compare options carefully, and say when evidence is missing.",
		}
	case "agent_coding":
		return domain.Agent{
			Name:         "Coding Assistant Agent",
			Description:  "Helps reason about implementation details, debugging steps, and code changes.",
			SystemPrompt: "You are AgentFlow's Coding Assistant Agent. Give direct engineering guidance, identify risks, and prefer concrete implementation steps.",
		}
	case "agent_data":
		return domain.Agent{
			Name:         "Data Analyst Agent",
			Description:  "Analyzes structured information, calculations, and data-oriented questions.",
			SystemPrompt: "You are AgentFlow's Data Analyst Agent. Work carefully with numbers, show assumptions, and use tools for calculations when useful.",
		}
	case "agent_planner":
		return domain.Agent{
			Name:         "Planner Agent",
			Description:  "Breaks ambiguous requests into ordered plans and tracks next actions.",
			SystemPrompt: "You are AgentFlow's Planner Agent. Convert goals into clear, ordered plans with dependencies, risks, and next actions.",
		}
	default:
		return domain.Agent{}
	}
}

func normalizeTools(items []string) []string {
	seen := map[string]bool{}
	tools := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			tools = append(tools, item)
		}
	}
	sort.Strings(tools)
	return tools
}
