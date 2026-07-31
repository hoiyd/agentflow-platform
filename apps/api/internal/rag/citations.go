package rag

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

var citationMarkerPattern = regexp.MustCompile(`(?i)\[S([0-9]+)\]`)

// AssignCitationSources numbers only the transformed chunks eligible for model
// context. Ordering is deterministic for a given ContextItems result.
func AssignCitationSources(items []domain.RetrievedDocumentChunk) ([]domain.RetrievedDocumentChunk, []domain.RAGCitation) {
	assigned := append([]domain.RetrievedDocumentChunk(nil), items...)
	sources := make([]domain.RAGCitation, 0, len(assigned))
	for index := range assigned {
		sourceID := fmt.Sprintf("S%d", index+1)
		assigned[index].SourceID = sourceID
		sources = append(sources, citationFromChunk(sourceID, assigned[index]))
	}
	return assigned, sources
}

// ResolveCitations accepts only markers present in the trusted source catalog.
// Returned citations preserve first appearance order and deduplicate repeats.
func ResolveCitations(answer string, sources []domain.RAGCitation) ([]domain.RAGCitation, []string) {
	available := make(map[string]domain.RAGCitation, len(sources))
	for _, source := range sources {
		available[strings.ToUpper(source.SourceID)] = source
	}
	resolved := make([]domain.RAGCitation, 0)
	invalid := make([]string, 0)
	seenResolved := map[string]bool{}
	seenInvalid := map[string]bool{}
	for _, match := range citationMarkerPattern.FindAllStringSubmatch(answer, -1) {
		number, _ := strconv.Atoi(match[1])
		sourceID := fmt.Sprintf("S%d", number)
		if source, ok := available[sourceID]; ok {
			if !seenResolved[sourceID] {
				resolved = append(resolved, source)
				seenResolved[sourceID] = true
			}
			continue
		}
		if !seenInvalid[sourceID] {
			invalid = append(invalid, sourceID)
			seenInvalid[sourceID] = true
		}
	}
	return resolved, invalid
}

func citationFromChunk(sourceID string, item domain.RetrievedDocumentChunk) domain.RAGCitation {
	sourceChunkIDs := append([]string(nil), item.SourceChunkIDs...)
	if len(sourceChunkIDs) == 0 && item.Chunk.ID != "" {
		sourceChunkIDs = []string{item.Chunk.ID}
	}
	documentVersion := item.Chunk.DocumentVersion
	if documentVersion == "" {
		documentVersion = item.Document.Version
	}
	return domain.RAGCitation{
		SourceID: sourceID, DocumentID: item.Document.ID, DocumentTitle: item.Document.Title,
		DocumentVersion: documentVersion, ChunkID: item.Chunk.ID,
		SourceChunkIDs: sourceChunkIDs, SectionPath: append([]string(nil), item.Chunk.SectionPath...),
		StartOffset: item.Chunk.StartOffset, EndOffset: item.Chunk.EndOffset,
	}
}
