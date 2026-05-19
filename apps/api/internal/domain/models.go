package domain

import "time"

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type ChatRequest struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
}

type ChatChunk struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	Delta          string `json:"delta,omitempty"`
	ToolCallID     string `json:"tool_call_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	Arguments      string `json:"arguments,omitempty"`
	Result         string `json:"result,omitempty"`
	DurationMS     int64  `json:"duration_ms,omitempty"`
	Error          string `json:"error,omitempty"`
}
