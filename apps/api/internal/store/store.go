package store

import (
	"errors"
	"time"

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

type ConversationStore interface {
	ListConversations() ([]domain.Conversation, error)
	CreateConversation(title string) (domain.Conversation, error)
	GetConversation(id string) (domain.Conversation, bool, error)
	DeleteConversation(id string) error
	ListMessages(conversationID string) ([]domain.Message, error)
	AddMessage(conversationID string, role string, content string) (domain.Message, error)
	UpdateConversationTitle(id string, title string) error
}

type AgentStore interface {
	ListAgents() ([]domain.Agent, error)
	CreateAgent(agent domain.Agent) (domain.Agent, error)
	GetAgent(id string) (domain.Agent, bool, error)
	UpdateAgent(agent domain.Agent) (domain.Agent, error)
	ArchiveAgent(id string) error
	GetDefaultAgent() (domain.Agent, bool, error)
}

type RunStore interface {
	CreateRun(agentID string, conversationID string, snapshot domain.RuntimeSnapshot) (domain.Run, error)
	UpdateRunAgent(id string, agentID string) (domain.Run, error)
	UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error)
	UpdateRunHeartbeat(id string) (domain.Run, error)
	ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error)
	GetRun(id string) (domain.Run, bool, error)
	ListRuns() ([]domain.Run, error)
}

type CollaborationStore interface {
	CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error)
	UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error)
	UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error)
	ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error)
}

type RunEventStore interface {
	CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error)
	ListRunEvents(runID string) ([]domain.RunEvent, error)
	GetRunTraceSummary(runID string) (domain.RunTraceSummary, error)
	GetRunReplay(runID string) (domain.RunReplay, bool, error)
}

type ContextCompactionStore interface {
	CreateContextCompaction(domain.ContextCompaction) (domain.ContextCompaction, error)
	GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error)
}

type MemoryStore interface {
	CreateMemory(memory domain.Memory, embedding domain.MemoryEmbedding) (domain.Memory, error)
	SearchMemories(search domain.MemorySearch) ([]domain.RetrievedMemory, error)
}

type MemoryCandidateStore interface {
	CreateMemoryCandidate(domain.MemoryCandidate) (domain.MemoryCandidate, bool, error)
	ListMemoryCandidates(conversationID string) ([]domain.MemoryCandidate, error)
}

// LegacyMemoryStore is used by the explicit message-memory cleanup command.
// It is separate from runtime recall so migration operations cannot leak into
// normal Turn execution paths.
type LegacyMemoryStore interface {
	ListLegacyMessageMemories() ([]domain.Memory, error)
	DeleteLegacyMessageMemories(ids []string) (int, error)
}

type DocumentStore interface {
	CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error)
	ListDocuments() ([]domain.Document, error)
	GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error)
	DeleteDocument(id string) error
	SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
}

// Store is the application-level persistence contract. Consumers should depend
// on the smallest capability interface above that satisfies their use case.
type Store interface {
	ConversationStore
	AgentStore
	RunStore
	CollaborationStore
	RunEventStore
	ContextCompactionStore
	MemoryStore
	MemoryCandidateStore
	LegacyMemoryStore
	DocumentStore
}
