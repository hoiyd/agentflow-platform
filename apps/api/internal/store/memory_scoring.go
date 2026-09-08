package store

import (
	"encoding/json"

	"fmt"
	"math"

	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func MemoryMatchesSearch(memory domain.Memory, search domain.MemorySearch) bool {
	if memory.DeletedAt != nil {
		return false
	}
	if NormalizeWorkspaceID(memory.WorkspaceID) != NormalizeWorkspaceID(search.WorkspaceID) {
		return false
	}
	if search.UserID != "" && memory.UserID != search.UserID {
		return false
	}
	if search.ProjectID != "" && memory.ProjectID != search.ProjectID {
		return false
	}
	for key, expected := range search.Metadata {
		value, ok := memory.Metadata[key]
		if !ok || strings.TrimSpace(expected) != strings.TrimSpace(toString(value)) {
			return false
		}
	}
	return true
}

func CosineSimilarity(a []float64, b []float64) float64 {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	if limit == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < limit; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func MemoryRecencyBoost(now time.Time, createdAt time.Time) float64 {
	if createdAt.IsZero() {
		return 0
	}
	ageDays := now.Sub(createdAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return 0.05 / (1 + ageDays/30)
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		bytes, _ := json.Marshal(v)
		return string(bytes)
	}
}
