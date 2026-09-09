package agent

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
)

type sessionHistoryErrorStore struct {
	*fixturestore.Store
	messageErr error
}

func (s sessionHistoryErrorStore) ListMessages(string) ([]domain.Message, error) {
	return nil, s.messageErr
}

func TestRetrieveSessionHistoryRestoresCompactedMessageAndExcludesActiveHistory(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("History retrieval")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	oldMessage, err := fixtureStore.AddMessage(conversation.ID, "user", "The exact deployment ID is release-2026-08.")
	if err != nil {
		t.Fatalf("add old message: %v", err)
	}
	recentMessage, err := fixtureStore.AddMessage(conversation.ID, "assistant", "The recent active message also says release-2026-08.")
	if err != nil {
		t.Fatalf("add recent message: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = ChatModeSingle
	snapshot.AutonomousLimits = nil
	snapshot.ContextAssembly = contextassembly.DefaultConfig()
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	previousRun, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create previous run: %v", err)
	}
	previousEvent, err := fixtureStore.CreateRunEvent(domain.RunEvent{
		RunID: previousRun.ID, Type: domain.EventToolFailed,
		Payload: map[string]any{"error": "release-2026-08 failed on port 8123"},
	})
	if err != nil {
		t.Fatalf("create previous event: %v", err)
	}
	_, err = fixtureStore.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, Type: domain.EventToolFailed,
		Payload: map[string]any{"error": "release-2026-08 current-run noise"},
	})
	if err != nil {
		t.Fatalf("create current event: %v", err)
	}

	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	items := runtime.retrieveSessionHistory(context.Background(), run.ID, conversation.ID, "What was the exact release-2026-08 error?")

	wantMessage := "message:" + oldMessage.ID
	wantEvent := "event:" + previousEvent.ID
	for _, item := range items {
		if item.Reference == "message:"+recentMessage.ID {
			t.Fatalf("active history was reintroduced: %#v", items)
		}
		if item.RunID == run.ID {
			t.Fatalf("current run event was reintroduced: %#v", items)
		}
		if item.Reference == wantMessage {
			wantMessage = ""
		}
		if item.Reference == wantEvent && strings.Contains(item.Content, "8123") {
			wantEvent = ""
		}
	}
	if wantMessage != "" || wantEvent != "" {
		t.Fatalf("missing restored sources message=%q event=%q items=%#v", wantMessage, wantEvent, items)
	}
}

func TestRetrieveSessionHistorySkipsMissingRunAndEmptyQuery(t *testing.T) {
	fixtureStore := fixturestore.New()

	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	if items := runtime.retrieveSessionHistory(context.Background(), "missing", "conv", "release-42"); items != nil {
		t.Fatalf("missing run returned history: %#v", items)
	}

	conversation, _ := fixtureStore.CreateConversation("Empty history query")
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = contextassembly.DefaultConfig()
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if items := runtime.retrieveSessionHistory(context.Background(), run.ID, conversation.ID, "the and what please"); items != nil {
		t.Fatalf("noise-only query returned history: %#v", items)
	}
	events, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil || len(events) != 0 {
		t.Fatalf("noise-only query should not publish events: events=%#v err=%v", events, err)
	}
}

func TestRetrieveSessionHistoryPublishesSearchFailure(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("Failed history search")
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = contextassembly.DefaultConfig()
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	want := errors.New("message history unavailable")
	runtime := NewRuntime(RuntimeOptions{
		Store:       sessionHistoryErrorStore{Store: fixtureStore, messageErr: want},
		ModelClient: newLocalFallbackOpenAIClientForTest(),
	})
	if items := runtime.retrieveSessionHistory(context.Background(), run.ID, conversation.ID, "recover release-42"); items != nil {
		t.Fatalf("failed search returned history: %#v", items)
	}
	events, err := fixtureStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 || events[0].Type != domain.EventHistorySearchStarted || events[1].Type != domain.EventHistorySearchFailed {
		t.Fatalf("unexpected search lifecycle: %#v", events)
	}
	if events[1].Payload["error"] != want.Error() {
		t.Fatalf("failure payload lost cause: %#v", events[1].Payload)
	}
}
