package store

import (
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func normalizeMemoryCandidate(candidate domain.MemoryCandidate) (domain.MemoryCandidate, error) {
	candidate.ID = strings.TrimSpace(candidate.ID)
	candidate.SourceMessageID = strings.TrimSpace(candidate.SourceMessageID)
	candidate.SourceRole = strings.TrimSpace(candidate.SourceRole)
	candidate.Kind = strings.TrimSpace(candidate.Kind)
	candidate.Content = strings.TrimSpace(candidate.Content)
	if candidate.ID == "" {
		return domain.MemoryCandidate{}, errors.New("memory candidate id is required")
	}
	if candidate.SourceMessageID == "" {
		return domain.MemoryCandidate{}, errors.New("memory candidate source message id is required")
	}
	if candidate.Kind == "" || candidate.Content == "" {
		return domain.MemoryCandidate{}, errors.New("memory candidate kind and content are required")
	}
	if candidate.Status != domain.MemoryCandidateAccepted && candidate.Status != domain.MemoryCandidateRejected {
		return domain.MemoryCandidate{}, errors.New("memory candidate status is invalid")
	}
	if candidate.Confidence == 0 && candidate.ExtractionReason != "adaptive_model" {
		candidate.Confidence = 1
	}
	if candidate.Confidence < 0 || candidate.Confidence > 1 {
		return domain.MemoryCandidate{}, errors.New("memory candidate confidence must be between 0 and 1")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	return candidate, nil
}
