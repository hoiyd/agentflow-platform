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
	Conversations         []domain.Conversation           `json:"conversations"`
	Messages              []domain.Message                `json:"messages"`
	Agents                []domain.Agent                  `json:"agents"`
	Runs                  []domain.Run                    `json:"runs"`
	CollaborationSteps    []domain.CollaborationStep      `json:"collaboration_steps"`
	RunEvents             []domain.RunEvent               `json:"run_events"`
	StageCheckpoints      []domain.StageCheckpoint        `json:"stage_checkpoints"`
	ToolEffects           []domain.ToolEffectRecord       `json:"tool_effects"`
	RunUsageEntries       []domain.RunUsageEntry          `json:"run_usage_entries"`
	VerificationEvidence  []domain.VerificationEvidence   `json:"verification_evidence"`
	VerificationArtifacts []domain.VerificationArtifact   `json:"verification_artifacts"`
	ContextCompactions    []domain.ContextCompaction      `json:"context_compactions"`
	TaskStateRevisions    []domain.TaskStateRevision      `json:"task_state_revisions"`
	ModelRequestRecords   []domain.ModelRequestRecord     `json:"model_request_records"`
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
	migrated := s.normalizeLoadedDataLocked()
	if len(s.data.Agents) == 0 {
		s.seedDefaultAgentsLocked()
		return s.saveLocked()
	}
	if s.migrateDefaultAgentsLocked() || migrated {
		return s.saveLocked()
	}
	return nil
}

func (s *FileStore) saveLocked() error {
	bytes, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(s.path)
	temporary, err := os.CreateTemp(directory, ".agentflow-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(bytes); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return err
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

func (s *FileStore) hasConversationLocked(id string) bool {
	for _, item := range s.data.Conversations {
		if item.ID == id {
			return true
		}
	}
	return false
}

func normalizeWorkspaceID(workspaceID string) string {
	return domain.NormalizeWorkspaceID(workspaceID)
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
		Conversations:       []domain.Conversation{},
		Messages:            []domain.Message{},
		Agents:              []domain.Agent{},
		Runs:                []domain.Run{},
		CollaborationSteps:  []domain.CollaborationStep{},
		RunEvents:           []domain.RunEvent{},
		StageCheckpoints:    []domain.StageCheckpoint{},
		ToolEffects:         []domain.ToolEffectRecord{},
		ContextCompactions:  []domain.ContextCompaction{},
		TaskStateRevisions:  []domain.TaskStateRevision{},
		ModelRequestRecords: []domain.ModelRequestRecord{},
		MemoryCandidates:    []domain.MemoryCandidate{},
		Memories:            []domain.Memory{},
		MemoryEmbeddings:    []domain.MemoryEmbedding{},
		Documents:           []domain.Document{},
		DocumentContents:    map[string]string{},
		DocumentChunks:      []domain.DocumentChunk{},
		ChunkEmbeddings:     []domain.DocumentChunkEmbedding{},
	}
}

func (s *FileStore) normalizeLoadedDataLocked() bool {
	migrated := false
	if s.data.Conversations == nil {
		s.data.Conversations = []domain.Conversation{}
	}
	for i := range s.data.Conversations {
		if normalized := normalizeWorkspaceID(s.data.Conversations[i].WorkspaceID); normalized != s.data.Conversations[i].WorkspaceID {
			s.data.Conversations[i].WorkspaceID = normalized
			migrated = true
		}
	}
	for i := range s.data.Messages {
		if normalized := normalizeWorkspaceID(s.data.Messages[i].WorkspaceID); normalized != s.data.Messages[i].WorkspaceID {
			if conversation, ok := s.getConversationLocked(s.data.Messages[i].ConversationID); ok {
				s.data.Messages[i].WorkspaceID = conversation.WorkspaceID
			} else {
				s.data.Messages[i].WorkspaceID = normalized
			}
			migrated = true
		}
	}
	for i := range s.data.Runs {
		if normalized := normalizeWorkspaceID(s.data.Runs[i].WorkspaceID); normalized != s.data.Runs[i].WorkspaceID {
			if conversation, ok := s.getConversationLocked(s.data.Runs[i].ConversationID); ok {
				s.data.Runs[i].WorkspaceID = conversation.WorkspaceID
			} else {
				s.data.Runs[i].WorkspaceID = normalized
			}
			migrated = true
		}
	}
	for i := range s.data.TaskStateRevisions {
		revision := &s.data.TaskStateRevisions[i]
		if conversation, ok := s.getConversationLocked(revision.ConversationID); ok {
			workspaceID := normalizeWorkspaceID(conversation.WorkspaceID)
			if revision.WorkspaceID != workspaceID || revision.State.WorkspaceID != workspaceID {
				revision.WorkspaceID = workspaceID
				revision.State.WorkspaceID = workspaceID
				migrated = true
			}
		}
	}
	for i := range s.data.Documents {
		if normalized := normalizeWorkspaceID(s.data.Documents[i].WorkspaceID); normalized != s.data.Documents[i].WorkspaceID {
			s.data.Documents[i].WorkspaceID = normalized
			migrated = true
		}
	}
	for i := range s.data.Memories {
		if normalized := normalizeWorkspaceID(s.data.Memories[i].WorkspaceID); normalized != s.data.Memories[i].WorkspaceID {
			if conversation, ok := s.getConversationLocked(s.data.Memories[i].ConversationID); ok {
				s.data.Memories[i].WorkspaceID = conversation.WorkspaceID
			} else {
				s.data.Memories[i].WorkspaceID = normalized
			}
			migrated = true
		}
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
	if s.data.StageCheckpoints == nil {
		s.data.StageCheckpoints = []domain.StageCheckpoint{}
	}
	if s.data.ToolEffects == nil {
		s.data.ToolEffects = []domain.ToolEffectRecord{}
	}
	if s.data.ContextCompactions == nil {
		s.data.ContextCompactions = []domain.ContextCompaction{}
	}
	if s.data.TaskStateRevisions == nil {
		s.data.TaskStateRevisions = []domain.TaskStateRevision{}
	}
	if s.data.ModelRequestRecords == nil {
		s.data.ModelRequestRecords = []domain.ModelRequestRecord{}
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
	return migrated
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
