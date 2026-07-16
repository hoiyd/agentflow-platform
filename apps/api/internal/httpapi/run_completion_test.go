package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

type recordingMemoryCurationQueue struct {
	jobs []memorypkg.CurationJob
}

func (q *recordingMemoryCurationQueue) Enqueue(job memorypkg.CurationJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}

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
	memoryCuration := &recordingMemoryCurationQueue{}
	handler := &Handler{store: fileStore, agentRuntime: runtime, memoryCuration: memoryCuration}
	response := httptest.NewRecorder()
	userMessage := domain.Message{
		ID: "msg_user", ConversationID: conversation.ID, Role: "user",
		Content: "Remember that AgentFlow uses typed events.",
	}

	ok := handler.completeStreamingRun(response, response, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID, Assistant: "Completed response.",
		UserMessage: &userMessage,
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
	if len(memoryCuration.jobs) != 1 || memoryCuration.jobs[0].Message.ID != userMessage.ID || memoryCuration.jobs[0].Message.Role != "user" {
		t.Fatalf("completion should curate only the user message: %#v", memoryCuration.jobs)
	}
}
