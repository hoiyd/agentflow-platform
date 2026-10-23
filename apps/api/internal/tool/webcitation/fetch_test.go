package webcitation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFetchEvidenceKeepsSearchIdentityAndRequiresSelectedContent(t *testing.T) {
	search := webEvent(1, "search-event", "search-call", false, "https://example.com/article", "Article")
	page := map[string]any{"source_id": "W1", "title": "Article", "url": "https://example.com/article", "content": "Retention is 37 days.", "content_hash": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("Retention is 37 days."))), "trust_boundary": "untrusted_external_content", "content_complete": true, "status": "read"}
	fetch := domain.RunEvent{ID: "fetch-event", RunID: "run-1", Sequence: 2, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "web_fetch", "tool_call_id": "fetch-call", "result": page}}
	catalog := FromEvents([]domain.RunEvent{search, fetch})
	if source, ok := catalog.Lookup("W1"); !ok || source.ToolCallID != "search-call" {
		t.Fatalf("fetch must retain search provenance: %#v %v", source, ok)
	}
	resolved, invalid := catalog.Resolve("37 days [W1].", map[string]bool{"fetch-call": true})
	if len(resolved) != 1 || len(invalid) != 0 || resolved[0].ToolCallID != "fetch-call" {
		t.Fatalf("model-visible fetch not citable: %#v %v", resolved, invalid)
	}
	resolved, _ = catalog.Resolve("[W1]", map[string]bool{"search-call": true, "fetch-call": true})
	if len(resolved) != 1 || resolved[0].ToolCallID != "fetch-call" {
		t.Fatal("fetch evidence was hidden by an earlier search snippet")
	}
	resolved, invalid = catalog.Resolve("[W1]", map[string]bool{})
	if len(resolved) != 0 || len(invalid) != 1 {
		t.Fatal("excluded fetch became evidence")
	}
	for key, value := range map[string]any{"url": "https://other.example/", "source_id": "W9", "content_hash": "forged", "content": ""} {
		t.Run(key, func(t *testing.T) {
			bad := map[string]any{}
			for k, v := range page {
				bad[k] = v
			}
			bad[key] = value
			failed := fetch
			failed.Payload = map[string]any{"tool_name": "web_fetch", "tool_call_id": "fetch-call", "result": bad}
			resolved, _ := FromEvents([]domain.RunEvent{search, failed}).Resolve("[W1] [W9]", map[string]bool{"fetch-call": true})
			if len(resolved) != 0 {
				t.Fatal("forged fetch granted evidence")
			}
		})
	}

	encoded, _ := json.Marshal(page)
	artifact := domain.ToolArtifact{ID: "artifact-fetch", RunID: "run-1", ToolName: "web_fetch", ToolCallID: "fetch-call", StoredByteSize: len(encoded), ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))}
	fetch.Payload = map[string]any{"tool_name": "web_fetch", "tool_call_id": "fetch-call", "arguments": `{"source_id":"W1"}`, "truncated": true, "artifact": artifact.Reference()}
	read := domain.RunEvent{ID: "read-event", RunID: "run-1", Sequence: 3, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "artifact_read", "tool_call_id": "read-call", "result": domain.ToolArtifactRead{Artifact: artifact, Content: string(encoded), NextOffset: len(encoded), Complete: true}}}
	resolved, _ = FromEvents([]domain.RunEvent{search, fetch, read}).Resolve("[W1]", map[string]bool{"read-call": true})
	if len(resolved) != 1 || resolved[0].ToolEventID != "read-event" {
		t.Fatalf("complete delivered Artifact is not evidence: %#v", resolved)
	}
	read.Payload["truncated"] = true
	resolved, _ = FromEvents([]domain.RunEvent{search, fetch, read}).Resolve("[W1]", map[string]bool{"read-call": true, "fetch-call": true})
	if len(resolved) != 0 {
		t.Fatal("truncated Artifact or preview became evidence")
	}
}
