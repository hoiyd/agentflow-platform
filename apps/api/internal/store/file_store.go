package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
)

type FileStore struct {
	path string
	mu   sync.RWMutex
	data fileData
}

type fileData struct {
	Conversations         []domain.Conversation           `json:"conversations"`
	Messages              []domain.Message                `json:"messages"`
	Agents                []domain.Agent                  `json:"agents"`
	Runs                  []domain.Run                    `json:"runs"`
	CollaborationSteps    []domain.CollaborationStep      `json:"collaboration_steps"`
	RunEvents             []domain.RunEvent               `json:"run_events"`
	RunUsageEntries       []domain.RunUsageEntry          `json:"run_usage_entries"`
	VerificationEvidence  []domain.VerificationEvidence   `json:"verification_evidence"`
	VerificationArtifacts []domain.VerificationArtifact   `json:"verification_artifacts"`
	ContextCompactions    []domain.ContextCompaction      `json:"context_compactions"`
	MemoryCandidates      []domain.MemoryCandidate        `json:"memory_candidates"`
	Memories              []domain.Memory                 `json:"memories"`
	MemoryEmbeddings      []domain.MemoryEmbedding        `json:"memory_embeddings"`
	Documents             []domain.Document               `json:"documents"`
	DocumentChunks        []domain.DocumentChunk          `json:"document_chunks"`
	ChunkEmbeddings       []domain.DocumentChunkEmbedding `json:"document_chunk_embeddings"`
}

var _ Store = (*FileStore)(nil)

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

func (s *FileStore) DeleteConversation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasConversationLocked(id) {
		return ErrNotFound("conversation")
	}

	conversations := make([]domain.Conversation, 0, len(s.data.Conversations))
	for _, conversation := range s.data.Conversations {
		if conversation.ID != id {
			conversations = append(conversations, conversation)
		}
	}
	s.data.Conversations = conversations

	messages := make([]domain.Message, 0, len(s.data.Messages))
	for _, message := range s.data.Messages {
		if message.ConversationID != id {
			messages = append(messages, message)
		}
	}
	s.data.Messages = messages

	runIDs := map[string]bool{}
	runs := make([]domain.Run, 0, len(s.data.Runs))
	for _, run := range s.data.Runs {
		if run.ConversationID == id {
			runIDs[run.ID] = true
			continue
		}
		runs = append(runs, run)
	}
	s.data.Runs = runs

	steps := make([]domain.CollaborationStep, 0, len(s.data.CollaborationSteps))
	for _, step := range s.data.CollaborationSteps {
		if step.ConversationID == id || runIDs[step.RunID] {
			continue
		}
		steps = append(steps, step)
	}
	s.data.CollaborationSteps = steps

	runEvents := make([]domain.RunEvent, 0, len(s.data.RunEvents))
	for _, event := range s.data.RunEvents {
		if !runIDs[event.RunID] {
			runEvents = append(runEvents, event)
		}
	}
	s.data.RunEvents = runEvents

	usageEntries := make([]domain.RunUsageEntry, 0, len(s.data.RunUsageEntries))
	for _, entry := range s.data.RunUsageEntries {
		if !runIDs[entry.RunID] {
			usageEntries = append(usageEntries, entry)
		}
	}
	s.data.RunUsageEntries = usageEntries

	evidence := make([]domain.VerificationEvidence, 0, len(s.data.VerificationEvidence))
	for _, item := range s.data.VerificationEvidence {
		if !runIDs[item.RunID] {
			evidence = append(evidence, item)
		}
	}
	s.data.VerificationEvidence = evidence

	artifacts := make([]domain.VerificationArtifact, 0, len(s.data.VerificationArtifacts))
	for _, item := range s.data.VerificationArtifacts {
		if !runIDs[item.RunID] {
			artifacts = append(artifacts, item)
		}
	}
	s.data.VerificationArtifacts = artifacts

	compactions := make([]domain.ContextCompaction, 0, len(s.data.ContextCompactions))
	for _, compaction := range s.data.ContextCompactions {
		if compaction.ConversationID != id {
			compactions = append(compactions, compaction)
		}
	}
	s.data.ContextCompactions = compactions

	return s.saveLocked()
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

func (s *FileStore) CreateContextCompaction(compaction domain.ContextCompaction) (domain.ContextCompaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasConversationLocked(compaction.ConversationID) {
		return domain.ContextCompaction{}, ErrNotFound("conversation")
	}
	for _, existing := range s.data.ContextCompactions {
		if existing.ConversationID == compaction.ConversationID && existing.SourceHash == compaction.SourceHash {
			return cloneContextCompaction(existing), nil
		}
	}
	if compaction.ID == "" {
		compaction.ID = newID("cmp")
	}
	if compaction.CreatedAt.IsZero() {
		compaction.CreatedAt = time.Now().UTC()
	}
	compaction.SourceMessageIDs = append([]string(nil), compaction.SourceMessageIDs...)
	s.data.ContextCompactions = append(s.data.ContextCompactions, compaction)
	return cloneContextCompaction(compaction), s.saveLocked()
}

func (s *FileStore) GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest domain.ContextCompaction
	found := false
	for _, item := range s.data.ContextCompactions {
		if item.ConversationID != conversationID {
			continue
		}
		if !found || item.CreatedAt.After(latest.CreatedAt) || (item.CreatedAt.Equal(latest.CreatedAt) && item.ID > latest.ID) {
			latest = item
			found = true
		}
	}
	return cloneContextCompaction(latest), found, nil
}

func cloneContextCompaction(item domain.ContextCompaction) domain.ContextCompaction {
	item.SourceMessageIDs = append([]string(nil), item.SourceMessageIDs...)
	return item
}

func (s *FileStore) CreateMemoryCandidate(candidate domain.MemoryCandidate) (domain.MemoryCandidate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	candidate, err = normalizeMemoryCandidate(candidate)
	if err != nil {
		return domain.MemoryCandidate{}, false, err
	}
	for _, existing := range s.data.MemoryCandidates {
		if existing.ID == candidate.ID {
			return existing, false, nil
		}
	}
	s.data.MemoryCandidates = append(s.data.MemoryCandidates, candidate)
	return candidate, true, s.saveLocked()
}

func (s *FileStore) ListMemoryCandidates(conversationID string) ([]domain.MemoryCandidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.MemoryCandidate, 0, len(s.data.MemoryCandidates))
	for _, candidate := range s.data.MemoryCandidates {
		if conversationID == "" || candidate.ConversationID == conversationID {
			items = append(items, candidate)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
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
	return ErrNotFound("conversation")
}

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

func (s *FileStore) CreateRun(agentID string, conversationID string, snapshot domain.RuntimeSnapshot) (domain.Run, error) {
	return s.CreateRunWithContract(agentID, conversationID, snapshot, nil)
}

func (s *FileStore) CreateRunWithContract(agentID string, conversationID string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasAgentLocked(agentID) {
		return domain.Run{}, errors.New("agent not found")
	}
	if !s.hasConversationLocked(conversationID) {
		return domain.Run{}, errors.New("conversation not found")
	}
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion {
		return domain.Run{}, errors.New("runtime snapshot is required")
	}

	now := time.Now().UTC()
	run := domain.Run{
		ID:                 newID("run"),
		AgentID:            agentID,
		ConversationID:     conversationID,
		Status:             domain.RunQueued,
		RuntimeSnapshot:    cloneRuntimeSnapshot(snapshot),
		CompletionContract: cloneCompletionContract(contract),
		VerificationStatus: domain.VerificationNotRequired,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if contract != nil {
		run.VerificationStatus = domain.VerificationPending
	}
	stored := cloneRun(run)
	s.data.Runs = append(s.data.Runs, stored)
	return cloneRun(run), s.saveLocked()
}

func (s *FileStore) UpdateRunVerificationStatus(id string, status domain.VerificationStatus) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			s.data.Runs[i].VerificationStatus = status
			s.data.Runs[i].UpdatedAt = time.Now().UTC()
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) AppendVerificationRecord(record domain.VerificationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.getRunLocked(record.Evidence.RunID); !ok {
		return errors.New("run not found")
	}
	for _, item := range s.data.VerificationEvidence {
		if item.ID == record.Evidence.ID {
			return errors.New("verification evidence already exists")
		}
	}
	for _, artifact := range record.Artifacts {
		if artifact.RunID != record.Evidence.RunID || artifact.EvidenceID != record.Evidence.ID {
			return errors.New("verification artifact does not match evidence")
		}
	}
	s.data.VerificationEvidence = append(s.data.VerificationEvidence, cloneVerificationEvidence(record.Evidence))
	s.data.VerificationArtifacts = append(s.data.VerificationArtifacts, record.Artifacts...)
	return s.saveLocked()
}

func (s *FileStore) ListVerificationEvidence(runID string) ([]domain.VerificationEvidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.VerificationEvidence{}
	for _, item := range s.data.VerificationEvidence {
		if item.RunID == runID {
			items = append(items, cloneVerificationEvidence(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.Before(items[j].StartedAt) })
	return items, nil
}

func (s *FileStore) ListVerificationArtifacts(runID string) ([]domain.VerificationArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.VerificationArtifact{}
	for _, item := range s.data.VerificationArtifacts {
		if item.RunID == runID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *FileStore) UpdateRunAgent(id string, agentID string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasAgentLocked(agentID) {
		return domain.Run{}, errors.New("agent not found")
	}
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			s.data.Runs[i].AgentID = agentID
			s.data.Runs[i].UpdatedAt = time.Now().UTC()
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) UpdateRunStatus(id string, status domain.RunStatus, errorMessage string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			now := time.Now().UTC()
			wasRunning := s.data.Runs[i].Status == domain.RunRunning
			willRun := status == domain.RunRunning
			if wasRunning && !willRun && s.data.Runs[i].ExecutionStartedAt != nil {
				s.data.Runs[i].ActiveRuntimeMS += max(int64(0), now.Sub(*s.data.Runs[i].ExecutionStartedAt).Milliseconds())
				s.data.Runs[i].ExecutionStartedAt = nil
			}
			if !wasRunning && willRun {
				s.data.Runs[i].ExecutionStartedAt = &now
			}
			s.data.Runs[i].Status = status
			s.data.Runs[i].Error = strings.TrimSpace(errorMessage)
			s.data.Runs[i].UpdatedAt = now
			if status == domain.RunRunning && s.data.Runs[i].StartedAt == nil {
				s.data.Runs[i].StartedAt = &now
			}
			if status == domain.RunRunning {
				s.data.Runs[i].HeartbeatAt = &now
				s.data.Runs[i].CompletedAt = nil
			}
			if status == domain.RunWaitingForUser {
				s.data.Runs[i].CompletedAt = nil
			}
			if status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunFailedRecoverable || status == domain.RunCanceled {
				s.data.Runs[i].CompletedAt = &now
			}
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) UpdateRunHeartbeat(id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Runs {
		if s.data.Runs[i].ID == id {
			now := time.Now().UTC()
			s.data.Runs[i].HeartbeatAt = &now
			s.data.Runs[i].UpdatedAt = now
			return cloneRun(s.data.Runs[i]), s.saveLocked()
		}
	}
	return domain.Run{}, errors.New("run not found")
}

func (s *FileStore) ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := []domain.Run{}
	for _, run := range s.data.Runs {
		if run.Status != domain.RunRunning {
			continue
		}
		if run.HeartbeatAt == nil || run.HeartbeatAt.Before(cutoff) {
			items = append(items, cloneRun(run))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileStore) GetRun(id string) (domain.Run, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Runs {
		if item.ID == id {
			return cloneRun(item), true, nil
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
	items := make([]domain.Run, 0, len(s.data.Runs))
	for _, run := range s.data.Runs {
		items = append(items, cloneRun(run))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileStore) CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasRunLocked(step.RunID) {
		return domain.CollaborationStep{}, errors.New("run not found")
	}
	if !s.hasConversationLocked(step.ConversationID) {
		return domain.CollaborationStep{}, errors.New("conversation not found")
	}
	now := time.Now().UTC()
	step.ID = strings.TrimSpace(step.ID)
	if step.ID == "" {
		step.ID = newID("step")
	}
	step.Role = strings.TrimSpace(step.Role)
	if step.Role == "" {
		return domain.CollaborationStep{}, errors.New("collaboration role is required")
	}
	if step.Status == "" {
		step.Status = domain.CollaborationStepQueued
	}
	step.Input = strings.TrimSpace(step.Input)
	step.Output = strings.TrimSpace(step.Output)
	step.Error = strings.TrimSpace(step.Error)
	if step.Iteration < 0 {
		step.Iteration = 0
	}
	step.CreatedAt = now
	step.UpdatedAt = now
	s.data.CollaborationSteps = append(s.data.CollaborationSteps, step)
	return step, s.saveLocked()
}

func (s *FileStore) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.CollaborationSteps {
		if s.data.CollaborationSteps[i].ID == id {
			s.data.CollaborationSteps[i].Status = status
			s.data.CollaborationSteps[i].Output = strings.TrimSpace(output)
			s.data.CollaborationSteps[i].Error = strings.TrimSpace(errorMessage)
			s.data.CollaborationSteps[i].UpdatedAt = time.Now().UTC()
			return s.data.CollaborationSteps[i], s.saveLocked()
		}
	}
	return domain.CollaborationStep{}, errors.New("collaboration step not found")
}

func (s *FileStore) UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.CollaborationSteps {
		if s.data.CollaborationSteps[i].ID == id {
			s.data.CollaborationSteps[i].Output = strings.TrimSpace(output)
			s.data.CollaborationSteps[i].UpdatedAt = time.Now().UTC()
			return s.data.CollaborationSteps[i], s.saveLocked()
		}
	}
	return domain.CollaborationStep{}, errors.New("collaboration step not found")
}

func (s *FileStore) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := []domain.CollaborationStep{}
	for _, step := range s.data.CollaborationSteps {
		if step.RunID == runID {
			items = append(items, step)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *FileStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(event.RunID) {
		return domain.RunEvent{}, errors.New("run not found")
	}
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = newID("event")
	}
	if event.Type == "" {
		return domain.RunEvent{}, errors.New("run event type is required")
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = domain.CurrentRunEventSchemaVersion
	}
	var next int64 = 1
	for _, existing := range s.data.RunEvents {
		if existing.RunID == event.RunID && existing.Sequence >= next {
			next = existing.Sequence + 1
		}
	}
	event.Sequence = next
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	s.data.RunEvents = append(s.data.RunEvents, event)
	return event, s.saveLocked()
}

func (s *FileStore) ListRunEvents(runID string) ([]domain.RunEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.RunEvent{}
	for _, event := range s.data.RunEvents {
		if event.RunID == runID {
			items = append(items, event)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Sequence < items[j].Sequence })
	return items, nil
}

func (s *FileStore) GetRunTraceSummary(runID string) (domain.RunTraceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunTraceSummary{}, ErrNotFound("run")
	}
	return buildRunTraceSummary(run, s.runEventsForRunLocked(runID)), nil
}

func (s *FileStore) ApplyRunUsage(entry domain.RunUsageEntry) (domain.RunUsageLedger, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.getRunLocked(entry.RunID)
	if !ok {
		return domain.RunUsageLedger{}, false, ErrNotFound("run")
	}
	if err := validateUsageEntry(entry); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	entries := s.runUsageEntriesForRunLocked(entry.RunID)
	for _, existing := range entries {
		if existing.OperationID != entry.OperationID || existing.Kind != entry.Kind {
			continue
		}
		ledger := budget.BuildLedger(entry.RunID, runBudget(run), entries)
		if !sameUsageEntry(existing, entry) {
			return ledger, false, errors.New("run usage operation already recorded with different values")
		}
		return ledger, false, nil
	}
	if entry.Kind == domain.UsageModelSettlement && !hasUsageReservation(entries, entry.OperationID) {
		return budget.BuildLedger(entry.RunID, runBudget(run), entries), false, errors.New("model usage settlement has no reservation")
	}
	proposed := append(append([]domain.RunUsageEntry(nil), entries...), entry)
	ledger := budget.BuildLedger(entry.RunID, runBudget(run), proposed)
	if entry.Kind != domain.UsageModelSettlement {
		current := budget.BuildLedger(entry.RunID, runBudget(run), entries)
		if err := budget.Check(runBudget(run), current.Totals, budget.EntryTotals(entry), entry.OperationID, entry.Purpose); err != nil {
			return current, false, err
		}
	}

	s.data.RunUsageEntries = append(s.data.RunUsageEntries, entry)
	if err := s.saveLocked(); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	if entry.Kind == domain.UsageModelSettlement {
		if err := budget.CheckTotals(runBudget(run), ledger.Totals, entry.OperationID, entry.Purpose); err != nil {
			return ledger, true, err
		}
	}
	return ledger, true, nil
}

func (s *FileStore) GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunUsageLedger{}, false, nil
	}
	return budget.BuildLedger(runID, runBudget(run), s.runUsageEntriesForRunLocked(runID)), true, nil
}

func (s *FileStore) GetRunReplay(runID string) (domain.RunReplay, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunReplay{}, false, nil
	}
	conversation, ok := s.getConversationLocked(run.ConversationID)
	if !ok {
		return domain.RunReplay{}, false, errors.New("conversation not found")
	}
	messages := s.messagesForConversationLocked(run.ConversationID)
	steps := s.stepsForRunLocked(runID)
	runEvents := s.runEventsForRunLocked(runID)
	return domain.RunReplay{
		Run:                   cloneRun(run),
		RuntimeSnapshot:       cloneRuntimeSnapshotValue(run.RuntimeSnapshot),
		Conversation:          conversation,
		Messages:              messages,
		Steps:                 steps,
		Summary:               buildRunTraceSummary(run, runEvents),
		UsageLedger:           budget.BuildLedger(runID, runBudget(run), s.runUsageEntriesForRunLocked(runID)),
		RunEvents:             runEvents,
		VerificationEvidence:  verificationEvidenceForRun(s.data.VerificationEvidence, runID),
		VerificationArtifacts: verificationArtifactsForRun(s.data.VerificationArtifacts, runID),
	}, true, nil
}

func cloneRuntimeSnapshot(snapshot domain.RuntimeSnapshot) *domain.RuntimeSnapshot {
	bytes, _ := json.Marshal(snapshot)
	var cloned domain.RuntimeSnapshot
	_ = json.Unmarshal(bytes, &cloned)
	return &cloned
}

func cloneRuntimeSnapshotValue(snapshot *domain.RuntimeSnapshot) *domain.RuntimeSnapshot {
	if snapshot == nil {
		return nil
	}
	return cloneRuntimeSnapshot(*snapshot)
}

func cloneRun(run domain.Run) domain.Run {
	run.RuntimeSnapshot = cloneRuntimeSnapshotValue(run.RuntimeSnapshot)
	run.CompletionContract = cloneCompletionContract(run.CompletionContract)
	return run
}

func (s *FileStore) runUsageEntriesForRunLocked(runID string) []domain.RunUsageEntry {
	entries := []domain.RunUsageEntry{}
	for _, entry := range s.data.RunUsageEntries {
		if entry.RunID == runID {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	return entries
}

func runBudget(run domain.Run) domain.RuntimeRunBudget {
	if run.RuntimeSnapshot == nil || run.RuntimeSnapshot.RunBudget == nil {
		return domain.RuntimeRunBudget{}
	}
	return *run.RuntimeSnapshot.RunBudget
}

func validateUsageEntry(entry domain.RunUsageEntry) error {
	if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.RunID) == "" || strings.TrimSpace(entry.OperationID) == "" {
		return errors.New("run usage requires id, run_id, and operation_id")
	}
	if entry.Purpose != domain.UsagePurposePrimary && entry.Purpose != domain.UsagePurposeRouter && entry.Purpose != domain.UsagePurposeCompaction {
		return errors.New("run usage purpose is invalid")
	}
	if entry.ModelCalls < 0 || entry.ToolCalls < 0 || entry.PromptTokens < 0 || entry.CompletionTokens < 0 || entry.TotalTokens < 0 || entry.EstimatedCostMicros < 0 {
		return errors.New("run usage values cannot be negative")
	}
	switch entry.Kind {
	case domain.UsageModelReservation, domain.UsageModelSettlement:
		if entry.ModelCalls != 1 || entry.ToolCalls != 0 || entry.TotalTokens < entry.PromptTokens+entry.CompletionTokens {
			return errors.New("model usage entry has invalid counters")
		}
	case domain.UsageToolExecution:
		if entry.ToolCalls != 1 || entry.ModelCalls != 0 || entry.PromptTokens != 0 || entry.CompletionTokens != 0 || entry.TotalTokens != 0 || entry.EstimatedCostMicros != 0 {
			return errors.New("tool usage entry has invalid counters")
		}
	default:
		return errors.New("run usage kind is invalid")
	}
	if entry.Timestamp.IsZero() {
		return errors.New("run usage timestamp is required")
	}
	return nil
}

func hasUsageReservation(entries []domain.RunUsageEntry, operationID string) bool {
	for _, entry := range entries {
		if entry.OperationID == operationID && entry.Kind == domain.UsageModelReservation {
			return true
		}
	}
	return false
}

func sameUsageEntry(left, right domain.RunUsageEntry) bool {
	left.ID, right.ID = "", ""
	left.Timestamp, right.Timestamp = time.Time{}, time.Time{}
	return left == right
}

func cloneCompletionContract(contract *domain.CompletionContract) *domain.CompletionContract {
	if contract == nil {
		return nil
	}
	bytes, _ := json.Marshal(contract)
	var cloned domain.CompletionContract
	_ = json.Unmarshal(bytes, &cloned)
	return &cloned
}

func verificationEvidenceForRun(items []domain.VerificationEvidence, runID string) []domain.VerificationEvidence {
	result := []domain.VerificationEvidence{}
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, cloneVerificationEvidence(item))
		}
	}
	return result
}

func cloneVerificationEvidence(evidence domain.VerificationEvidence) domain.VerificationEvidence {
	evidence.ArtifactIDs = append([]string(nil), evidence.ArtifactIDs...)
	if evidence.Details == nil {
		evidence.Details = map[string]any{}
	} else {
		encoded, _ := json.Marshal(evidence.Details)
		evidence.Details = nil
		_ = json.Unmarshal(encoded, &evidence.Details)
	}
	return evidence
}

func verificationArtifactsForRun(items []domain.VerificationArtifact, runID string) []domain.VerificationArtifact {
	result := []domain.VerificationArtifact{}
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, item)
		}
	}
	return result
}

func (s *FileStore) CreateMemory(memory domain.Memory, embedding domain.MemoryEmbedding) (domain.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	memory.ID = strings.TrimSpace(memory.ID)
	if memory.ID == "" {
		memory.ID = newID("mem")
	}
	memory.Kind = strings.TrimSpace(memory.Kind)
	if memory.Kind == "" {
		return domain.Memory{}, errors.New("memory kind is required")
	}
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.Content == "" {
		return domain.Memory{}, errors.New("memory content is required")
	}
	if memory.Metadata == nil {
		memory.Metadata = map[string]any{}
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = now
	}
	memory.UpdatedAt = now
	embedding.MemoryID = memory.ID
	if embedding.Provider == "" {
		embedding.Provider = "local"
	}
	if embedding.Model == "" {
		embedding.Model = "local_hash"
	}
	if embedding.Dimensions == 0 {
		embedding.Dimensions = len(embedding.Embedding)
	}
	if embedding.CreatedAt.IsZero() {
		embedding.CreatedAt = now
	}

	s.data.Memories = append(s.data.Memories, memory)
	s.data.MemoryEmbeddings = append(s.data.MemoryEmbeddings, embedding)
	return memory, s.saveLocked()
}

func (s *FileStore) SearchMemories(search domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := search.Limit
	if limit <= 0 {
		limit = 5
	} else if limit > 20 {
		limit = 20
	}
	embeddingByMemoryID := map[string]domain.MemoryEmbedding{}
	for _, embedding := range s.data.MemoryEmbeddings {
		embeddingByMemoryID[embedding.MemoryID] = embedding
	}

	items := []domain.RetrievedMemory{}
	now := time.Now().UTC()
	for _, memory := range s.data.Memories {
		if !memoryMatchesSearch(memory, search) {
			continue
		}
		embedding, ok := embeddingByMemoryID[memory.ID]
		if !ok || len(embedding.Embedding) == 0 || len(search.Embedding) == 0 {
			continue
		}
		if search.EmbeddingProvider != "" && embedding.Provider != search.EmbeddingProvider {
			continue
		}
		if search.EmbeddingModel != "" && embedding.Model != search.EmbeddingModel {
			continue
		}
		similarity := cosineSimilarity(search.Embedding, embedding.Embedding)
		recencyBoost := memoryRecencyBoost(now, memory.CreatedAt)
		items = append(items, domain.RetrievedMemory{
			Memory:       memory,
			Similarity:   similarity,
			RecencyBoost: recencyBoost,
			Score:        similarity + recencyBoost,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(chunks) != len(embeddings) {
		return domain.Document{}, errors.New("document chunks and embeddings length mismatch")
	}
	now := time.Now().UTC()
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		document.ID = newID("doc")
	}
	document.Title = strings.TrimSpace(document.Title)
	if document.Title == "" {
		return domain.Document{}, errors.New("document title is required")
	}
	document.Content = strings.TrimSpace(document.Content)
	if document.Content == "" {
		return domain.Document{}, errors.New("document content is required")
	}
	document.SourceType = strings.TrimSpace(document.SourceType)
	if document.SourceType == "" {
		document.SourceType = "text"
	}
	if document.Metadata == nil {
		document.Metadata = map[string]any{}
	}
	if document.CreatedAt.IsZero() {
		document.CreatedAt = now
	}
	document.UpdatedAt = now

	for i := range chunks {
		chunks[i].ID = strings.TrimSpace(chunks[i].ID)
		if chunks[i].ID == "" {
			chunks[i].ID = newID("chunk")
		}
		chunks[i].DocumentID = document.ID
		chunks[i].ChunkIndex = i
		chunks[i].Content = strings.TrimSpace(chunks[i].Content)
		if chunks[i].Content == "" {
			return domain.Document{}, errors.New("document chunk content is required")
		}
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = map[string]any{}
		}
		if chunks[i].CreatedAt.IsZero() {
			chunks[i].CreatedAt = now
		}
		embeddings[i].ChunkID = chunks[i].ID
		if embeddings[i].Provider == "" {
			embeddings[i].Provider = "local"
		}
		if embeddings[i].Model == "" {
			embeddings[i].Model = "local_hash"
		}
		if embeddings[i].Dimensions == 0 {
			embeddings[i].Dimensions = len(embeddings[i].Embedding)
		}
		if embeddings[i].CreatedAt.IsZero() {
			embeddings[i].CreatedAt = now
		}
	}

	document.ChunkCount = len(chunks)
	document.EmbeddingCount = len(embeddings)
	s.data.Documents = append(s.data.Documents, document)
	s.data.DocumentChunks = append(s.data.DocumentChunks, chunks...)
	s.data.ChunkEmbeddings = append(s.data.ChunkEmbeddings, embeddings...)
	return document, s.saveLocked()
}

func (s *FileStore) ListDocuments() ([]domain.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	documents := append([]domain.Document(nil), s.data.Documents...)
	chunkCounts := map[string]int{}
	embeddingCounts := map[string]int{}
	for _, chunk := range s.data.DocumentChunks {
		chunkCounts[chunk.DocumentID]++
	}
	chunkIDToDocumentID := map[string]string{}
	for _, chunk := range s.data.DocumentChunks {
		chunkIDToDocumentID[chunk.ID] = chunk.DocumentID
	}
	for _, embedding := range s.data.ChunkEmbeddings {
		if documentID := chunkIDToDocumentID[embedding.ChunkID]; documentID != "" {
			embeddingCounts[documentID]++
		}
	}
	for i := range documents {
		documents[i].ChunkCount = chunkCounts[documents[i].ID]
		documents[i].EmbeddingCount = embeddingCounts[documents[i].ID]
	}
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].CreatedAt.After(documents[j].CreatedAt)
	})
	return documents, nil
}

func (s *FileStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var document domain.Document
	found := false
	for _, item := range s.data.Documents {
		if item.ID == strings.TrimSpace(id) {
			document = item
			found = true
			break
		}
	}
	if !found {
		return domain.Document{}, nil, false, nil
	}
	chunks := []domain.DocumentChunk{}
	for _, chunk := range s.data.DocumentChunks {
		if chunk.DocumentID == document.ID {
			chunk.Document = document
			chunks = append(chunks, chunk)
		}
	}
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].ChunkIndex < chunks[j].ChunkIndex
	})
	document.ChunkCount = len(chunks)
	embeddingByChunkID := map[string]bool{}
	for _, embedding := range s.data.ChunkEmbeddings {
		embeddingByChunkID[embedding.ChunkID] = true
	}
	for _, chunk := range chunks {
		if embeddingByChunkID[chunk.ID] {
			document.EmbeddingCount++
		}
	}
	return document, chunks, true, nil
}

func (s *FileStore) DeleteDocument(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound("document")
	}
	found := false
	documents := s.data.Documents[:0]
	for _, document := range s.data.Documents {
		if document.ID == id {
			found = true
			continue
		}
		documents = append(documents, document)
	}
	if !found {
		return ErrNotFound("document")
	}

	deletedChunkIDs := map[string]bool{}
	chunks := s.data.DocumentChunks[:0]
	for _, chunk := range s.data.DocumentChunks {
		if chunk.DocumentID == id {
			deletedChunkIDs[chunk.ID] = true
			continue
		}
		chunks = append(chunks, chunk)
	}
	embeddings := s.data.ChunkEmbeddings[:0]
	for _, embedding := range s.data.ChunkEmbeddings {
		if deletedChunkIDs[embedding.ChunkID] {
			continue
		}
		embeddings = append(embeddings, embedding)
	}

	s.data.Documents = documents
	s.data.DocumentChunks = chunks
	s.data.ChunkEmbeddings = embeddings
	return s.saveLocked()
}

func (s *FileStore) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := search.Limit
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	documentByID := map[string]domain.Document{}
	for _, document := range s.data.Documents {
		documentByID[document.ID] = document
	}
	embeddingByChunkID := map[string]domain.DocumentChunkEmbedding{}
	for _, embedding := range s.data.ChunkEmbeddings {
		embeddingByChunkID[embedding.ChunkID] = embedding
	}

	items := []domain.RetrievedDocumentChunk{}
	now := time.Now().UTC()
	for _, chunk := range s.data.DocumentChunks {
		document, ok := documentByID[chunk.DocumentID]
		if !ok || !documentChunkMatchesSearch(document, chunk, search) {
			continue
		}
		embedding, ok := embeddingByChunkID[chunk.ID]
		if !ok || len(embedding.Embedding) == 0 || len(search.Embedding) == 0 {
			continue
		}
		if search.EmbeddingProvider != "" && embedding.Provider != search.EmbeddingProvider {
			continue
		}
		if search.EmbeddingModel != "" && embedding.Model != search.EmbeddingModel {
			continue
		}
		similarity := cosineSimilarity(search.Embedding, embedding.Embedding)
		if search.MinSimilarity > 0 && similarity < search.MinSimilarity {
			continue
		}
		recencyBoost := memoryRecencyBoost(now, chunk.CreatedAt)
		items = append(items, domain.RetrievedDocumentChunk{
			Document:     document,
			Chunk:        chunk,
			Similarity:   similarity,
			RecencyBoost: recencyBoost,
			Score:        similarity + recencyBoost,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})
	if len(items) > limit {
		items = items[:limit]
	}
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

func (s *FileStore) hasRunLocked(id string) bool {
	for _, item := range s.data.Runs {
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
		Conversations:      []domain.Conversation{},
		Messages:           []domain.Message{},
		Agents:             []domain.Agent{},
		Runs:               []domain.Run{},
		CollaborationSteps: []domain.CollaborationStep{},
		RunEvents:          []domain.RunEvent{},
		ContextCompactions: []domain.ContextCompaction{},
		MemoryCandidates:   []domain.MemoryCandidate{},
		Memories:           []domain.Memory{},
		MemoryEmbeddings:   []domain.MemoryEmbedding{},
		Documents:          []domain.Document{},
		DocumentChunks:     []domain.DocumentChunk{},
		ChunkEmbeddings:    []domain.DocumentChunkEmbedding{},
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
	if s.data.CollaborationSteps == nil {
		s.data.CollaborationSteps = []domain.CollaborationStep{}
	}
	if s.data.RunEvents == nil {
		s.data.RunEvents = []domain.RunEvent{}
	}
	if s.data.ContextCompactions == nil {
		s.data.ContextCompactions = []domain.ContextCompaction{}
	}
	if s.data.MemoryCandidates == nil {
		s.data.MemoryCandidates = []domain.MemoryCandidate{}
	}
	if s.data.Memories == nil {
		s.data.Memories = []domain.Memory{}
	}
	if s.data.MemoryEmbeddings == nil {
		s.data.MemoryEmbeddings = []domain.MemoryEmbedding{}
	}
	if s.data.Documents == nil {
		s.data.Documents = []domain.Document{}
	}
	if s.data.DocumentChunks == nil {
		s.data.DocumentChunks = []domain.DocumentChunk{}
	}
	if s.data.ChunkEmbeddings == nil {
		s.data.ChunkEmbeddings = []domain.DocumentChunkEmbedding{}
	}
}

func (s *FileStore) getRunLocked(id string) (domain.Run, bool) {
	for _, item := range s.data.Runs {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Run{}, false
}

func (s *FileStore) getConversationLocked(id string) (domain.Conversation, bool) {
	for _, item := range s.data.Conversations {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Conversation{}, false
}

func (s *FileStore) messagesForConversationLocked(conversationID string) []domain.Message {
	messages := []domain.Message{}
	for _, message := range s.data.Messages {
		if message.ConversationID == conversationID {
			messages = append(messages, message)
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages
}

func (s *FileStore) stepsForRunLocked(runID string) []domain.CollaborationStep {
	steps := []domain.CollaborationStep{}
	for _, step := range s.data.CollaborationSteps {
		if step.RunID == runID {
			steps = append(steps, step)
		}
	}
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].CreatedAt.Before(steps[j].CreatedAt)
	})
	return steps
}

func (s *FileStore) runEventsForRunLocked(runID string) []domain.RunEvent {
	events := []domain.RunEvent{}
	for _, event := range s.data.RunEvents {
		if event.RunID == runID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	return events
}

func buildRunTraceSummary(run domain.Run, events []domain.RunEvent) domain.RunTraceSummary {
	summary := domain.RunTraceSummary{
		RunID:  run.ID,
		Status: run.Status,
	}
	if run.StartedAt != nil {
		end := time.Now().UTC()
		if run.CompletedAt != nil {
			end = *run.CompletedAt
		}
		if end.After(*run.StartedAt) {
			summary.TotalDurationMS = end.Sub(*run.StartedAt).Milliseconds()
		}
	}
	for _, event := range events {
		switch event.Type {
		case domain.EventModelCompleted:
			summary.LLMCalls++
			summary.PromptTokens += intPayload(event.Payload, "prompt_tokens")
			summary.CompletionTokens += intPayload(event.Payload, "completion_tokens")
			summary.TotalTokens += intPayload(event.Payload, "total_tokens")
			if boolPayload(event.Payload, "token_usage_estimated") {
				summary.TokenUsageEstimated = true
			}
		case domain.EventToolCompleted, domain.EventToolFailed:
			summary.ToolCalls++
		case domain.EventModelFailed, domain.EventRetrievalFailed, domain.EventMemoryCandidateFailed, domain.EventMemorySyncFailed:
			summary.ErrorCount++
		}
	}
	return summary
}

func intPayload(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func boolPayload(payload map[string]any, key string) bool {
	value, ok := payload[key].(bool)
	return ok && value
}

func memoryMatchesSearch(memory domain.Memory, search domain.MemorySearch) bool {
	if search.WorkspaceID != "" && memory.WorkspaceID != search.WorkspaceID {
		return false
	}
	if search.UserID != "" && memory.UserID != search.UserID {
		return false
	}
	if search.ProjectID != "" && memory.ProjectID != search.ProjectID {
		return false
	}
	for key, expected := range search.Metadata {
		value, ok := memory.Metadata[key]
		if !ok || strings.TrimSpace(expected) != strings.TrimSpace(toString(value)) {
			return false
		}
	}
	return true
}

func documentChunkMatchesSearch(document domain.Document, chunk domain.DocumentChunk, search domain.DocumentSearch) bool {
	if search.WorkspaceID != "" && document.WorkspaceID != search.WorkspaceID {
		return false
	}
	for key, expected := range search.Metadata {
		value, ok := chunk.Metadata[key]
		if !ok {
			value, ok = document.Metadata[key]
		}
		if !ok || strings.TrimSpace(expected) != strings.TrimSpace(toString(value)) {
			return false
		}
	}
	return true
}

func cosineSimilarity(a []float64, b []float64) float64 {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	if limit == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < limit; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func memoryRecencyBoost(now time.Time, createdAt time.Time) float64 {
	if createdAt.IsZero() {
		return 0
	}
	ageDays := now.Sub(createdAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return 0.05 / (1 + ageDays/30)
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		bytes, _ := json.Marshal(v)
		return string(bytes)
	}
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
