package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
)

// SIGKILL bypasses cancellation, Handler return and terminal flushing. Reopen
// must recover only committed progress, never claim the killed Tool completed.
func TestPostgresToolProgressSurvivesWorkerKill(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	storage, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	conversation, err := storage.CreateConversation("Tool progress crash fixture")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestToolProgressCrashWorker$")
	command.Env = append(os.Environ(), "AGENTFLOW_PROGRESS_CRASH_URL="+url, "AGENTFLOW_PROGRESS_CRASH_RUN="+run.ID, "AGENTFLOW_PROGRESS_CRASH_CONVERSATION="+conversation.ID)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "progress-committed" {
		t.Fatalf("worker checkpoint missing: %s", scanner.Text())
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if err = storage.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err = NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storage.UpdateRunStatus(run.ID, domain.RunFailedRecoverable, "worker crashed"); err != nil {
		t.Fatal(err)
	}
	replay, found, err := storage.GetRunReplay(run.ID)
	if err != nil || !found {
		t.Fatalf("replay found=%v err=%v", found, err)
	}
	if len(replay.Projection.ToolProgress) != 1 || replay.Projection.ToolProgress[0].Status != "interrupted" || replay.Projection.ToolProgress[0].Phase != "executing" {
		t.Fatalf("incorrect crash view: %#v", replay.Projection.ToolProgress)
	}
	if _, found, err := storage.ForWorkspace(domain.NewWorkspaceScope("999999999")).GetRunReplay(run.ID); err == nil && found {
		t.Fatal("foreign Workspace read progress")
	}
	if err = storage.DeleteConversation(conversation.ID); err != nil {
		t.Fatal(err)
	}
	if events, err := storage.ListRunEvents(run.ID); err != nil || len(events) != 0 {
		t.Fatalf("orphaned progress: %d %v", len(events), err)
	}
}

type crashProgressStore struct{ *PostgresStore }

func (s crashProgressStore) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	created, err := s.PostgresStore.CreateRunEvent(item)
	if err == nil && item.Type == domain.EventToolProgress {
		fmt.Println("progress-committed")
	}
	return created, err
}

func TestToolProgressCrashWorker(t *testing.T) {
	url := os.Getenv("AGENTFLOW_PROGRESS_CRASH_URL")
	if url == "" {
		t.Skip("subprocess fixture only")
	}
	storage, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	runID, conversationID := os.Getenv("AGENTFLOW_PROGRESS_CRASH_RUN"), os.Getenv("AGENTFLOW_PROGRESS_CRASH_CONVERSATION")
	ctx := event.WithScope(t.Context(), event.Scope{RunID: runID, ConversationID: conversationID, TurnID: "crash-turn"})
	catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "reader", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		tool.ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "executing", Message: "committed stage"})
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	tool.NewExecutor(catalog, tool.ExecutorOptions{Tracer: event.NewToolExecutionTracer(event.NewRecorder(crashProgressStore{storage}), runID, "")}).Execute(ctx, tool.ExecutionRequest{RunID: runID, TurnID: "crash-turn", Tool: "reader", CallID: "crash-call"})
}
