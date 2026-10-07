package store

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// The child is killed without Close/terminal flushing: only a committed prefix
// may survive. These databases are disposable, never an operator database.
func TestPostgresPartialOutputSurvivesWorkerKill(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			url := pgfixture.DatabaseURL(t)
			storage, err := NewPostgresStore(url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = storage.Close() })
			conversation, err := storage.CreateConversation("partial-output crash fixture")
			if err != nil {
				t.Fatal(err)
			}
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPartialOutputCrashWorker$")
			command.Env = append(os.Environ(), "AGENTFLOW_OUTPUT_CRASH_URL="+url, "AGENTFLOW_OUTPUT_CRASH_RUN="+run.ID,
				"AGENTFLOW_OUTPUT_CRASH_CONVERSATION="+conversation.ID, fmt.Sprintf("AGENTFLOW_OUTPUT_COMMITTED=%t", committed))
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
			if !scanner.Scan() || scanner.Text() != "checkpoint-ready" {
				t.Fatalf("worker did not reach checkpoint: %s", scanner.Text())
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
				t.Fatalf("replay: %v found=%v", err, found)
			}
			outputs := replay.Projection.PartialOutputs
			if committed {
				if len(outputs) != 1 || outputs[0].Text != "committed prefix" || outputs[0].Status != "interrupted" {
					t.Fatalf("restored uncommitted or complete output: %#v", outputs)
				}
			} else if len(outputs) != 0 {
				t.Fatalf("invented a durable prefix: %#v", outputs)
			}
			if _, found, err := storage.ForWorkspace(domain.NewWorkspaceScope("999999999")).GetRunReplay(run.ID); err == nil && found {
				t.Fatal("foreign workspace read partial output")
			}
			if err = storage.DeleteConversation(conversation.ID); err != nil {
				t.Fatal(err)
			}
			if events, err := storage.ListRunEvents(run.ID); err != nil || len(events) != 0 {
				t.Fatalf("orphaned checkpoints: %d %v", len(events), err)
			}
		})
	}
}

func TestPartialOutputCrashWorker(t *testing.T) {
	url := os.Getenv("AGENTFLOW_OUTPUT_CRASH_URL")
	if url == "" {
		t.Skip("subprocess fixture only")
	}
	storage, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	recorder := event.NewOutputRecorder(event.StoreSink{Store: storage}, nil)
	meta := domain.RunEvent{Type: domain.EventModelDelta, RunID: os.Getenv("AGENTFLOW_OUTPUT_CRASH_RUN"),
		ConversationID: os.Getenv("AGENTFLOW_OUTPUT_CRASH_CONVERSATION"), TurnID: "crash-turn"}
	if os.Getenv("AGENTFLOW_OUTPUT_COMMITTED") == "true" {
		meta.Payload = map[string]any{"delta": "committed prefix ", "model_call_id": "crash-call", "attempt": 1}
		if err := recorder.Publish(t.Context(), meta); err != nil {
			t.Fatal(err)
		}
	}
	meta.Payload = map[string]any{"delta": "uncommitted", "model_call_id": "crash-call", "attempt": 1}
	if err := recorder.Publish(t.Context(), meta); err != nil {
		t.Fatal(err)
	}
	fmt.Println("checkpoint-ready")
	<-t.Context().Done()
}

func TestPostgresPartialOutputBoundsManyToolRounds(t *testing.T) {
	storage, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	conversation, err := storage.CreateConversation("bounded tool-round display")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := event.NewOutputRecorder(event.StoreSink{Store: storage}, nil)
	defer recorder.Close()
	for round := 1; round <= 40; round++ {
		meta := domain.RunEvent{Type: domain.EventModelReasoningDelta, RunID: run.ID, ConversationID: conversation.ID, TurnID: "bounded-turn",
			Payload: map[string]any{"model_call_id": fmt.Sprintf("round-%d", round), "attempt": 1, "status": "receiving", "text": "Safe explanation "}}
		if err := recorder.Publish(t.Context(), meta); err != nil {
			t.Fatal(err)
		}
		meta.Type = domain.EventModelDelta
		meta.Payload = map[string]any{"model_call_id": fmt.Sprintf("round-%d", round), "attempt": 1, "delta": fmt.Sprintf("answer %d ", round)}
		if err := recorder.Publish(t.Context(), meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Flush("interrupted"); err != nil {
		t.Fatal(err)
	}
	replay, found, err := storage.GetRunReplay(run.ID)
	if err != nil || !found {
		t.Fatalf("replay: %v", err)
	}
	outputs := replay.Projection.PartialOutputs
	if len(outputs) != domain.MaxPartialOutputEntries {
		t.Fatalf("unbounded retained slots: %d", len(outputs))
	}
	for _, output := range outputs {
		if output.Channel == "answer" && (output.Text != "answer 40" || output.Round != 40 || output.Revision < 2) {
			t.Fatalf("answer slot lost identity/revision during eviction: %#v", output)
		}
	}
	if len(replay.RunEvents) > 40*3 {
		t.Fatalf("unbounded checkpoint writes: %d", len(replay.RunEvents))
	}
}
