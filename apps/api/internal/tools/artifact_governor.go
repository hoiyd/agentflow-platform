package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

const (
	DefaultMaxArtifactBytes     = 5 * 1024 * 1024
	DefaultArtifactPreviewBytes = 1_000
	DefaultArtifactRetention    = 7 * 24 * time.Hour
)

type ToolArtifactWriter interface {
	CreateToolArtifact(domain.ToolArtifact, []byte) (domain.ToolArtifact, error)
}

type resultArtifactGovernor struct {
	store        ToolArtifactWriter
	maxBytes     int
	previewBytes int
	retention    time.Duration
	redact       func([]byte) ([]byte, int, error)
	now          func() time.Time
}

func newResultArtifactGovernor(options ExecutorOptions) *resultArtifactGovernor {
	maxBytes := options.MaxArtifactBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxArtifactBytes
	}
	previewBytes := options.ArtifactPreviewBytes
	if previewBytes <= 0 {
		previewBytes = DefaultArtifactPreviewBytes
	}
	retention := options.ArtifactRetention
	if retention <= 0 {
		retention = DefaultArtifactRetention
	}
	redact := options.RedactArtifactJSON
	if redact == nil {
		redact = redaction.JSON
	}
	return &resultArtifactGovernor{
		store: options.ArtifactStore, maxBytes: maxBytes, previewBytes: previewBytes,
		retention: retention, redact: redact, now: func() time.Time { return time.Now().UTC() },
	}
}

func (e *Executor) finishBatch(ctx context.Context, requests []ExecutionRequest, results []ExecutionResult) []ExecutionResult {
	used := 0
	for index := range results {
		if results[index].Error == nil && !results[index].Truncated && len(results[index].encodedResult) > 0 {
			if used+len(results[index].encodedResult) > e.maxBatchResultBytes {
				remaining := max(0, e.maxBatchResultBytes-used)
				results[index] = e.artifactGovernor.govern(ctx, requests[index], results[index], results[index].encodedResult, remaining)
			} else {
				used += len(results[index].encodedResult)
			}
		}
		if results[index].Truncated {
			remaining := max(0, e.maxBatchResultBytes-used)
			previewBytes := boundArtifactPreview(&results[index], remaining)
			used += previewBytes
		}
		results[index].encodedResult = nil
		if e.tracer != nil {
			e.tracer.ToolFinished(ctx, results[index])
		}
	}
	return results
}

func boundArtifactPreview(result *ExecutionResult, limit int) int {
	if result == nil {
		return 0
	}
	value, ok := result.Result.(map[string]any)
	if !ok {
		return 0
	}
	preview, _ := value["preview"].(string)
	if limit <= 0 {
		value["preview"] = ""
		return 0
	}
	bounded := truncateUTF8([]byte(preview), min(limit, len([]byte(preview))))
	value["preview"] = bounded
	return len([]byte(bounded))
}

func (g *resultArtifactGovernor) govern(ctx context.Context, request ExecutionRequest, result ExecutionResult, encoded []byte, previewLimit int) ExecutionResult {
	_ = ctx
	result.Truncated = true
	result.OriginalResultBytes = len(encoded)
	result.encodedResult = nil
	if previewLimit > 0 {
		previewLimit = minPositive(previewLimit, g.previewBytes)
	}
	redacted, count, err := g.redact(encoded)
	if err != nil {
		return artifactDegraded(result, nil, previewLimit, "tool result redaction failed", err)
	}
	if len(redacted) > g.maxBytes {
		return artifactDegraded(result, redacted, previewLimit, fmt.Sprintf("tool result exceeds artifact limit of %d bytes", g.maxBytes), nil)
	}
	if g.store == nil {
		return artifactDegraded(result, redacted, previewLimit, "tool artifact store is unavailable", nil)
	}
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.CallID) == "" {
		return artifactDegraded(result, redacted, previewLimit, "tool artifact requires run and call identity", nil)
	}
	now := g.now()
	expiresAt := now.Add(g.retention)
	contentHash := artifactHash(redacted)
	artifact := domain.ToolArtifact{
		ID: artifactID(request, contentHash), SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: request.RunID, StageID: request.StageID, TurnID: request.TurnID,
		ToolCallID: request.CallID, ToolName: request.Tool, DefinitionRevision: request.DefinitionRevision,
		MediaType: "application/json", ContentHash: contentHash,
		OriginalByteSize: len(encoded), StoredByteSize: len(redacted),
		Redacted: count > 0, RedactionStrategy: redaction.DeterministicStrategy, RedactionCount: count,
		CreatedAt: now, ExpiresAt: &expiresAt,
	}
	created, err := g.store.CreateToolArtifact(artifact, redacted)
	if err != nil {
		return artifactDegraded(result, redacted, previewLimit, "persist tool artifact: "+err.Error(), err)
	}
	reference := created.Reference()
	result.Artifact = &reference
	result.Result = artifactPreview(redacted, previewLimit, &reference, nil)
	return result
}

func artifactDegraded(result ExecutionResult, safeContent []byte, previewLimit int, message string, cause error) ExecutionResult {
	artifactErr := executionError(ErrorArtifactUnavailable, message, cause)
	result.ArtifactError = artifactErr
	result.Result = artifactPreview(safeContent, previewLimit, nil, artifactErr)
	return result
}

func artifactPreview(content []byte, limit int, reference *domain.ToolArtifactReference, artifactErr *ExecutionError) map[string]any {
	preview := "[tool result omitted because safe artifact storage was unavailable]"
	if limit <= 0 {
		preview = ""
	} else if len(content) > 0 {
		preview = truncateUTF8(content, min(limit, len(content)))
	} else if len([]byte(preview)) > limit {
		preview = truncateUTF8([]byte(preview), limit)
	}
	value := map[string]any{"preview": preview, "truncated": true, "content_complete": false}
	if reference != nil {
		value["artifact"] = reference
	}
	if artifactErr != nil {
		value["artifact_unavailable"] = true
		value["artifact_error_code"] = string(artifactErr.Code)
	}
	return value
}

func artifactID(request ExecutionRequest, contentHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{request.RunID, request.StageID, request.CallID, request.Tool, contentHash}, "\x00")))
	return "tool_artifact_" + hex.EncodeToString(sum[:16])
}

func artifactHash(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func minPositive(values ...int) int {
	minimum := 0
	for _, value := range values {
		if value > 0 && (minimum == 0 || value < minimum) {
			minimum = value
		}
	}
	return minimum
}
