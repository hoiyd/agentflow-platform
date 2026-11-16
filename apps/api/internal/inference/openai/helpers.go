package openai

import (
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func normalizeBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "https://api.openai.com/v1"
	}
	return strings.TrimRight(baseURL, "/")
}

func messagesTextLength(messages []Message) int {
	total := 0
	for _, message := range messages {
		total += len(messageTextForEstimation(message))
	}
	return total
}

func messagesToText(messages []Message) string {
	var builder strings.Builder
	for _, message := range messages {
		text := messageTextForEstimation(message)
		if text == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(text)
		builder.WriteString("\n")
	}
	return builder.String()
}

func messageTextForEstimation(message Message) string {
	if message.ReasoningContent == nil {
		return message.Content
	}
	return *message.ReasoningContent + message.Content
}

// Request Capture owns opt-in body storage. Ordinary events must not persist
// the provider's reasoning text as a side effect of continuation support.
func messagesForTrace(messages []Message) []Message {
	result := append([]Message(nil), messages...)
	for index := range result {
		result[index].ReasoningContent = nil
	}
	return result
}

func estimateUsage(input string, output string) Usage {
	promptTokens := estimateTokens(input)
	completionTokens := estimateTokens(output)
	return Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		Estimated:        true,
	}
}

func estimateTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return len([]rune(value))/4 + 1
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func tokenPayload(payload map[string]any, usage Usage) map[string]any {
	payload["prompt_tokens"] = usage.PromptTokens
	payload["completion_tokens"] = usage.CompletionTokens
	payload["total_tokens"] = usage.TotalTokens
	payload["token_usage_estimated"] = usage.Estimated
	return payload
}

func retrievedMemoryPayload(memories []domain.RetrievedMemory) []map[string]any {
	items := make([]map[string]any, 0, len(memories))
	for _, memory := range memories {
		items = append(items, map[string]any{
			"id":              memory.Memory.ID,
			"kind":            memory.Memory.Kind,
			"content":         truncateText(memory.Memory.Content, 1200),
			"metadata":        memory.Memory.Metadata,
			"similarity":      memory.Similarity,
			"recency_boost":   memory.RecencyBoost,
			"score":           memory.Score,
			"conversation_id": memory.Memory.ConversationID,
			"run_id":          memory.Memory.RunID,
		})
	}
	return items
}

func retrievedChunkPayload(chunks []domain.RetrievedDocumentChunk) []map[string]any {
	items := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		items = append(items, map[string]any{
			"document_id":      chunk.Document.ID,
			"document_title":   chunk.Document.Title,
			"document_version": chunk.Chunk.DocumentVersion,
			"chunk_id":         chunk.Chunk.ID,
			"parent_id":        chunk.Chunk.ParentID,
			"section_path":     chunk.Chunk.SectionPath,
			"start_offset":     chunk.Chunk.StartOffset,
			"end_offset":       chunk.Chunk.EndOffset,
			"content_hash":     chunk.Chunk.ContentHash,
			"chunk_index":      chunk.Chunk.ChunkIndex,
			"content":          truncateText(chunk.Chunk.Content, 1600),
			"metadata":         chunk.Chunk.Metadata,
			"similarity":       chunk.Similarity,
			"recency_boost":    chunk.RecencyBoost,
			"score":            chunk.Score,
			"vector_rank":      chunk.VectorRank,
			"lexical_rank":     chunk.LexicalRank,
			"lexical_score":    chunk.LexicalScore,
			"rrf_score":        chunk.RRFScore,
			"fusion_rank":      chunk.FusionRank,
			"rerank_rank":      chunk.RerankRank,
			"rerank_score":     chunk.RerankScore,
		})
	}
	return items
}
