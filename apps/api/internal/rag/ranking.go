package rag

import (
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func NormalizeSearchLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 10 {
		return 10
	}
	return limit
}

func CandidateLimit(limit int) int {
	if limit <= 0 {
		limit = 5
	}
	candidateLimit := limit * 4
	if candidateLimit < 10 {
		candidateLimit = 10
	}
	if candidateLimit > 20 {
		candidateLimit = 20
	}
	return candidateLimit
}

func Rerank(query string, items []domain.RetrievedDocumentChunk, limit int) []domain.RetrievedDocumentChunk {
	if len(items) == 0 {
		return items
	}
	queryTerms := QueryTerms(query)
	for index := range items {
		items[index].LexicalBoost = lexicalBoost(query, queryTerms, items[index].Chunk.Content)
		items[index].MetadataBoost = metadataBoost(query, queryTerms, items[index])
		items[index].RerankScore = items[index].Score + items[index].LexicalBoost + items[index].MetadataBoost
		items[index].MatchedTerms = matchedTerms(query, queryTerms, items[index])
		items[index].EvidenceCoverage = evidenceCoverage(queryTerms, items[index].MatchedTerms)
		items[index].EvidenceScore = evidenceScore(query, queryTerms, items[index])
		items[index].Confidence, items[index].FilterReason = relevanceConfidence(items[index])
		items[index].RerankScore += items[index].EvidenceScore
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].RerankScore > items[j].RerankScore
	})

	selected := make([]domain.RetrievedDocumentChunk, 0, minInt(limit, len(items)))
	usedDocuments := map[string]int{}
	for _, item := range items {
		if len(selected) >= limit {
			break
		}
		documentUses := usedDocuments[item.Document.ID]
		if documentUses > 0 && hasUnselectedDocument(items, usedDocuments) {
			item.DiversityPenalty = 0.04 * float64(documentUses)
			item.RerankScore -= item.DiversityPenalty
			if documentUses >= 2 {
				continue
			}
		}
		selected = append(selected, item)
		usedDocuments[item.Document.ID]++
	}
	if len(selected) < limit {
		seenChunks := map[string]bool{}
		for _, item := range selected {
			seenChunks[item.Chunk.ID] = true
		}
		for _, item := range items {
			if len(selected) >= limit {
				break
			}
			if seenChunks[item.Chunk.ID] {
				continue
			}
			selected = append(selected, item)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].RerankScore > selected[j].RerankScore
	})
	for index := range selected {
		selected[index].RerankRank = index + 1
	}
	return selected
}

func ApplyRelevanceGate(items []domain.RetrievedDocumentChunk) []domain.RetrievedDocumentChunk {
	if len(items) == 0 {
		return items
	}
	filtered := make([]domain.RetrievedDocumentChunk, 0, len(items))
	for _, item := range items {
		if item.Confidence == "low" {
			continue
		}
		filtered = append(filtered, item)
	}
	for index := range filtered {
		filtered[index].RerankRank = index + 1
	}
	return filtered
}

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

func hasUnselectedDocument(items []domain.RetrievedDocumentChunk, usedDocuments map[string]int) bool {
	for _, item := range items {
		if usedDocuments[item.Document.ID] == 0 {
			return true
		}
	}
	return false
}

func lexicalBoost(query string, queryTerms []string, content string) float64 {
	content = strings.ToLower(content)
	query = strings.ToLower(strings.TrimSpace(query))
	boost := 0.0
	if query != "" && strings.Contains(content, query) {
		boost += 0.18
	}
	if len(queryTerms) == 0 {
		return boost
	}
	matches := 0
	for _, term := range queryTerms {
		if strings.Contains(content, term) {
			matches++
		}
	}
	boost += 0.14 * float64(matches) / float64(len(queryTerms))
	return boost
}

func metadataBoost(query string, queryTerms []string, item domain.RetrievedDocumentChunk) float64 {
	text := strings.Join([]string{
		item.Document.Title,
		item.Document.SourceURI,
		metadataText(item.Document.Metadata, "filename"),
		metadataText(item.Chunk.Metadata, "title"),
		metadataText(item.Chunk.Metadata, "heading_path"),
		metadataText(item.Chunk.Metadata, "chunk_type"),
	}, " ")
	text = strings.ToLower(text)
	query = strings.ToLower(strings.TrimSpace(query))
	boost := 0.0
	if query != "" && strings.Contains(text, query) {
		boost += 0.12
	}
	if len(queryTerms) == 0 {
		return boost
	}
	matches := 0
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			matches++
		}
	}
	boost += 0.10 * float64(matches) / float64(len(queryTerms))
	return boost
}

func evidenceCoverage(queryTerms []string, matchedTerms []string) float64 {
	if len(queryTerms) == 0 {
		if len(matchedTerms) > 0 {
			return 1
		}
		return 0
	}
	if len(matchedTerms) == 0 {
		return 0
	}
	matched := map[string]bool{}
	for _, term := range matchedTerms {
		matched[strings.TrimSpace(term)] = true
	}
	count := 0
	for _, term := range queryTerms {
		if matched[strings.TrimSpace(term)] {
			count++
		}
	}
	return float64(count) / float64(len(queryTerms))
}

func evidenceScore(query string, queryTerms []string, item domain.RetrievedDocumentChunk) float64 {
	score := item.EvidenceCoverage * 0.16
	exact := strings.ToLower(strings.TrimSpace(query))
	if exact != "" {
		content := strings.ToLower(item.Chunk.Content)
		metadata := strings.ToLower(strings.Join([]string{
			item.Document.Title,
			item.Document.SourceURI,
			metadataText(item.Document.Metadata, "filename"),
			metadataText(item.Chunk.Metadata, "title"),
			metadataText(item.Chunk.Metadata, "heading_path"),
		}, " "))
		if strings.Contains(content, exact) {
			score += 0.18
		}
		if strings.Contains(metadata, exact) {
			score += 0.14
		}
	}
	if len(queryTerms) > 0 && item.EvidenceCoverage >= 0.5 {
		score += 0.06
	}
	return score
}

func relevanceConfidence(item domain.RetrievedDocumentChunk) (string, string) {
	if item.LexicalRank > 0 && item.LexicalScore >= 0.95 {
		return "high", "strong lexical recall match"
	}
	if item.EvidenceCoverage >= 0.6 || item.EvidenceScore >= 0.24 {
		return "high", "strong evidence match"
	}
	if item.Similarity >= 0.72 {
		return "high", "strong vector similarity"
	}
	if item.Similarity >= 0.60 {
		return "medium", "vector similarity passed conservative gate"
	}
	if len(item.MatchedTerms) > 0 && item.Similarity >= 0.30 {
		return "medium", "evidence terms matched with acceptable similarity"
	}
	if item.RerankScore >= 0.58 && len(item.MatchedTerms) > 0 {
		return "medium", "rerank score passed with evidence"
	}
	if item.LexicalRank > 0 && item.LexicalScore >= 0.20 && len(item.MatchedTerms) > 0 {
		return "medium", "lexical recall passed with supporting terms"
	}
	return "low", "filtered: weak similarity and no supporting evidence"
}

func matchedTerms(query string, queryTerms []string, item domain.RetrievedDocumentChunk) []string {
	text := strings.ToLower(strings.Join([]string{
		item.Document.Title,
		item.Document.SourceURI,
		item.Chunk.Content,
		metadataText(item.Document.Metadata, "filename"),
		metadataText(item.Chunk.Metadata, "title"),
		metadataText(item.Chunk.Metadata, "heading_path"),
		metadataText(item.Chunk.Metadata, "chunk_type"),
	}, " "))
	matches := []string{}
	seen := map[string]bool{}
	exact := strings.ToLower(strings.TrimSpace(query))
	if exact != "" && strings.Contains(text, exact) {
		seen[exact] = true
		matches = append(matches, exact)
	}
	for _, term := range queryTerms {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			continue
		}
		if strings.Contains(text, term) {
			seen[term] = true
			matches = append(matches, term)
		}
	}
	return matches
}

func metadataText(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(metadataValueString(value))
}

func metadataValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func QueryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !(r >= '\u4e00' && r <= '\u9fff')
	})
	terms := make([]string, 0, len(fields)+len([]rune(query)))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
	}
	runes := []rune(strings.TrimSpace(query))
	for i := 0; i+1 < len(runes); i++ {
		if !isCJK(runes[i]) || !isCJK(runes[i+1]) {
			continue
		}
		term := string(runes[i : i+2])
		if seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func isCJK(r rune) bool {
	return r >= '\u4e00' && r <= '\u9fff'
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
