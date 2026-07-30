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
	DocumentContents      map[string]string               `json:"document_contents,omitempty"`
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
			message.Citations = cloneCitations(message.Citations)
			messages = append(messages, message)
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages, nil
}

func (s *FileStore) AddMessage(conversationID string, role string, content string) (domain.Message, error) {
	return s.AddMessageWithCitations(conversationID, role, content, nil)
}

func (s *FileStore) AddMessageWithCitations(conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error) {
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
		Citations:      cloneCitations(citations),
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

func cloneCitations(citations []domain.RAGCitation) []domain.RAGCitation {
	if len(citations) == 0 {
		return nil
	}
	cloned := make([]domain.RAGCitation, len(citations))
	for index, citation := range citations {
		citation.SourceChunkIDs = append([]string(nil), citation.SourceChunkIDs...)
		citation.SectionPath = append([]string(nil), citation.SectionPath...)
		cloned[index] = citation
	}
	return cloned
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
		DocumentContents:   map[string]string{},
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
	if s.data.DocumentContents == nil {
		s.data.DocumentContents = map[string]string{}
	}
	for index := range s.data.Documents {
		s.data.Documents[index].Content = s.data.DocumentContents[s.data.Documents[index].ID]
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
