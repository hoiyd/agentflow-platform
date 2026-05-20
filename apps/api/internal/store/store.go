package store

import (
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

type ErrNotFound string

func (e ErrNotFound) Error() string {
	return string(e) + " not found"
}

func IsNotFound(err error) bool {
	var notFound ErrNotFound
	return errors.As(err, &notFound)
}

type Store interface {
	ListConversations() ([]domain.Conversation, error)
	CreateConversation(title string) (domain.Conversation, error)
	GetConversation(id string) (domain.Conversation, bool, error)
	ListMessages(conversationID string) ([]domain.Message, error)
	AddMessage(conversationID string, role string, content string) (domain.Message, error)
	UpdateConversationTitle(id string, title string) error
	ListAgents() ([]domain.Agent, error)
	CreateAgent(agent domain.Agent) (domain.Agent, error)
	GetAgent(id string) (domain.Agent, bool, error)
	UpdateAgent(agent domain.Agent) (domain.Agent, error)
	GetDefaultAgent() (domain.Agent, bool, error)
	CreateRun(agentID string, conversationID string) (domain.Run, error)
	UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error)
	GetRun(id string) (domain.Run, bool, error)
	ListRuns() ([]domain.Run, error)
}
