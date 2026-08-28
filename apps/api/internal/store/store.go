package store

import (
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

type ErrNotFound string

func (e ErrNotFound) Error() string {
	return string(e) + " not found"
}

func (e ErrNotFound) FailureInfo() failure.Info {
	return failure.Info{Code: "not_found", Source: "store", Category: failure.CategoryNotFound}
}

func IsNotFound(err error) bool {
	var notFound ErrNotFound
	return errors.As(err, &notFound)
}

type ConversationStore interface {
	ListConversations() ([]domain.Conversation, error)
	ListConversationsByWorkspace(workspaceID string) ([]domain.Conversation, error)
	CreateConversation(title string) (domain.Conversation, error)
	CreateConversationInWorkspace(workspaceID string, title string) (domain.Conversation, error)
	GetConversation(id string) (domain.Conversation, bool, error)
	GetConversationInWorkspace(workspaceID string, id string) (domain.Conversation, bool, error)
	DeleteConversation(id string) error
	DeleteConversationInWorkspace(workspaceID string, id string) error
	ListMessages(conversationID string) ([]domain.Message, error)
	ListMessagesInWorkspace(workspaceID string, conversationID string) ([]domain.Message, error)
	AddMessage(conversationID string, role string, content string) (domain.Message, error)
	AddMessageInWorkspace(workspaceID string, conversationID string, role string, content string) (domain.Message, error)
	AddMessageWithCitations(conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error)
	AddMessageWithCitationsInWorkspace(workspaceID string, conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error)
	UpdateConversationTitle(id string, title string) error
	UpdateConversationTitleInWorkspace(workspaceID string, id string, title string) error
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
	CreateRunWithContract(agentID string, conversationID string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error)
	UpdateRunAgent(id string, agentID string) (domain.Run, error)
	UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error)
	UpdateRunHeartbeat(id string) (domain.Run, error)
	ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error)
	GetRun(id string) (domain.Run, bool, error)
	GetRunInWorkspace(workspaceID string, id string) (domain.Run, bool, error)
	ListRuns() ([]domain.Run, error)
	ListRunsByWorkspace(workspaceID string) ([]domain.Run, error)
}

type DelegationStore interface {
	CreateChildRun(domain.ChildRunRequest) (domain.Run, domain.RunDelegation, error)
	UpdateRunDelegation(string, domain.DelegationResult) (domain.RunDelegation, error)
	GetRunDelegation(string) (domain.RunDelegation, bool, error)
	GetParentDelegation(string) (domain.RunDelegation, bool, error)
	ListRunDelegations(string) ([]domain.RunDelegation, error)
	ListActiveRunDelegations() ([]domain.RunDelegation, error)
}

type VerificationStore interface {
	UpdateRunVerificationStatus(id string, status domain.VerificationStatus) (domain.Run, error)
	AppendVerificationRecord(record domain.VerificationRecord) error
	ListVerificationEvidence(runID string) ([]domain.VerificationEvidence, error)
	ListVerificationArtifacts(runID string) ([]domain.VerificationArtifact, error)
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

type RecoveryStore interface {
	ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error)
	ListRunEvents(runID string) ([]domain.RunEvent, error)
	RepairInterruptedRun(domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error)
}

type CheckpointStore interface {
	SaveStageCheckpoint(domain.StageCheckpoint) (domain.StageCheckpoint, error)
	GetStageCheckpoint(runID string, stageID string) (domain.StageCheckpoint, bool, error)
	ListStageCheckpoints(runID string) ([]domain.StageCheckpoint, error)
	BeginToolEffect(domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error)
	CompleteToolEffect(idempotencyKey string, result []byte) (domain.ToolEffectRecord, error)
	MarkToolEffectNeedsReconciliation(idempotencyKey string, errorMessage string) (domain.ToolEffectRecord, error)
	ListToolEffects(runID string) ([]domain.ToolEffectRecord, error)
}

type SessionHistoryStore interface {
	ListMessages(conversationID string) ([]domain.Message, error)
	ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error)
}

type RunUsageStore interface {
	ApplyRunUsage(domain.RunUsageEntry) (domain.RunUsageLedger, bool, error)
	GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error)
}

type ContextCompactionStore interface {
	CreateContextCompaction(domain.ContextCompaction) (domain.ContextCompaction, error)
	CommitContextCompaction(domain.ContextCompaction, domain.RunEvent) (domain.ContextCompaction, domain.RunEvent, error)
	GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error)
}

type TaskStateStore interface {
	GetTaskState(conversationID string) (domain.TaskState, bool, error)
	GetTaskStateRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error)
	ListTaskStateRevisions(conversationID string) ([]domain.TaskStateRevision, error)
	ApplyTaskStatePatch(conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error)
}

type ModelRequestStore interface {
	CreateModelRequestRecord(domain.ModelRequestRecord) (domain.ModelRequestRecord, error)
	ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error)
}

type MemoryStore interface {
	CreateMemory(memory domain.Memory, embedding domain.MemoryEmbedding) (domain.Memory, error)
	SearchMemories(search domain.MemorySearch) ([]domain.RetrievedMemory, error)
}

type MemoryCandidateStore interface {
	CreateMemoryCandidate(domain.MemoryCandidate) (domain.MemoryCandidate, bool, error)
	ListMemoryCandidates(conversationID string) ([]domain.MemoryCandidate, error)
}

type DocumentStore interface {
	CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error)
	ListDocuments() ([]domain.Document, error)
	ListDocumentsByWorkspace(workspaceID string) ([]domain.Document, error)
	GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error)
	GetDocumentInWorkspace(workspaceID string, id string) (domain.Document, []domain.DocumentChunk, bool, error)
	DeleteDocument(id string) error
	DeleteDocumentInWorkspace(workspaceID string, id string) error
	SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error)
	ListDocumentContextChunks(search domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error)
}

// WorkspaceStore is the only persistence surface exposed to HTTP handlers for
// Workspace-owned resources. Run-owned child records are authorized through
// the parent Run before the backend operation is executed.
type WorkspaceStore interface {
	ListConversations() ([]domain.Conversation, error)
	CreateConversation(title string) (domain.Conversation, error)
	GetConversation(id string) (domain.Conversation, bool, error)
	DeleteConversation(id string) error
	ListMessages(conversationID string) ([]domain.Message, error)
	AddMessage(conversationID string, role string, content string) (domain.Message, error)
	AddMessageWithCitations(conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error)
	UpdateConversationTitle(id string, title string) error
	GetRun(id string) (domain.Run, bool, error)
	ListRuns() ([]domain.Run, error)
	ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error)
	ListRunEvents(runID string) ([]domain.RunEvent, error)
	CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error)
	GetRunReplay(runID string) (domain.RunReplay, bool, error)
	GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error)
	GetTaskState(conversationID string) (domain.TaskState, bool, error)
	GetTaskStateRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error)
	ListTaskStateRevisions(conversationID string) ([]domain.TaskStateRevision, error)
	ApplyTaskStatePatch(conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error)
	ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error)
	UpdateRunVerificationStatus(runID string, status domain.VerificationStatus) (domain.Run, error)
	ListDocuments() ([]domain.Document, error)
	GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error)
	DeleteDocument(id string) error
}

type WorkspaceStoreProvider interface {
	ForWorkspace(domain.WorkspaceScope) WorkspaceStore
}

// Store is the application-level persistence contract. Consumers should depend
// on the smallest capability interface above that satisfies their use case.
type Store interface {
	WorkspaceStoreProvider
	ConversationStore
	AgentStore
	RunStore
	DelegationStore
	CollaborationStore
	RunEventStore
	RecoveryStore
	CheckpointStore
	SessionHistoryStore
	RunUsageStore
	VerificationStore
	ContextCompactionStore
	TaskStateStore
	ModelRequestStore
	MemoryStore
	MemoryCandidateStore
	DocumentStore
}
