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
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
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
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
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

	client := newLocalFallbackOpenAIClientForTest()
	runtime := agent.NewRuntime(agent.RuntimeOptions{
		Store: fileStore, ModelClient: client, RouterMode: agent.RouterModeQuery,
		Autonomous: agent.AutonomousLimits{
			MaxIterations:  2,
			MaxRuntime:     time.Minute,
			MaxOutputChars: 60000,
			MaxToolCalls:   20,
		},
	})
	handler := &Handler{
		store: fileStore, modelClient: client, agentRuntime: runtime,
		runController: concurrency.NewRunController(concurrency.RunOptions{
			MaxConcurrent: 1, QueueSize: 1, WaitTimeout: time.Second,
		}),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/resume", bytes.NewReader([]byte(`{"user_input":"Resume from test"}`)))
	recorder := httptest.NewRecorder()
	handler.resumeRun(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	events := parseSSEChunks(t, recorder.Body.String())
	if !hasRunEvent(events, domain.EventRunStarted, func(event domain.RunEvent) bool {
		return event.Payload["status"] == string(domain.RunRunning)
	}) {
		t.Fatalf("expected running run event, got %s", recorder.Body.String())
	}
	if !hasRunEvent(events, domain.EventStageCompleted, func(event domain.RunEvent) bool {
		return event.Payload["name"] == "recovery" && event.Payload["status"] == string(domain.CollaborationStepCompleted)
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

func TestResumeRecoverableCollaborationThroughAPIUsesDurableChildResult(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("Recoverable collaboration API resume")
	if err != nil {
		t.Fatal(err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	runtime := agent.NewRuntime(agent.RuntimeOptions{
		Store: fileStore, ModelClient: client, RouterMode: agent.RouterModeQuery,
		ChildRuns: agent.ChildRunLimits{
			MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000},
		},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "planner",
		AgentID: "agent_planner", Status: domain.CollaborationStepCompleted,
		Input: "Implement the API change", Output: "Inspect, implement, and test.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "router",
		AgentID: "agent_planner", Status: domain.CollaborationStepCompleted,
		Input: "route", Output: "agent_planner",
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "worker",
		AgentID: "agent_planner", Status: domain.CollaborationStepFailed,
		Input: "delegated work", Error: "worker interrupted",
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := prepared.Run.RuntimeSnapshot.Agent
	for _, candidate := range prepared.Run.RuntimeSnapshot.CandidateAgents {
		if candidate.ID == "agent_planner" {
			selected = candidate
			break
		}
	}
	delegationID := "delegation-api-resume"
	childSnapshot := domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: agent.ChatModeSingle,
		Agent: selected, Model: prepared.Run.RuntimeSnapshot.Model,
		ContextAssembly:    prepared.Run.RuntimeSnapshot.ContextAssembly,
		RunBudget:          &domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000},
		ToolSecurityPolicy: prepared.Run.RuntimeSnapshot.ToolSecurityPolicy,
		ToolProgressGuard:  prepared.Run.RuntimeSnapshot.ToolProgressGuard,
		Delegation: &domain.RuntimeDelegation{
			DelegationID: delegationID, ParentRunID: prepared.Run.ID, ParentTurnID: "turn-api-resume",
			ParentStageID: worker.ID, Depth: 1, IsolatedContext: true,
			TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 100,
		},
		CreatedAt: time.Now().UTC(),
	}
	child, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: delegationID, ParentRunID: prepared.Run.ID, ParentTurnID: "turn-api-resume",
			ParentStageID: worker.ID, AgentID: selected.ID, Depth: 1,
			Task: worker.Input, TimeoutMS: time.Minute.Milliseconds(),
		},
		RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationCompleted, Summary: "durable worker result",
		OutputRef: "run://" + child.ID + "/stages/worker", OutputHash: "hash", OutputBytes: 21,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "worker interrupted"); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{
		store: fileStore, modelClient: client, agentRuntime: runtime,
		runController: concurrency.NewRunController(concurrency.RunOptions{
			MaxConcurrent: 1, QueueSize: 1, WaitTimeout: time.Second,
		}),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+prepared.Run.ID+"/resume", bytes.NewReader([]byte(`{}`)))
	recorder := httptest.NewRecorder()
	handler.resumeRun(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "event: done") {
		t.Fatalf("resume response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil || !ok || updated.Status != domain.RunCompleted {
		t.Fatalf("resumed parent=%#v ok=%v err=%v", updated, ok, err)
	}
	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasStepRole(steps, "reviewer") || !hasStepRole(steps, "finalizer") || planner.ID == "" {
		t.Fatalf("resumed collaboration steps=%#v", steps)
	}
}

func hasStepRole(steps []domain.CollaborationStep, role string) bool {
	for _, step := range steps {
		if step.Role == role {
			return true
		}
	}
	return false
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

func TestResumeFailurePolicyKeepsReplayOnlyRunRecoverable(t *testing.T) {
	status, failRun := resumeFailurePolicy(agent.ErrRuntimeSnapshotResumeUnsupported)
	if status != http.StatusConflict || failRun {
		t.Fatalf("replay-only resume policy: status=%d fail_run=%t", status, failRun)
	}
}

type sseChunk struct {
	Event    string
	Chunk    domain.ChatChunk
	RunEvent domain.RunEvent
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
			data := []byte(strings.TrimPrefix(line, "data: "))
			if strings.Contains(currentEvent, ".") {
				var event domain.RunEvent
				if err := json.Unmarshal(data, &event); err != nil {
					t.Fatalf("decode run event: %v", err)
				}
				events = append(events, sseChunk{Event: currentEvent, RunEvent: event})
				currentEvent = ""
				continue
			}
			var chunk domain.ChatChunk
			if err := json.Unmarshal(data, &chunk); err != nil {
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

func hasRunEvent(events []sseChunk, eventType domain.RunEventType, match func(domain.RunEvent) bool) bool {
	for _, item := range events {
		if item.RunEvent.Type == eventType && match(item.RunEvent) {
			return true
		}
	}
	return false
}

func hasChunk(events []sseChunk, event string, match func(domain.ChatChunk) bool) bool {
	for _, item := range events {
		if item.Event == event && match(item.Chunk) {
			return true
		}
	}
	return false
}
