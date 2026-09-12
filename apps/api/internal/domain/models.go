package domain

import (
	"strings"
	"time"
)

const RAGPromptGuardPolicyVersion = "rag-prompt-guard-v2"
const RAGCitationProtocolVersion = "rag-citation-v1"
const DefaultWorkspaceID = "default_workspace"

// WorkspaceScope is the mandatory namespace carried by user-facing storage
// operations. Its ID is normalized at construction and can never be empty.
type WorkspaceScope struct{ id string }

func NewWorkspaceScope(workspaceID string) WorkspaceScope {
	return WorkspaceScope{id: NormalizeWorkspaceID(workspaceID)}
}

func (s WorkspaceScope) ID() string {
	return NormalizeWorkspaceID(s.id)
}

func NormalizeWorkspaceID(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || workspaceID == "default" {
		return DefaultWorkspaceID
	}
	return workspaceID
}

type Conversation struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Message struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspace_id"`
	ConversationID string        `json:"conversation_id"`
	Role           string        `json:"role"`
	Content        string        `json:"content"`
	Citations      []RAGCitation `json:"citations,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
}

type Agent struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	SystemPrompt     string            `json:"system_prompt"`
	RoutingHints     AgentRoutingHints `json:"routing_hints"`
	Tools            []string          `json:"tools"`
	MemoryEnabled    bool              `json:"memory_enabled"`
	RetrievalEnabled bool              `json:"retrieval_enabled"`
	Executor         string            `json:"executor"`
	Archived         bool              `json:"archived,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// AgentRoutingHints are declarative ranking signals owned by an Agent profile.
// They improve deterministic routing but never grant tools or other authority.
type AgentRoutingHints struct {
	Capabilities []string `json:"capabilities"`
	TaskExamples []string `json:"task_examples"`
	Exclusions   []string `json:"exclusions"`
}

const (
	DefaultAgentExecutor = "native"
)

func IsDefaultAgentID(id string) bool {
	switch strings.TrimSpace(id) {
	case "agent_research", "agent_coding", "agent_data", "agent_planner":
		return true
	default:
		return false
	}
}

func NormalizeAgentConfig(agent Agent) Agent {
	missingConfig := !agent.MemoryEnabled && !agent.RetrievalEnabled && strings.TrimSpace(agent.Executor) == ""
	if strings.TrimSpace(agent.Executor) == "" {
		agent.Executor = DefaultAgentExecutor
	}
	if missingConfig {
		agent.MemoryEnabled = true
		agent.RetrievalEnabled = true
	}
	agent.RoutingHints = NormalizeAgentRoutingHints(agent.RoutingHints)
	return agent
}

func NormalizeAgentRoutingHints(hints AgentRoutingHints) AgentRoutingHints {
	hints.Capabilities = normalizeUniqueStrings(hints.Capabilities)
	hints.TaskExamples = normalizeUniqueStrings(hints.TaskExamples)
	hints.Exclusions = normalizeUniqueStrings(hints.Exclusions)
	return hints
}

func normalizeUniqueStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}
