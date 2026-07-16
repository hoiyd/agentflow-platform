package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestCompleteStreamingRunPersistsMessageAndCompletesRun(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("completion test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	runtime := agent.NewRuntime(agent.RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(),
	})
	handler := &Handler{store: fileStore, agentRuntime: runtime}
	response := httptest.NewRecorder()

	ok := handler.completeStreamingRun(response, response, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID, Assistant: "Completed response.",
	})
	if !ok {
		t.Fatalf("complete streaming run failed: %s", response.Body.String())
	}

	completed, found, err := fileStore.GetRun(run.ID)
	if err != nil || !found || completed.Status != domain.RunCompleted {
		t.Fatalf("unexpected completed run: run=%#v found=%v err=%v", completed, found, err)
	}
	messages, err := fileStore.ListMessages(conversation.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "assistant" || messages[0].Content != "Completed response." {
		t.Fatalf("unexpected persisted messages: %#v", messages)
	}
}
