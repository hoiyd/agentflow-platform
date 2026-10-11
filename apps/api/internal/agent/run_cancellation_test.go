package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"context"
	"errors"
	"testing"
	"time"
)

type blockingPreparedClient struct {
	provider.Client
	started chan struct{}
}

func (c *blockingPreparedClient) WithRuntimeIdentity(identity provider.RuntimeIdentity) provider.Client {
	return &blockingPreparedClient{Client: c.Client.WithRuntimeIdentity(identity), started: c.started}
}

func (c *blockingPreparedClient) CompletePreparedText(ctx context.Context, _ provider.PreparedText) (provider.TextCompletion, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return provider.TextCompletion{}, ctx.Err()
}

func (c *blockingPreparedClient) StreamAnswer(ctx context.Context, _ provider.PreparedChat, _ provider.ChatStreamKind, _ provider.ChatTrace, _ chan<- provider.StreamEvent) (bool, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return false, ctx.Err()
}

func TestCancelRunStopsActiveWorkerStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel isolated worker")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery}, client)
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	createCompletedStage(t, runtime, prepared.Run, "planner", "agent_planner", "task", "approved plan")
	if _, err := fixtureStore.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatal(err)
	}

	events, errs := runtime.ContinueCollaboration(context.Background(), prepared.Run.ID, "approved plan", domain.AgentRoutingRequirements{})
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker stage did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
		canceled, failErr := runtime.FailRun(prepared.Run.ID, err)
		if failErr != nil || canceled.Status != domain.RunCanceled {
			t.Fatalf("finalize canceled run: run=%#v err=%v", canceled, failErr)
		}
		steps, listErr := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if _, found := latestCompletedCollaborationStep(steps, "reviewer"); found {
			t.Fatal("reviewer ran after the worker was canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("active worker ignored cancellation")
	}
}

func TestCancelRunStopsActiveSingleTurn(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel single turn")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore}, client)
	prepared, err := runtime.PrepareChatRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.StreamChat(context.Background(), prepared, nil, "wait")
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("single turn did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("single turn ignored cancellation")
	}
}

func TestCancelRunStopsActivePlannerStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel planner")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore}, client)
	prepared, err := runtime.PrepareCollaborationRunWithContract(context.Background(), "agent_planner", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunCollaboration(context.Background(), prepared, "wait")
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("planner stage did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("planner stage ignored cancellation")
	}
}

func TestAutonomousRunCanBeCanceledBeforeLoop(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Cancel test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery, Autonomous: AutonomousLimits{
		MaxIterations: 2, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
	}}, newLocalFallbackOpenAIClientForTest())
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	events, errs := runtime.RunAutonomous(context.Background(), prepared, "Long task")
	seenCanceled := false
	for event := range events {
		if event.Type == domain.EventRunCanceled && event.Payload["status"] == domain.RunCanceled {
			seenCanceled = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("expected clean cancel, got %v", err)
	}
	if !seenCanceled {
		t.Fatal("expected canceled run event")
	}
	run, ok, err := fixtureStore.GetRun(prepared.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || run.Status != domain.RunCanceled {
		t.Fatalf("expected canceled run, got %#v", run)
	}
}

func TestAutonomousCancelClosesActiveStage(t *testing.T) {
	fixtureStore := fixturestore.New()
	conversation, err := fixtureStore.CreateConversation("cancel active autonomous stage")
	if err != nil {
		t.Fatal(err)
	}
	client := &blockingPreparedClient{Client: newLocalFallbackOpenAIClientForTest(), started: make(chan struct{}, 1)}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery}, client)
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := runtime.RunAutonomous(context.Background(), prepared, "wait")
	done := make(chan error, 1)
	go func() {
		for range events {
		}
		done <- <-errs
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("autonomous model request did not start")
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("autonomous cancellation returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("autonomous cancellation did not finish")
	}
	runEvents, err := fixtureStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var startedStage string
	var failedStage bool
	for _, event := range runEvents {
		switch event.Type {
		case domain.EventStageStarted:
			startedStage = event.StageID
		case domain.EventStageFailed:
			failedStage = event.StageID == startedStage
		}
	}
	if startedStage == "" || !failedStage {
		t.Fatalf("canceled autonomous stage has no terminal event: %#v", runEvents)
	}
}
