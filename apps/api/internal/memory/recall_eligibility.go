package memory

import (
	"regexp"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

var ticketID = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*-[0-9]+$`)

func applyQwen3RecallGate(search *domain.MemorySearch) {
	if search.EmbeddingProvider != "ollama" || strings.TrimSuffix(search.EmbeddingModel, ":latest") != "qwen3-embedding" || len(search.Embedding) != 1536 {
		return
	}
	// Calibrated on the local Qwen3 profile, not on recency-adjusted scores.
	search.MinSimilarity = 0.60
	var ids []string
	for _, token := range strings.FieldsFunc(strings.ToLower(search.Query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) {
		if ticketID.MatchString(token) {
			ids = append(ids, token)
		}
	}
	if len(ids) > 0 {
		search.RequiredContentPattern = `(^|[^a-z0-9_-])(` + strings.Join(ids, "|") + `)($|[^a-z0-9_-])`
	}
}
