package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

func TestResumeRecoverableRunThroughAPIStreamsAndCompletes(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Recoverable API resume")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := fileStore.AddMessage(conversation.ID, "user", "Write a concise recovery demo."); err != nil {
		t.Fatalf("add message: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		Role:           "observe",
		AgentID:        "agent_planner",
		Status:         domain.CollaborationStepCompleted,
		Iteration:      1,
		Input:          "User task:\nWrite a concise recovery demo.\n\nCurrent state:\nNo prior autonomous work.",
		Output:         "The run needs a concise recovery demo.",
	}); err != nil {
		t.Fatalf("create observe step: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunFailedRecoverable, "heartbeat expired"); err != nil {
		t.Fatalf("mark recoverable: %v", err)
	}

	handler := NewHandlerWithRouterModeAndLimits(
		fileStore,
		openai.NewClientWithTimeout("", "", "test", time.Second),
		nil,
		nil,
		agent.RouterModeQuery,
		agent.AutonomousLimits{
			MaxIterations:  2,
			MaxRuntime:     time.Minute,
			MaxOutputChars: 60000,
			MaxToolCalls:   20,
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/resume", bytes.NewReader([]byte(`{"user_input":"Resume from test"}`)))
	recorder := httptest.NewRecorder()
	handler.resumeRun(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	events := parseSSEChunks(t, recorder.Body.String())
	if !hasChunk(events, "run", func(chunk domain.ChatChunk) bool {
		return chunk.Status == string(domain.RunRunning)
	}) {
		t.Fatalf("expected running run event, got %s", recorder.Body.String())
	}
	if !hasChunk(events, "collaboration_step", func(chunk domain.ChatChunk) bool {
		return chunk.Role == "recovery" && chunk.Status == string(domain.CollaborationStepCompleted)
	}) {
		t.Fatalf("expected completed recovery step event, got %s", recorder.Body.String())
	}
	if !hasChunk(events, "done", func(chunk domain.ChatChunk) bool {
		return chunk.Status == string(domain.RunCompleted)
	}) {
		t.Fatalf("expected completed done event, got %s", recorder.Body.String())
	}

	updated, ok, err := fileStore.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || updated.Status != domain.RunCompleted {
		t.Fatalf("expected completed run, got %#v ok=%v", updated, ok)
	}
	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	if len(replay.Messages) < 2 || replay.Messages[len(replay.Messages)-1].Role != "assistant" {
		t.Fatalf("expected persisted assistant message, got %#v", replay.Messages)
	}
	foundRecovery := false
	for _, step := range replay.Steps {
		if step.Role == "recovery" && step.Output == "Resume from test" {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("expected recovery step in replay, got %#v", replay.Steps)
	}
}

func TestDetachedRequestContextIgnoresCancellationAndKeepsValues(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request-id"), "req_test")
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/runs/run_test/resume", bytes.NewReader([]byte(`{}`))).WithContext(ctx)
	detached := detachedRequestContext(req)
	if err := detached.Err(); err != nil {
		t.Fatalf("expected detached context to ignore request cancellation, got %v", err)
	}
	if value := detached.Value(contextKey("request-id")); value != "req_test" {
		t.Fatalf("expected detached context to keep request values, got %#v", value)
	}
}

type sseChunk struct {
	Event string
	Chunk domain.ChatChunk
}

func parseSSEChunks(t *testing.T, body string) []sseChunk {
	t.Helper()
	events := []sseChunk{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	currentEvent := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			var chunk domain.ChatChunk
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
				t.Fatalf("decode SSE chunk: %v line=%q", err, line)
			}
			events = append(events, sseChunk{Event: currentEvent, Chunk: chunk})
			currentEvent = ""
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	return events
}

func hasChunk(events []sseChunk, event string, match func(domain.ChatChunk) bool) bool {
	for _, item := range events {
		if item.Event == event && match(item.Chunk) {
			return true
		}
	}
	return false
}
