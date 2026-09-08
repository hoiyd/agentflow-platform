package store

import (
	"agentflow-platform/apps/api/internal/domain"
)

func CloneCitations(citations []domain.RAGCitation) []domain.RAGCitation {
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

func CloneContextCompaction(item domain.ContextCompaction) domain.ContextCompaction {
	if item.Status == "" {
		item.Status = domain.ContextCompactionCompleted
	}
	if item.Generation <= 0 {
		item.Generation = 1
	}
	if item.ReplacementSummaryID == "" && item.ID != "" {
		item.ReplacementSummaryID = "summary:" + item.ID
	}
	if item.ShadowedMessageRange.MessageCount == 0 && len(item.SourceMessageIDs) > 0 {
		item.ShadowedMessageRange = domain.ContextShadowedRange{
			FirstMessageID: item.SourceMessageIDs[0], LastMessageID: item.SourceMessageIDs[len(item.SourceMessageIDs)-1],
			MessageCount: len(item.SourceMessageIDs),
		}
	}
	item.SourceMessageIDs = append([]string(nil), item.SourceMessageIDs...)
	item.SourceEventIDs = append([]string(nil), item.SourceEventIDs...)
	return item
}

func CompletedContextCompaction(item domain.ContextCompaction) bool {
	// Empty status is the pre-H-06 file format and is treated as completed.
	return item.Status == "" || item.Status == domain.ContextCompactionCompleted
}
