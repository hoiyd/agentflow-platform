package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	MaxToolArtifactReadBytes   = 64 * 1024
	MaxToolArtifactSearchQuery = 256
	MaxToolArtifactMatches     = 20
)

var (
	ErrToolArtifactExpired = errors.New("tool artifact expired")
	ErrToolArtifactRange   = errors.New("tool artifact range is invalid")
)

func toolArtifactContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeArtifactRead(offset int, limit int) (int, int, error) {
	if offset < 0 {
		return 0, 0, errors.New("artifact offset cannot be negative")
	}
	if limit <= 0 {
		limit = 8 * 1024
	}
	if limit > MaxToolArtifactReadBytes {
		return 0, 0, fmt.Errorf("artifact read limit cannot exceed %d bytes", MaxToolArtifactReadBytes)
	}
	return offset, limit, nil
}

func normalizeArtifactSearch(query string, maxMatches int) (string, int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", 0, errors.New("artifact search query is required")
	}
	if len([]byte(query)) > MaxToolArtifactSearchQuery {
		return "", 0, fmt.Errorf("artifact search query cannot exceed %d bytes", MaxToolArtifactSearchQuery)
	}
	if maxMatches <= 0 {
		maxMatches = 5
	}
	if maxMatches > MaxToolArtifactMatches {
		return "", 0, fmt.Errorf("artifact search matches cannot exceed %d", MaxToolArtifactMatches)
	}
	return query, maxMatches, nil
}

func validateToolArtifact(artifact domain.ToolArtifact, content []byte) error {
	if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.RunID) == "" ||
		strings.TrimSpace(artifact.ToolCallID) == "" || strings.TrimSpace(artifact.ToolName) == "" {
		return errors.New("tool artifact requires id, run, tool call, and tool name")
	}
	if artifact.SchemaVersion != domain.CurrentToolArtifactSchemaVersion {
		return errors.New("unsupported tool artifact schema version")
	}
	if artifact.MediaType != "application/json" && artifact.MediaType != "text/plain" {
		return errors.New("tool artifact media type must be application/json or text/plain")
	}
	if artifact.StoredByteSize != len(content) || artifact.StoredByteSize < 0 || artifact.OriginalByteSize < 0 {
		return errors.New("tool artifact byte sizes do not match content")
	}
	if artifact.ContentHash != toolArtifactContentHash(content) {
		return errors.New("tool artifact content hash mismatch")
	}
	if artifact.CreatedAt.IsZero() {
		return errors.New("tool artifact created_at is required")
	}
	return nil
}

func toolArtifactExpired(artifact domain.ToolArtifact, now time.Time) bool {
	return artifact.ExpiresAt != nil && !now.Before(*artifact.ExpiresAt)
}

func searchToolArtifact(artifact domain.ToolArtifact, content []byte, query string, maxMatches int) domain.ToolArtifactSearchResult {
	text := string(content)
	matches := make([]domain.ToolArtifactSearchMatch, 0, maxMatches)
	truncated := false
	for cursor := 0; cursor < len(text); {
		relative := strings.Index(text[cursor:], query)
		if relative < 0 {
			break
		}
		offset := cursor + relative
		if len(matches) == maxMatches {
			truncated = true
			break
		}
		start := max(0, offset-80)
		end := min(len(content), offset+len(query)+160)
		matches = append(matches, domain.ToolArtifactSearchMatch{Offset: offset, Preview: string(content[start:end])})
		cursor = offset + max(1, len(query))
	}
	return domain.ToolArtifactSearchResult{
		Artifact: artifact, Query: query, Matches: matches,
		ScannedBytes: len(content), Truncated: truncated,
	}
}

func toolArtifactsForRun(items []domain.ToolArtifact, runID string) []domain.ToolArtifact {
	result := make([]domain.ToolArtifact, 0)
	now := time.Now().UTC()
	for _, artifact := range items {
		if artifact.RunID == runID {
			artifact.Expired = toolArtifactExpired(artifact, now)
			result = append(result, artifact)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}
