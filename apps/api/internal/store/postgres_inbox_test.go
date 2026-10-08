package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestInboxSchema(t *testing.T) {
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS conversation_inputs", "conversation_inputs_key_idx", "conversation_inputs_conversation_idx", "conversation_inputs_run_idx"} {
		if !strings.Contains(strings.Join(postgresMigrations, "\n"), required) {
			t.Fatalf("missing durable inbox migration: %s", required)
		}
	}
}

func TestPostgresInboxFailureAndRecoveryBoundaries(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	conv, err := s.CreateConversation("inbox failures")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, _ := s.GetDefaultAgent()
	snapshot := testRuntimeSnapshot()
	run, err := s.CreateRunWithContract(agent.ID, conv.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	base := domain.RunInputRequest{Kind: "steer", RunID: run.ID, Content: "Constraint", IdempotencyKey: "invalid"}
	for _, input := range []domain.RunInputRequest{
		{Kind: "unknown", RunID: run.ID, Content: "test", IdempotencyKey: "x"},
		{Kind: "steer", RunID: run.ID, Content: " ", IdempotencyKey: "x"},
		{Kind: "steer", RunID: run.ID, Content: strings.Repeat("x", 8193), IdempotencyKey: "x"},
		{Kind: "steer", RunID: run.ID, Content: "test", IdempotencyKey: "x", Mode: "bad"},
	} {
		if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, ErrInputInvalid) {
			t.Fatalf("invalid accepted: %+v %v", input, err)
		}
	}
	if _, err = s.EnqueueRunInput(ctx, "stranger", conv.WorkspaceID, conv.ID, base); err == nil {
		t.Fatal("cross-owner enqueue")
	}
	// Withdrawal and consumption race through the same durable lock. Exactly one
	// wins, and an applied record always has exactly one matching user message.
	for n := 0; n < 8; n++ {
		input := base
		input.IdempotencyKey = fmt.Sprintf("race-%d", n)
		receipt, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var withdrawErr, consumeErr error
		go func() {
			defer wg.Done()
			<-start
			_, withdrawErr = s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, receipt.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, consumeErr = s.ConsumeSteering(ctx, run.ID, "stage-one", "turn-one")
		}()
		close(start)
		wg.Wait()
		if consumeErr != nil || withdrawErr != nil && !errors.Is(withdrawErr, ErrInputConflict) {
			t.Fatalf("race: %v %v", consumeErr, withdrawErr)
		}
		items, _ := s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
		messages, _ := s.ListMessages(conv.ID)
		for _, item := range items {
			if item.ID != receipt.ID {
				continue
			}
			count := 0
			for _, message := range messages {
				if message.ID == item.ID {
					count++
				}
			}
			if item.Status == "applied" && count != 1 || item.Status == "withdrawn" && count != 0 {
				t.Fatalf("receipt/message mismatch: %+v %d", item, count)
			}
		}
	}
	input := base
	input.IdempotencyKey = "expiry"
	expired, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE conversation_inputs SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == expired.ID && item.Status != "expired" {
			t.Fatalf("not expired: %+v", item)
		}
	}
	// Pending limits reject without consuming or overwriting any accepted input.
	for n := 0; n < 16; n++ {
		input := base
		input.Kind = "follow_up"
		input.IdempotencyKey = fmt.Sprintf("queue-%d", n)
		if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	input = base
	input.IdempotencyKey = "overflow"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, ErrInputCapacity) {
		t.Fatalf("queue overflow: %v", err)
	}
	// Revoked membership/lifecycle prevents consumption, even though the Run was
	// admitted earlier under valid authorization.
	if _, err = s.db.Exec(`DELETE FROM auth_memberships WHERE user_id=$1 AND workspace_id=$2`, identity.SuperUserID, conv.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeSteering(ctx, run.ID, "", "turn-two"); err == nil {
		t.Fatal("revoked membership consumed")
	}
	if _, err = s.db.Exec(`INSERT INTO auth_memberships(user_id,workspace_id) VALUES($1,$2)`, identity.SuperUserID, conv.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE workspaces SET status='archived' WHERE id=$1`, conv.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeSteering(ctx, run.ID, "", "turn-two"); err == nil {
		t.Fatal("archived workspace consumed")
	}
}

func TestPostgresFollowupCreationIsAtomic(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	s, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	conv, err := s.CreateConversation("followup atomicity")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, _ := s.GetDefaultAgent()
	snapshot := testRuntimeSnapshot()
	run, err := s.CreateRunWithContract(agent.ID, conv.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.RunInputRequest{Kind: "follow_up", RunID: run.ID, Content: "A separate task", IdempotencyKey: "next"}
	receipt, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateInputRun(ctx, receipt.ID, identity.SuperUserID, false, agent.ID, conv.ID, snapshot, nil); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("active predecessor: %v", err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunFailed, "budget exhausted"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateInputRun(ctx, receipt.ID, identity.SuperUserID, false, agent.ID, conv.ID, snapshot, nil); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("failed predecessor auto-drained: %v", err)
	}
	// Simulate a failure after the receipt's message INSERT, but before Run INSERT.
	if _, err = s.db.Exec(`CREATE FUNCTION reject_followup_run() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture write failure'; END $$;
	CREATE TRIGGER reject_followup BEFORE INSERT ON runs FOR EACH ROW EXECUTE FUNCTION reject_followup_run()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateInputRun(ctx, receipt.ID, identity.SuperUserID, true, agent.ID, conv.ID, snapshot, nil); err == nil {
		t.Fatal("expected fixture write failure")
	}
	messages, _ := s.ListMessages(conv.ID)
	if len(messages) != 0 {
		t.Fatalf("partial commit: %+v", messages)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_followup ON runs`); err != nil {
		t.Fatal(err)
	}
	snapshot.Agent.SystemPrompt = "New frozen configuration"
	created, err := s.CreateInputRun(ctx, receipt.ID, identity.SuperUserID, true, agent.ID, conv.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == run.ID || created.RuntimeSnapshot.Agent.SystemPrompt != snapshot.Agent.SystemPrompt || created.Status != domain.RunRunning {
		t.Fatalf("fresh run: %+v", created)
	}
	if _, err = s.CreateInputRun(ctx, receipt.ID, identity.SuperUserID, true, agent.ID, conv.ID, snapshot, nil); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("duplicate run: %v", err)
	}
	items, _ := s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if len(items) != 1 || items[0].AppliedRunID != created.ID {
		t.Fatalf("lost receipt: %+v", items)
	}
	messages, _ = s.ListMessages(conv.ID)
	if len(messages) != 1 || messages[0].ID != receipt.ID {
		t.Fatalf("duplicate messages: %+v", messages)
	}
	// Reopening the Store retains the committed input/Run association.
	reopened, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	items, err = reopened.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if err != nil || len(items) != 1 || items[0].AppliedRunID != created.ID {
		t.Fatalf("restart receipt: %+v %v", items, err)
	}
}

func TestPostgresInboxProtocol(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	conv, err := s.CreateConversation("Inbox protocol")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, _ := s.GetDefaultAgent()
	run, err := s.CreateRunWithContract(agent.ID, conv.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	input := domain.RunInputRequest{Kind: "steer", RunID: run.ID, Content: "Keep the answer short", IdempotencyKey: "one"}
	receipt, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
	if err != nil || duplicate.ID != receipt.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	input.Content = "Different"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("changed payload: %v", err)
	}
	if _, err = s.ListRunInputs(ctx, "other", conv.WorkspaceID, conv.ID); err == nil {
		t.Fatal("cross-owner access")
	}
	applied, err := s.ConsumeSteering(ctx, run.ID, "", "turn_one")
	if err != nil || len(applied) != 1 || applied[0].Status != "applied" {
		t.Fatalf("consume: %+v %v", applied, err)
	}
	if _, err = s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, receipt.ID); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("withdraw applied: %v", err)
	}
	again, err := s.ConsumeSteering(ctx, run.ID, "", "turn_two")
	if err != nil || len(again) != 1 || again[0].TurnID != "turn_one" {
		t.Fatalf("recovery: %+v %v", again, err)
	}
	messages, _ := s.ListMessages(conv.ID)
	if len(messages) != 1 || messages[0].ID != receipt.ID {
		t.Fatalf("messages: %+v", messages)
	}
	input.IdempotencyKey = "two"
	pending, err := s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, pending.ID)
	if err != nil || withdrawn.Status != "withdrawn" {
		t.Fatalf("withdraw: %+v %v", withdrawn, err)
	}
	items, err := s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("roundtrip: %+v %v", items, err)
	}
}

func TestPostgresInboxByteBoundsRetentionAndRollback(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	conv, err := s.CreateConversation("inbox byte bounds")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.GetDefaultAgent()
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract(agent.ID, conv.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	input := domain.RunInputRequest{Kind: "steer", RunID: run.ID, Content: strings.Repeat("x", 8192)}
	for index := 0; index < 8; index++ {
		input.IdempotencyKey = fmt.Sprintf("bytes-%d", index)
		if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	input.IdempotencyKey, input.Content = "over-bytes", "x"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, ErrInputCapacity) {
		t.Fatalf("byte limit: %v", err)
	}
	// Fail the receipt UPDATE after the message INSERT: neither half may survive.
	if _, err = s.db.Exec(`CREATE FUNCTION reject_inbox_application() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture receipt failure'; END $$;
 CREATE TRIGGER reject_inbox_application BEFORE UPDATE ON conversation_inputs FOR EACH ROW EXECUTE FUNCTION reject_inbox_application()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeSteering(ctx, run.ID, "", "turn-fixture"); err == nil {
		t.Fatal("expected receipt write failure")
	}
	messages, err := s.ListMessages(conv.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("partial message write: %+v %v", messages, err)
	}
	items, err := s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if err != nil || len(items) != 8 {
		t.Fatalf("lost queued receipts: %+v %v", items, err)
	}
	for _, item := range items {
		if item.Status != "queued" {
			t.Fatalf("partial application: %+v", item)
		}
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_inbox_application ON conversation_inputs`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeSteering(ctx, run.ID, "", "turn-fixture"); err != nil {
		t.Fatal(err)
	}
	// Application frees pending capacity but not the total steering bound per Run.
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, ErrInputCapacity) {
		t.Fatalf("applied steering limit: %v", err)
	}
	input.Kind, input.IdempotencyKey = "follow_up", "separate-task"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE conversation_inputs SET created_at=NOW()-INTERVAL '8 days' WHERE conversation_id=$1`, conv.ID); err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "retain-active"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if err != nil || len(items) != 10 {
		t.Fatalf("active recovery inputs purged: %d %v", len(items), err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "cleanup-terminal"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListRunInputs(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("terminal retention cleanup: %d %v", len(items), err)
	}
	input.Kind, input.IdempotencyKey = "steer", "terminal-steer"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("terminal steering: %v", err)
	}
	if _, err = s.ConsumeSteering(ctx, run.ID, "", "after-terminal"); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("terminal consumption: %v", err)
	}
	other, err := s.CreateConversation("another conversation")
	if err != nil {
		t.Fatal(err)
	}
	input.Kind = "follow_up"
	if _, err = s.EnqueueRunInput(ctx, identity.SuperUserID, other.WorkspaceID, other.ID, input); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("foreign Run accepted: %v", err)
	}
	if _, err = s.WithdrawRunInput(ctx, identity.SuperUserID, other.WorkspaceID, other.ID, items[0].ID); !errors.Is(err, ErrInputConflict) {
		t.Fatalf("foreign withdrawal: %v", err)
	}
	withdrawn, err := s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.WithdrawRunInput(ctx, identity.SuperUserID, conv.WorkspaceID, conv.ID, withdrawn.ID); err != nil || again.Status != "withdrawn" {
		t.Fatalf("withdraw retry: %+v %v", again, err)
	}
	// A canceled transaction must never be misreported as an idempotency conflict.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.EnqueueRunInput(canceled, identity.SuperUserID, conv.WorkspaceID, conv.ID, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enqueue: %v", err)
	}
	if _, err = s.ListRunInputs(canceled, identity.SuperUserID, conv.WorkspaceID, conv.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if _, err = s.WithdrawRunInput(canceled, identity.SuperUserID, conv.WorkspaceID, conv.ID, items[1].ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled withdrawal: %v", err)
	}
	if _, err = s.ConsumeSteering(canceled, run.ID, "", "canceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled consumption: %v", err)
	}
}
