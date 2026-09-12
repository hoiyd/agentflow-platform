package httpapi

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
)

func TestResumeRecoverableRunThroughAPIStreamsAndCompletes(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Recoverable API resume")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := fixtureStore.AddMessage(conversation.ID, "user", "Write a concise recovery demo."); err != nil {
		t.Fatalf("add message: %v", err)
	}
	run, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
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
	if _, err := fixtureStore.UpdateRunStatus(run.ID, domain.RunFailedRecoverable, "heartbeat expired"); err != nil {
		t.Fatalf("mark recoverable: %v", err)
	}
	beforeResume, ok, err := fixtureStore.GetRunReplay(run.ID)
	if err != nil || !ok || beforeResume.RecoverySummary == nil || beforeResume.RecoverySummary.Reason != domain.RecoveryRunRecoverable {
		t.Fatalf("expected recoverable pre-resume replay, got %#v ok=%v err=%v", beforeResume.RecoverySummary, ok, err)
	}
	if len(beforeResume.RecoverySummary.Actions) != 1 || beforeResume.RecoverySummary.Actions[0].Kind != "resume_run" || !beforeResume.RecoverySummary.Actions[0].Enabled {
		t.Fatalf("expected enabled resume action, got %#v", beforeResume.RecoverySummary.Actions)
	}
	writeBenchmarkReplayArtifact(t, "benchmark-recovery-before-replay.json", beforeResume)

	client := newLocalFallbackOpenAIClientForTest()
	runtime := agent.NewRuntime(agent.RuntimeOptions{
		Store: fixtureStore, ModelClient: client, RouterMode: agent.RouterModeQuery,
		Autonomous: agent.AutonomousLimits{
			MaxIterations:  2,
			MaxRuntime:     time.Minute,
			MaxOutputChars: 60000,
			MaxToolCalls:   20,
		},
	})
	handler := &Handler{
		store: fixtureStore, modelClient: client, agentRuntime: runtime,
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

	updated, ok, err := fixtureStore.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || updated.Status != domain.RunCompleted {
		t.Fatalf("expected completed run, got %#v ok=%v", updated, ok)
	}
	replay, ok, err := fixtureStore.GetRunReplay(run.ID)
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
	foundCheckpoint := false
	for _, event := range replay.RunEvents {
		if event.Type == domain.EventCheckpointCaptured {
			foundCheckpoint = true
			break
		}
	}
	if !foundCheckpoint {
		t.Fatal("expected a durable checkpoint in recovered replay")
	}
	writeBenchmarkReplayArtifact(t, "benchmark-recovery-replay.json", replay)
}

func writeBenchmarkReplayArtifact(t *testing.T, name string, replay domain.RunReplay) {
	t.Helper()
	dir := os.Getenv("EVALUATION_REPORT_DIR")
	if dir == "" {
		return
	}
	content, err := json.MarshalIndent(replay, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRecoverableCollaborationThroughAPIUsesDurableChildResult(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Recoverable collaboration API resume")
	if err != nil {
		t.Fatal(err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	runtime := agent.NewRuntime(agent.RuntimeOptions{
		Store: fixtureStore, ModelClient: client, RouterMode: agent.RouterModeQuery,
		ChildRuns: agent.ChildRunLimits{
			MaxConcurrent: 1, MaxPerParent: 1, Timeout: time.Minute, SummaryMaxChars: 100,
			RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 2, MaxTotalTokens: 4000},
		},
	})
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "planner",
		AgentID: "agent_planner", Status: domain.CollaborationStepCompleted,
		Input: "Implement the API change", Output: "Inspect, implement, and test.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "router",
		AgentID: "agent_planner", Status: domain.CollaborationStepCompleted,
		Input: "route", Output: "agent_planner",
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := fixtureStore.CreateCollaborationStep(domain.CollaborationStep{
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
	child, relation, err := fixtureStore.CreateChildRun(domain.ChildRunRequest{
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
	if _, err := fixtureStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationCompleted, Summary: "durable worker result",
		OutputRef: "run://" + child.ID + "/stages/worker", OutputHash: "hash", OutputBytes: 21,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "worker interrupted"); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{
		store: fixtureStore, modelClient: client, agentRuntime: runtime,
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
	updated, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil || !ok || updated.Status != domain.RunCompleted {
		t.Fatalf("resumed parent=%#v ok=%v err=%v", updated, ok, err)
	}
	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
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

func TestResumeRunRejectsStaleAndUnreconciledActions(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Resume conflicts")
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(completed.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fixtureStore}
	response := httptest.NewRecorder()
	handler.resumeRun(response, httptest.NewRequest(http.MethodPost, "/api/runs/"+completed.ID+"/resume", strings.NewReader(`{}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "current state") {
		t.Fatalf("stale resume: status=%d body=%s", response.Code, response.Body.String())
	}

	recoverable, err := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.UpdateRunStatus(recoverable.ID, domain.RunFailedRecoverable, "interrupted"); err != nil {
		t.Fatal(err)
	}
	effect, _, err := fixtureStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-resume", RunID: recoverable.ID, StageID: "stage-1", ToolCallID: "call-1",
		ToolName: "external_writer", RequestHash: "hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureStore.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "timeout"); err != nil {
		t.Fatal(err)
	}
	replay, ok, err := fixtureStore.GetRunReplay(recoverable.ID)
	if err != nil || !ok || replay.RecoverySummary == nil || replay.RecoverySummary.Reason != domain.RecoveryToolEffectUncertain {
		t.Fatalf("file recovery summary: summary=%#v ok=%v err=%v", replay.RecoverySummary, ok, err)
	}
	response = httptest.NewRecorder()
	handler.resumeRun(response, httptest.NewRequest(http.MethodPost, "/api/runs/"+recoverable.ID+"/resume", strings.NewReader(`{}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "unresolved tool effects") {
		t.Fatalf("unreconciled resume: status=%d body=%s", response.Code, response.Body.String())
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
