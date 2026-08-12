package agent

import (
	"context"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestRetrieveSessionHistoryRestoresCompactedMessageAndExcludesActiveHistory(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("History retrieval")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	oldMessage, err := fileStore.AddMessage(conversation.ID, "user", "The exact deployment ID is release-2026-08.")
	if err != nil {
		t.Fatalf("add old message: %v", err)
	}
	recentMessage, err := fileStore.AddMessage(conversation.ID, "assistant", "The recent active message also says release-2026-08.")
	if err != nil {
		t.Fatalf("add recent message: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Mode = ChatModeSingle
	snapshot.AutonomousLimits = nil
	snapshot.ContextAssembly = contextassembly.DefaultConfig()
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	previousRun, err := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
	if err != nil {
		t.Fatalf("create previous run: %v", err)
	}
	previousEvent, err := fileStore.CreateRunEvent(domain.RunEvent{
		RunID: previousRun.ID, Type: domain.EventToolFailed,
		Payload: map[string]any{"error": "release-2026-08 failed on port 8123"},
	})
	if err != nil {
		t.Fatalf("create previous event: %v", err)
	}
	_, err = fileStore.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, Type: domain.EventToolFailed,
		Payload: map[string]any{"error": "release-2026-08 current-run noise"},
	})
	if err != nil {
		t.Fatalf("create current event: %v", err)
	}

	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
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

func TestRetrieveSessionHistoryKeepsLegacySnapshotDisabled(t *testing.T) {
	config := contextassembly.NormalizeSnapshotConfig(contextassembly.DefaultConfig(), domain.UnifiedExecutionSnapshotVersion)
	if config.HistoryRetrievalEnabled {
		t.Fatalf("legacy snapshot unexpectedly enabled history retrieval: %#v", config)
	}
}
