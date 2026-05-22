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
	DeleteConversation(id string) error
	ListMessages(conversationID string) ([]domain.Message, error)
	AddMessage(conversationID string, role string, content string) (domain.Message, error)
	UpdateConversationTitle(id string, title string) error
	ListAgents() ([]domain.Agent, error)
	CreateAgent(agent domain.Agent) (domain.Agent, error)
	GetAgent(id string) (domain.Agent, bool, error)
	UpdateAgent(agent domain.Agent) (domain.Agent, error)
	GetDefaultAgent() (domain.Agent, bool, error)
	CreateRun(agentID string, conversationID string) (domain.Run, error)
	UpdateRunAgent(id string, agentID string) (domain.Run, error)
	UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error)
	GetRun(id string) (domain.Run, bool, error)
	ListRuns() ([]domain.Run, error)
	CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error)
	UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error)
	UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error)
	ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error)
}
