// Package fixturestore supplies process-local state for behavior tests and
// evaluation runners. It is not a persistence backend or a PostgreSQL emulator.
package fixturestore

import (
	"sync"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

// Store implements capabilities consumed by fixtures, not a durable-store contract.
// ponytail: a single mutex is sufficient for small fixtures; use PostgreSQL for
// concurrency, transaction and restart durability acceptance.
type Store struct {
	mu              sync.RWMutex
	data            state
	artifactContent map[string][]byte
}

type state struct {
	Conversations         []domain.Conversation
	Messages              []domain.Message
	Agents                []domain.Agent
	Runs                  []domain.Run
	RunDelegations        []domain.RunDelegation
	CollaborationSteps    []domain.CollaborationStep
	RunEvents             []domain.RunEvent
	StageCheckpoints      []domain.StageCheckpoint
	ToolEffects           []domain.ToolEffectRecord
	RunUsageEntries       []domain.RunUsageEntry
	VerificationEvidence  []domain.VerificationEvidence
	VerificationArtifacts []domain.VerificationArtifact
	ContextCompactions    []domain.ContextCompaction
	TaskStateRevisions    []domain.TaskStateRevision
	ModelRequestRecords   []domain.ModelRequestRecord
	ToolArtifacts         []domain.ToolArtifact
	MemoryCandidates      []domain.MemoryCandidate
	Memories              []domain.Memory
	MemoryChanges         []domain.MemoryChange
	MemoryEmbeddings      []domain.MemoryEmbedding
	Documents             []domain.Document
	DocumentContents      map[string]string
	DocumentChunks        []domain.DocumentChunk
	ChunkEmbeddings       []domain.DocumentChunkEmbedding
}

func New() *Store {
	s := &Store{artifactContent: make(map[string][]byte)}
	s.data.DocumentContents = make(map[string]string)
	s.seedDefaultAgentsLocked()
	return s
}

func (s *Store) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return store.ScopeWorkspace(s, scope)
}
