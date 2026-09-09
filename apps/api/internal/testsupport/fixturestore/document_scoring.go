package fixturestore

import (
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func DocumentChunkMatchesSearch(document domain.Document, chunk domain.DocumentChunk, search domain.DocumentSearch) bool {
	if store.NormalizeWorkspaceID(document.WorkspaceID) != store.NormalizeWorkspaceID(search.WorkspaceID) {
		return false
	}
	for key, expected := range search.Metadata {
		value, ok := chunk.Metadata[key]
		if !ok {
			value, ok = document.Metadata[key]
		}
		if !ok || strings.TrimSpace(expected) != strings.TrimSpace(toString(value)) {
			return false
		}
	}
	return true
}

func DocumentChunkLexicalScore(search domain.DocumentSearch, document domain.Document, chunk domain.DocumentChunk) float64 {
	query := strings.ToLower(strings.TrimSpace(search.Query))
	if query == "" {
		return 0
	}
	text := strings.ToLower(strings.Join([]string{
		document.Title,
		document.SourceURI,
		chunk.Content,
		toString(document.Metadata["filename"]),
		toString(chunk.Metadata["title"]),
		toString(chunk.Metadata["heading_path"]),
	}, " "))
	score := 0.0
	if strings.Contains(text, query) {
		score = 1
	}
	if lexicalIdentifierMatch(query, text) {
		score = 1
	}
	if len(search.LexicalTerms) > 0 {
		matches := 0
		for _, term := range search.LexicalTerms {
			if strings.Contains(text, strings.ToLower(strings.TrimSpace(term))) {
				matches++
			}
		}
		coverage := float64(matches) / float64(len(search.LexicalTerms))
		if candidate := coverage * 0.60; candidate > score {
			score = candidate
		}
	}
	if score > 1 {
		return 1
	}
	return score
}

func lexicalIdentifierMatch(query string, text string) bool {
	for _, token := range strings.Fields(query) {
		token = strings.Trim(token, ".,;:!?()[]{}<>\"'`，。；：！？（）【】")
		if len([]rune(token)) < 3 || !containsASCIIDigit(token) {
			continue
		}
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func containsASCIIDigit(value string) bool {
	for _, r := range value {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func AbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
