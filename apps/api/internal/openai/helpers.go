package openai

import (
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func parseFallbackToolCall(content string) (ToolCall, bool) {
	var payload struct {
		Action    string          `json:"action"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	candidate := extractJSONObject(content)
	if candidate == "" {
		return ToolCall{}, false
	}
	if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
		return ToolCall{}, false
	}
	if payload.Action != "tool_call" || payload.Tool == "" {
		return ToolCall{}, false
	}
	if len(payload.Arguments) == 0 {
		payload.Arguments = json.RawMessage(`{}`)
	}
	return ToolCall{
		ID:   "fallback_call_1",
		Type: "function",
		Function: FunctionCall{
			Name:      payload.Tool,
			Arguments: string(payload.Arguments),
		},
	}, true
}

func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return ""
	}
	return content[start : end+1]
}

func suffix(index int, total int) string {
	if index == total-1 {
		return ""
	}
	return " "
}

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
		total += len(message.Content)
	}
	return total
}

func messagesToText(messages []Message) string {
	var builder strings.Builder
	for _, message := range messages {
		if message.Content == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(message.Content)
		builder.WriteString("\n")
	}
	return builder.String()
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

func deterministicEmbedding(input string, dimensions int) []float64 {
	if dimensions <= 0 {
		dimensions = 1536
	}
	vector := make([]float64, dimensions)
	words := strings.Fields(strings.ToLower(input))
	if len(words) == 0 {
		words = []string{input}
	}
	for _, word := range words {
		sum := sha256.Sum256([]byte(word))
		for i := 0; i < len(sum); i += 2 {
			index := int(sum[i])<<8 + int(sum[i+1])
			index = index % dimensions
			sign := 1.0
			if sum[(i+1)%len(sum)]%2 == 0 {
				sign = -1
			}
			vector[index] += sign
		}
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] = vector[i] / norm
	}
	return vector
}

func (u Usage) Valid() bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0
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
