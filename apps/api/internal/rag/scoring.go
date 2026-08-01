package rag

import (
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

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

func relevanceConfidence(item domain.RetrievedDocumentChunk, reranker domain.RerankerInfo) (string, string) {
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
	if reranker.Algorithm != heuristicRerankerAlgorithm {
		if item.RerankScore >= 0.75 {
			return "high", "model reranker score passed high-confidence gate"
		}
		if item.RerankScore >= 0.58 {
			return "medium", "model reranker score passed relevance gate"
		}
	}
	if len(item.MatchedTerms) > 0 && item.Similarity >= 0.30 {
		return "medium", "evidence terms matched with acceptable similarity"
	}
	// The v1 heuristic policy classified before EvidenceScore was added to the
	// final rank score. Subtract it here to preserve that behavior after the Gate
	// became an independent stage.
	heuristicScore := item.RerankScore - item.EvidenceScore
	if reranker.Algorithm == heuristicRerankerAlgorithm && heuristicScore >= 0.58 && len(item.MatchedTerms) > 0 {
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
