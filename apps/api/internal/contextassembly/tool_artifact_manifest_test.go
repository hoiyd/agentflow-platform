package contextassembly

import (
	"context"
	"testing"

	eventpkg "agentflow-platform/apps/api/internal/event"
)

func TestManifestLinksToolPreviewToDurableArtifact(t *testing.T) {
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: "run-1", TurnID: "turn-1"})
	ctx = WithSession(ctx, Session{Config: DefaultConfig()})
	pack, err := Assemble(ctx, Request{Model: "test", Messages: []Message{
		{Source: SourceSystem, ReferenceID: "system", Role: "system", Content: "system"},
		{Source: SourceToolResult, ReferenceID: "call-1", Role: "tool", Content: `{"tool":"future_tool","result":{"preview":"bounded","artifact":{"id":"tool_artifact_123"}},"artifact":{"id":"tool_artifact_123"}}`},
		{Source: SourceCurrentInput, ReferenceID: "current", Role: "user", Content: "continue"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range pack.Manifest.Entries {
		if entry.ReferenceID == "call-1" {
			if entry.Transformation != "tool_result_artifact_preview" || len(entry.ArtifactIDs) != 1 || entry.ArtifactIDs[0] != "tool_artifact_123" {
				t.Fatalf("manifest artifact relationship missing: %#v", entry)
			}
			return
		}
	}
	t.Fatal("tool result manifest entry not found")
}
