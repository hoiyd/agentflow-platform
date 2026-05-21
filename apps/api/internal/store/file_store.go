package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type FileStore struct {
	path string
	mu   sync.RWMutex
	data fileData
}

type fileData struct {
	Conversations []domain.Conversation `json:"conversations"`
	Messages      []domain.Message      `json:"messages"`
	Agents        []domain.Agent        `json:"agents"`
	Runs          []domain.Run          `json:"runs"`
}

func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{path: path}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) ListConversations() ([]domain.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.Conversations) == 0 {
		return []domain.Conversation{}, nil
	}
	items := append([]domain.Conversation(nil), s.data.Conversations...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *FileStore) CreateConversation(title string) (domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	conversation := domain.Conversation{
		ID:        newID("conv"),
		Title:     normalizeTitle(title),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.data.Conversations = append(s.data.Conversations, conversation)
	return conversation, s.saveLocked()
}

func (s *FileStore) GetConversation(id string) (domain.Conversation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Conversations {
		if item.ID == id {
			return item, true, nil
		}
	}
	return domain.Conversation{}, false, nil
}

func (s *FileStore) ListMessages(conversationID string) ([]domain.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	messages := []domain.Message{}
	for _, message := range s.data.Messages {
		if message.ConversationID == conversationID {
			messages = append(messages, message)
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages, nil
}

func (s *FileStore) AddMessage(conversationID string, role string, content string) (domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasConversationLocked(conversationID) {
		return domain.Message{}, errors.New("conversation not found")
	}

	now := time.Now().UTC()
	message := domain.Message{
		ID:             newID("msg"),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		CreatedAt:      now,
	}
	s.data.Messages = append(s.data.Messages, message)
	for i := range s.data.Conversations {
		if s.data.Conversations[i].ID == conversationID {
			s.data.Conversations[i].UpdatedAt = now
			break
		}
	}
	return message, s.saveLocked()
}

func (s *FileStore) UpdateConversationTitle(id string, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Conversations {
		if s.data.Conversations[i].ID == id {
			s.data.Conversations[i].Title = normalizeTitle(title)
			s.data.Conversations[i].UpdatedAt = time.Now().UTC()
			return s.saveLocked()
		}
	}
	return errors.New("conversation not found")
}

func (s *FileStore) ListAgents() ([]domain.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := append([]domain.Agent(nil), s.data.Agents...)
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
			return item, true, nil
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
			agent.CreatedAt = s.data.Agents[i].CreatedAt
			agent.UpdatedAt = time.Now().UTC()
			s.data.Agents[i] = agent
			return agent, s.saveLocked()
		}
	}
	return domain.Agent{}, errors.New("agent not found")
}

func (s *FileStore) GetDefaultAgent() (domain.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Agents {
		if item.ID == "agent_planner" {
			return item, true, nil
		}
	}
	if len(s.data.Agents) > 0 {
		return s.data.Agents[0], true, nil
	}
	return domain.Agent{}, false, nil
}

func (s *FileStore) CreateRun(agentID string, conversationID string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasAgentLocked(agentID) {
		return domain.Run{}, errors.New("agent not found")
	}
	if !s.hasConversationLocked(conversationID) {
		return domain.Run{}, errors.New("conversation not found")
	}

	now := time.Now().UTC()
	run := domain.Run{
		ID:             newID("run"),
		AgentID:        agentID,
		ConversationID: conversationID,
		Status:         domain.RunQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.data.Runs = append(s.data.Runs, run)
	return run, s.saveLocked()
}

func (s *FileStore) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			now := time.Now().UTC()
			s.data.Runs[i].Status = status
			s.data.Runs[i].Error = strings.TrimSpace(errorMessage)
			s.data.Runs[i].UpdatedAt = now
			if status == domain.RunRunning && s.data.Runs[i].StartedAt == nil {
				s.data.Runs[i].StartedAt = &now
			}
			if status == domain.RunCompleted || status == domain.RunFailed {
				s.data.Runs[i].CompletedAt = &now
			}
			return s.data.Runs[i], s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) GetRun(id string) (domain.Run, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Runs {
		if item.ID == id {
			return item, true, nil
		}
	}
	return domain.Run{}, false, nil
}

func (s *FileStore) ListRuns() ([]domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.Runs) == 0 {
		return []domain.Run{}, nil
	}
	items := append([]domain.Run(nil), s.data.Runs...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileStore) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	bytes, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.data = emptyFileData()
		s.seedDefaultAgentsLocked()
		return s.saveLocked()
	}
	if err != nil {
		return err
	}
	if len(bytes) == 0 {
		s.data = emptyFileData()
		s.seedDefaultAgentsLocked()
		return nil
	}
	if err := json.Unmarshal(bytes, &s.data); err != nil {
		return err
	}
	s.normalizeLoadedDataLocked()
	if len(s.data.Agents) == 0 {
		s.seedDefaultAgentsLocked()
		return s.saveLocked()
	}
	if s.migrateDefaultAgentsLocked() {
		return s.saveLocked()
	}
	return nil
}

func (s *FileStore) saveLocked() error {
	bytes, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, bytes, 0o644)
}

func (s *FileStore) hasConversationLocked(id string) bool {
	for _, item := range s.data.Conversations {
		if item.ID == id {
			return true
		}
	}
	return false
}

func (s *FileStore) hasAgentLocked(id string) bool {
	for _, item := range s.data.Agents {
		if item.ID == id {
			return true
		}
	}
	return false
}

func emptyFileData() fileData {
	return fileData{
		Conversations: []domain.Conversation{},
		Messages:      []domain.Message{},
		Agents:        []domain.Agent{},
		Runs:          []domain.Run{},
	}
}

func (s *FileStore) normalizeLoadedDataLocked() {
	if s.data.Conversations == nil {
		s.data.Conversations = []domain.Conversation{}
	}
	if s.data.Messages == nil {
		s.data.Messages = []domain.Message{}
	}
	if s.data.Agents == nil {
		s.data.Agents = []domain.Agent{}
	}
	if s.data.Runs == nil {
		s.data.Runs = []domain.Run{}
	}
}

func (s *FileStore) seedDefaultAgentsLocked() {
	s.data.Agents = defaultAgents(time.Now().UTC())
}

func defaultAgents(now time.Time) []domain.Agent {
	return []domain.Agent{
		{
			ID:           "agent_research",
			Name:         "Field Researcher",
			Description:  "Investigates people, places, products, and market context, then separates verified facts from open questions.",
			SystemPrompt: "You are AgentFlow's Field Researcher. Gather context with search and remote data tools, cite tool-derived facts when available, compare sources carefully, and call out uncertainty instead of filling gaps with guesses.",
			Tools:        []string{"mock_web_search", "smartapis__smartagent_discovery_capabilities", "smartapis__smartagent_places_search", "smartapis__smartagent_catalog_list_plans"},
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
			Tools:        []string{"calculator", "smartapis__smartagent_discovery_capabilities"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_planner",
			Name:         "Narrative Strategist",
			Description:  "Shapes messy goals into audience-aware briefs, storylines, launch plans, and decision-ready next actions.",
			SystemPrompt: "You are AgentFlow's Narrative Strategist. Clarify the audience, intent, and constraints behind a request. Convert goals into concise briefs, storylines, communication plans, or ordered next actions with dependencies and risks.",
			Tools:        []string{"get_current_time", "mock_web_search"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
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

func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "New conversation"
	}
	runes := []rune(title)
	if len(runes) > 48 {
		return string(runes[:48]) + "..."
	}
	return title
}

func newID(prefix string) string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return prefix + "_" + time.Now().UTC().Format("20060102150405")
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}
