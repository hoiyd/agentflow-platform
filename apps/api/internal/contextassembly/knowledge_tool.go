package contextassembly

import (
	"agentflow-platform/apps/api/internal/domain"
	"encoding/json"
)

func knowledgeToolCitation(message Message, transformation string) string {
	if message.Role != "tool" || transformation != "original" {
		return ""
	}
	var result struct {
		Tool      string                         `json:"tool"`
		Error     json.RawMessage                `json:"error"`
		Truncated bool                           `json:"truncated"`
		Result    domain.KnowledgeToolReadResult `json:"result"`
	}
	if json.Unmarshal([]byte(message.Content), &result) != nil || result.Tool != domain.KnowledgeReadToolName || result.Truncated || len(result.Error) > 0 && string(result.Error) != "null" || result.Result.Content == "" {
		return ""
	}
	return result.Result.Source.SourceID
}

func knowledgeReadCandidates(reads []domain.KnowledgeToolReadResult) []candidate {
	items := make([]candidate, 0, len(reads))
	for _, read := range reads {
		// JSON escapes tag delimiters in data while retaining the original excerpt.
		encoded, _ := json.Marshal(read)
		formatted := "<untrusted_knowledge_read>\n" + string(encoded) + "\n</untrusted_knowledge_read>"
		items = append(items, candidate{formatted: formatted, entry: domain.ContextManifestEntry{
			Source: SourceKnowledge, ReferenceID: read.Source.ToolCallID, CitationSourceID: read.Source.SourceID,
			Reason: "knowledge_budget_exceeded", Transformation: "knowledge_read_wrapped",
			EstimatedTokens: EstimateTokens(formatted), OriginalBytes: len(read.Content),
		}})
	}
	return items
}
