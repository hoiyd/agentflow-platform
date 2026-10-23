package app

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

func TestWebSourcesRequireMatchingDurableRunAndConversation(t *testing.T) {
	storage := fixturestore.New()
	conversation, err := storage.CreateConversation("Web evidence")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "web_search", "tool_call_id": "search-1", "result": map[string]any{"results": []map[string]any{{"title": "Public article", "url": "https://example.com/article"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	resolve := runWebSourceResolver(storage)
	for _, scope := range []event.Scope{{}, {RunID: run.ID}, {RunID: run.ID, ConversationID: "other-conversation"}, {RunID: "absent", ConversationID: conversation.ID}} {
		if _, err := resolve(event.WithScope(t.Context(), scope), "W1"); err == nil {
			t.Fatalf("accepted mismatched scope: %#v", scope)
		}
	}
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: conversation.ID})
	source, err := resolve(ctx, "W1")
	if err != nil || source.RunID != run.ID || source.SourceID != "W1" {
		t.Fatalf("source=%#v error=%v", source, err)
	}
	if _, err := resolve(ctx, "W2"); err == nil {
		t.Fatal("invented source was accepted")
	}
	other, err := storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(event.WithScope(t.Context(), event.Scope{RunID: other.ID, ConversationID: conversation.ID}), "W1"); err == nil {
		t.Fatal("another Run inherited a source")
	}
}
