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
