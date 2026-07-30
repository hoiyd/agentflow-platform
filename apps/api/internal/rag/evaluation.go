package rag

import (
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func EvaluateCase(evalCase domain.RAGEvaluationCase, items []domain.RetrievedDocumentChunk) domain.RAGEvaluationCaseResult {
	result := domain.RAGEvaluationCaseResult{
		ID:                    strings.TrimSpace(evalCase.ID),
		Query:                 strings.TrimSpace(evalCase.Query),
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
	bestRank := 0
	for index, item := range items {
		if ragCaseItemMatches(evalCase, item) {
			bestRank = index + 1
			break
		}
	}
	if bestRank == 0 {
		result.FailureReason = "no result matched expected document, chunk, or content"
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

func ragCaseItemMatches(evalCase domain.RAGEvaluationCase, item domain.RetrievedDocumentChunk) bool {
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

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}
