package domain

import (
	"strings"
	"time"
)

const RAGPromptGuardPolicyVersion = "rag-prompt-guard-v1"
const RAGCitationProtocolVersion = "rag-citation-v1"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversation_id"`
	Role           string        `json:"role"`
	Content        string        `json:"content"`
	Citations      []RAGCitation `json:"citations,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
}

type Agent struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	SystemPrompt     string    `json:"system_prompt"`
	Tools            []string  `json:"tools"`
	MemoryEnabled    bool      `json:"memory_enabled"`
	RetrievalEnabled bool      `json:"retrieval_enabled"`
	Executor         string    `json:"executor"`
	Archived         bool      `json:"archived,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
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
	return agent
}
