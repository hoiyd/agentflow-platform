package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"

	"sort"
)

func (s *Store) hasConversationLocked(id string) bool {
	for _, item := range s.data.Conversations {
		if item.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) hasRunLocked(id string) bool {
	for _, item := range s.data.Runs {
		if item.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) hasAgentLocked(id string) bool {
	for _, item := range s.data.Agents {
		if item.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) getRunLocked(id string) (domain.Run, bool) {
	for _, item := range s.data.Runs {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Run{}, false
}

func (s *Store) getConversationLocked(id string) (domain.Conversation, bool) {
	for _, item := range s.data.Conversations {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Conversation{}, false
}

func (s *Store) messagesForConversationLocked(conversationID string) []domain.Message {
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
