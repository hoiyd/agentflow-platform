package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestExecutorGovernsLargeResultsForAnyBinding(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "future_tool", Parameters: ObjectSchema(nil, nil)},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"authorization": "Bearer very-secret-token", "payload": strings.Repeat("x", 100*1024)}, nil
		},
		Policy: ExecutionPolicy{MaxResultBytes: 2 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := &artifactWriterStub{}
	result := NewExecutor(catalog, ExecutorOptions{ArtifactStore: writer, ArtifactPreviewBytes: 512}).Execute(
		context.Background(), ExecutionRequest{RunID: "run-1", StageID: "stage-1", TurnID: "turn-1", CallID: "call-1", Tool: "future_tool"},
	)
	if result.Error != nil || result.Artifact == nil || !result.Truncated || len(writer.content) < 100*1024 {
		t.Fatalf("large result was not durably governed: result=%#v stored=%d", result, len(writer.content))
	}
	if strings.Contains(string(writer.content), "very-secret-token") || !strings.Contains(string(writer.content), "[REDACTED]") {
		t.Fatalf("artifact was persisted before redaction: %q", writer.content[:min(200, len(writer.content))])
	}
	preview := result.Result.(map[string]any)["preview"].(string)
	if len([]byte(preview)) > 512 || !strings.Contains(result.Artifact.RetrievalHint, "artifact_read") {
		t.Fatalf("model-visible result is not bounded/recoverable: result=%#v", result.Result)
	}
}

func TestExecutorReportsArtifactDegradationWithoutLeakingUnboundedResult(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "large", Parameters: ObjectSchema(nil, nil)},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("private", 1000), nil },
		Policy:     ExecutionPolicy{MaxResultBytes: 64},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := NewExecutor(catalog, ExecutorOptions{
		ArtifactStore: &artifactWriterStub{err: errors.New("disk full")}, ArtifactPreviewBytes: 32,
	}).Execute(context.Background(), ExecutionRequest{RunID: "run-1", CallID: "call-1", Tool: "large"})
	if result.Error != nil || result.ArtifactError == nil || result.ArtifactError.Code != ErrorArtifactUnavailable {
		t.Fatalf("artifact degradation was not typed separately: %#v", result)
	}
	preview := result.Result.(map[string]any)["preview"].(string)
	if len([]byte(preview)) > 32 || result.Result.(map[string]any)["content_complete"] != false {
		t.Fatalf("degraded preview violated the hard boundary: %#v", result.Result)
	}
}

func TestExecutorAppliesAggregateBatchResultBudget(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "batch_reader", Parameters: ObjectSchema(nil, nil), Concurrency: ConcurrencyPolicy{Mode: ConcurrencyReadOnly}},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("x", 3500), nil },
		Policy:     ExecutionPolicy{MaxResultBytes: 10_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := &artifactWriterStub{}
	requests := make([]ExecutionRequest, 10)
	for index := range requests {
		requests[index] = ExecutionRequest{RunID: "run-1", CallID: fmt.Sprintf("call-%d", index+1), Tool: "batch_reader"}
	}
	results := NewExecutor(catalog, ExecutorOptions{ArtifactStore: writer, MaxBatchResultBytes: 6_000}).ExecuteBatch(context.Background(), requests)
	if len(results) != 10 || results[0].Artifact != nil || results[1].Artifact == nil {
		t.Fatalf("batch result budget was not applied in source order: %#v", results)
	}
	firstVisible, _ := json.Marshal(results[0].Result)
	visibleBytes := len(firstVisible)
	for _, result := range results[1:] {
		visibleBytes += len([]byte(result.Result.(map[string]any)["preview"].(string)))
	}
	if visibleBytes > 6_000 {
		t.Fatalf("aggregate model-visible content = %d, want <= 6000", visibleBytes)
	}
}

func TestExecutorArtifactGovernanceFailurePaths(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "large", Parameters: ObjectSchema(nil, nil)},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("private", 100), nil },
		Policy:     ExecutionPolicy{MaxResultBytes: 16},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		options ExecutorOptions
		request ExecutionRequest
	}{
		{
			name: "redaction failure",
			options: ExecutorOptions{ArtifactStore: &artifactWriterStub{}, RedactArtifactJSON: func([]byte) ([]byte, int, error) {
				return nil, 0, errors.New("redaction unavailable")
			}},
			request: ExecutionRequest{RunID: "run-1", CallID: "call-1", Tool: "large"},
		},
		{
			name:    "artifact size limit",
			options: ExecutorOptions{ArtifactStore: &artifactWriterStub{}, MaxArtifactBytes: 32},
			request: ExecutionRequest{RunID: "run-1", CallID: "call-1", Tool: "large"},
		},
		{
			name:    "store unavailable",
			options: ExecutorOptions{},
			request: ExecutionRequest{RunID: "run-1", CallID: "call-1", Tool: "large"},
		},
		{
			name:    "missing execution identity",
			options: ExecutorOptions{ArtifactStore: &artifactWriterStub{}},
			request: ExecutionRequest{Tool: "large"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := NewExecutor(catalog, test.options).Execute(context.Background(), test.request)
			if result.Error != nil || result.Artifact != nil || result.ArtifactError == nil || result.ArtifactError.Code != ErrorArtifactUnavailable {
				t.Fatalf("artifact degradation = %#v", result)
			}
			visible, ok := result.Result.(map[string]any)
			if !ok || visible["content_complete"] != false || visible["artifact_unavailable"] != true {
				t.Fatalf("degraded model result = %#v", result.Result)
			}
		})
	}
}

func TestArtifactPreviewHelpersRespectExhaustedBudget(t *testing.T) {
	if boundArtifactPreview(nil, 1) != 0 {
		t.Fatal("nil result consumed preview budget")
	}
	nonArtifact := ExecutionResult{Result: "plain"}
	if boundArtifactPreview(&nonArtifact, 1) != 0 {
		t.Fatal("plain result consumed artifact preview budget")
	}
	artifactResult := ExecutionResult{Result: map[string]any{"preview": "bounded"}}
	if used := boundArtifactPreview(&artifactResult, 0); used != 0 || artifactResult.Result.(map[string]any)["preview"] != "" {
		t.Fatalf("exhausted preview budget result=%#v used=%d", artifactResult.Result, used)
	}

	placeholder := artifactPreview(nil, 12, nil, executionError(ErrorArtifactUnavailable, "missing", nil))
	preview, _ := placeholder["preview"].(string)
	if len([]byte(preview)) > 12 || placeholder["artifact_error_code"] != string(ErrorArtifactUnavailable) {
		t.Fatalf("bounded placeholder = %#v", placeholder)
	}
	empty := artifactPreview([]byte("content"), 0, nil, nil)
	if empty["preview"] != "" {
		t.Fatalf("zero preview limit leaked content: %#v", empty)
	}
}

type artifactWriterStub struct {
	artifact domain.ToolArtifact
	content  []byte
	err      error
}

func (s *artifactWriterStub) CreateToolArtifact(artifact domain.ToolArtifact, content []byte) (domain.ToolArtifact, error) {
	if s.err != nil {
		return domain.ToolArtifact{}, s.err
	}
	s.artifact = artifact
	s.content = append([]byte(nil), content...)
	return artifact, nil
}
