package openai

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
)

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type embeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

type ollamaEmbeddingResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

func buildMessages(history []domain.Message) []Message {
	return buildMessagesWithToolNames(history, []string{"calculator", "get_current_time"})
}

func buildMessagesWithToolNames(history []domain.Message, toolNames []string) []Message {
	return buildMessagesWithSystemPrompt("You are AgentFlow's assistant. Use tools when they help.", history, toolNames)
}

func buildMessagesWithSystemPrompt(systemPrompt string, history []domain.Message, toolNames []string) []Message {
	available := "No tools are currently enabled."
	if len(toolNames) > 0 {
		available = "Available tools include " + strings.Join(toolNames, ", ") + "."
	}
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		systemPrompt = "You are AgentFlow's assistant."
	}
	messages := []Message{
		{
			Role: "system", Content: systemPrompt + " " + available,
			Source: contextassembly.SourceSystem, ReferenceID: "system_prompt",
		},
	}
	lastUser := -1
	for index := range history {
		if history[index].Role == "user" {
			lastUser = index
		}
	}
	for index, item := range history {
		if item.Role == "user" || item.Role == "assistant" {
			source := contextassembly.SourceHistory
			if index == lastUser {
				source = contextassembly.SourceCurrentInput
			}
			messages = append(messages, Message{Role: item.Role, Content: item.Content, Source: source, ReferenceID: item.ID})
		}
	}
	return messages
}

func ensureCurrentInput(messages []Message, latest string) []Message {
	latest = strings.TrimSpace(latest)
	if latest == "" {
		return messages
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Source == contextassembly.SourceCurrentInput {
			if strings.TrimSpace(messages[index].Content) == latest {
				return messages
			}
			messages[index].Source = contextassembly.SourceHistory
			break
		}
	}
	return append(messages, Message{
		Role: "user", Content: latest, Source: contextassembly.SourceCurrentInput, ReferenceID: "current_input",
	})
}

func normalizeToolCalls(toolCalls []ToolCall) []ToolCall {
	normalized := make([]ToolCall, 0, len(toolCalls))
	for index, call := range toolCalls {
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", index+1)
		}
		if call.Type == "" {
			call.Type = "function"
		}
		if strings.TrimSpace(call.Function.Arguments) == "" {
			call.Function.Arguments = "{}"
		}
		normalized = append(normalized, call)
	}
	return normalized
}
