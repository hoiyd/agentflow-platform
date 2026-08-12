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

func normalizeWorkspaceID(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return domain.DefaultWorkspaceID
	}
	return workspaceID
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

func (s *FileStore) normalizeLoadedDataLocked() bool {
	migrated := false
	if s.data.Conversations == nil {
		s.data.Conversations = []domain.Conversation{}
	}
	for i := range s.data.Conversations {
		if strings.TrimSpace(s.data.Conversations[i].WorkspaceID) == "" {
			s.data.Conversations[i].WorkspaceID = domain.DefaultWorkspaceID
			migrated = true
		}
	}
	for i := range s.data.Messages {
		if strings.TrimSpace(s.data.Messages[i].WorkspaceID) == "" {
			if conversation, ok := s.getConversationLocked(s.data.Messages[i].ConversationID); ok {
				s.data.Messages[i].WorkspaceID = conversation.WorkspaceID
			} else {
				s.data.Messages[i].WorkspaceID = domain.DefaultWorkspaceID
			}
			migrated = true
		}
	}
	for i := range s.data.Runs {
		if strings.TrimSpace(s.data.Runs[i].WorkspaceID) == "" {
			if conversation, ok := s.getConversationLocked(s.data.Runs[i].ConversationID); ok {
				s.data.Runs[i].WorkspaceID = conversation.WorkspaceID
			} else {
				s.data.Runs[i].WorkspaceID = domain.DefaultWorkspaceID
			}
			migrated = true
		}
	}
	for i := range s.data.Documents {
		if strings.TrimSpace(s.data.Documents[i].WorkspaceID) == "" {
			s.data.Documents[i].WorkspaceID = domain.DefaultWorkspaceID
			migrated = true
		}
	}
	for i := range s.data.Memories {
		if strings.TrimSpace(s.data.Memories[i].WorkspaceID) == "" {
			s.data.Memories[i].WorkspaceID = domain.DefaultWorkspaceID
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
