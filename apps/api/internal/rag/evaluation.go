package rag

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func EvaluateCase(evalCase domain.RAGEvaluationCase, items []domain.RetrievedDocumentChunk) domain.RAGEvaluationCaseResult {
	answerable := true
	if evalCase.Answerable != nil {
		answerable = *evalCase.Answerable
	}
	result := domain.RAGEvaluationCaseResult{
		ID:                    strings.TrimSpace(evalCase.ID),
		Query:                 strings.TrimSpace(evalCase.Query),
		Answerable:            answerable,
		ExpectedSources:       evalCase.ExpectedSources,
		ForbiddenSources:      evalCase.ForbiddenSources,
		RequiredSourceCount:   evalCase.RequiredSourceCount,
		ExpectedDocumentIDs:   evalCase.ExpectedDocumentIDs,
		ExpectedChunkIDs:      evalCase.ExpectedChunkIDs,
		ExpectedChunkContains: evalCase.ExpectedChunkContains,
		Tags:                  evalCase.Tags,
		Items:                 items,
	}
	if result.ID == "" {
		result.ID = result.Query
	}
	if result.Query == "" {
		result.FailureReason = "query is required"
		return result
	}
	for index, item := range items {
		if goldenSourceMatchesAny(evalCase.ForbiddenSources, item) {
			result.FailureReason = fmt.Sprintf("forbidden source matched at rank %d", index+1)
			return result
		}
	}
	if !answerable {
		if len(items) > 0 {
			result.FailureReason = fmt.Sprintf("expected no answer, but retrieval returned %d result(s)", len(items))
			return result
		}
		result.Hit = true
		return result
	}
	bestRank, matchedSources := expectedEvidenceRank(evalCase, items)
	if bestRank == 0 {
		if evalCase.RequiredSourceCount > 1 {
			result.FailureReason = fmt.Sprintf("matched %d of %d required expected sources", matchedSources, evalCase.RequiredSourceCount)
		} else {
			result.FailureReason = "no result matched an expected source"
		}
		return result
	}
	result.BestRank = bestRank
	result.HitAt1 = bestRank <= 1
	result.HitAt3 = bestRank <= 3
	result.HitAt5 = bestRank <= 5
	acceptableRank := evalCase.MinAcceptableRank
	if acceptableRank <= 0 {
		acceptableRank = len(items)
	}
	result.Hit = bestRank <= acceptableRank
	return result
}

func expectedEvidenceRank(evalCase domain.RAGEvaluationCase, items []domain.RetrievedDocumentChunk) (int, int) {
	if len(evalCase.ExpectedSources) == 0 {
		for index, item := range items {
			if ragCaseItemMatches(evalCase, item) {
				return index + 1, 1
			}
		}
		return 0, 0
	}

	required := evalCase.RequiredSourceCount
	if required <= 0 {
		required = 1
	}
	matched := 0
	for rank := 1; rank <= len(items); rank++ {
		matched = maximumDistinctSourceMatches(evalCase.ExpectedSources, items[:rank])
		if matched >= required {
			return rank, matched
		}
	}
	return 0, matched
}

func maximumDistinctSourceMatches(sources []domain.RAGGoldenSource, items []domain.RetrievedDocumentChunk) int {
	matchedItemSources := make([]int, len(items))
	for index := range matchedItemSources {
		matchedItemSources[index] = -1
	}
	matched := 0
	for sourceIndex := range sources {
		seenItems := make([]bool, len(items))
		if assignExpectedSource(sourceIndex, sources, items, matchedItemSources, seenItems) {
			matched++
		}
	}
	return matched
}

func assignExpectedSource(sourceIndex int, sources []domain.RAGGoldenSource, items []domain.RetrievedDocumentChunk, matchedItemSources []int, seenItems []bool) bool {
	for itemIndex, item := range items {
		if seenItems[itemIndex] || !goldenSourceMatches(sources[sourceIndex], item) {
			continue
		}
		seenItems[itemIndex] = true
		previousSource := matchedItemSources[itemIndex]
		if previousSource == -1 || assignExpectedSource(previousSource, sources, items, matchedItemSources, seenItems) {
			matchedItemSources[itemIndex] = sourceIndex
			return true
		}
	}
	return false
}

func ragCaseItemMatches(evalCase domain.RAGEvaluationCase, item domain.RetrievedDocumentChunk) bool {
	if len(evalCase.ExpectedSources) > 0 {
		return goldenSourceMatchesAny(evalCase.ExpectedSources, item)
	}
	hasExpectation := false
	if len(evalCase.ExpectedDocumentIDs) > 0 {
		hasExpectation = true
		if !stringInSlice(item.Document.ID, evalCase.ExpectedDocumentIDs) {
			return false
		}
	}
	if len(evalCase.ExpectedChunkIDs) > 0 {
		hasExpectation = true
		if !stringInSlice(item.Chunk.ID, evalCase.ExpectedChunkIDs) {
			return false
		}
	}
	if len(evalCase.ExpectedChunkContains) > 0 {
		hasExpectation = true
		content := strings.ToLower(item.Chunk.Content)
		for _, expected := range evalCase.ExpectedChunkContains {
			expected = strings.ToLower(strings.TrimSpace(expected))
			if expected == "" {
				continue
			}
			if !strings.Contains(content, expected) {
				return false
			}
		}
	}
	return hasExpectation
}

func goldenSourceMatchesAny(sources []domain.RAGGoldenSource, item domain.RetrievedDocumentChunk) bool {
	for _, source := range sources {
		if goldenSourceMatches(source, item) {
			return true
		}
	}
	return false
}

func goldenSourceMatches(source domain.RAGGoldenSource, item domain.RetrievedDocumentChunk) bool {
	hasMatcher := false
	if expected := strings.TrimSpace(source.DocumentID); expected != "" {
		hasMatcher = true
		if item.Document.ID != expected {
			return false
		}
	}
	if expected := strings.TrimSpace(source.ChunkID); expected != "" {
		hasMatcher = true
		if item.Chunk.ID != expected {
			return false
		}
	}
	if expected := strings.TrimSpace(source.SourceURI); expected != "" {
		hasMatcher = true
		if item.Document.SourceURI != expected {
			return false
		}
	}
	if len(source.ContentContains) > 0 {
		hasMatcher = true
		content := strings.ToLower(item.Chunk.Content)
		for _, expected := range source.ContentContains {
			if !strings.Contains(content, strings.ToLower(strings.TrimSpace(expected))) {
				return false
			}
		}
	}
	return hasMatcher
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}
