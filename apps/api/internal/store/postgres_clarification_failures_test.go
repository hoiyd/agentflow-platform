package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestPostgresClarificationFailureAndRaceBoundaries(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	makeRun := func() (domain.Run, domain.Clarification) {
		t.Helper()
		conv, err := s.CreateConversation("clarification boundary")
		if err != nil {
			t.Fatal(err)
		}
		a, _, _ := s.GetDefaultAgent()
		run, err := s.CreateRunWithContract(a.ID, conv.ID, testRuntimeSnapshot(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateRunEvent(domain.RunEvent{Type: domain.EventTurnStarted, RunID: run.ID, ConversationID: conv.ID, TurnID: "real-turn", Payload: map[string]any{"agent_id": a.ID}}); err != nil {
			t.Fatal(err)
		}
		return run, domain.Clarification{RunID: run.ID, ConversationID: conv.ID, TurnID: "real-turn", Questions: []domain.ClarificationQuestion{{ID: "goal", Question: "What outcome?"}}}
	}
	for _, kind := range []string{"missing_identity", "wrong_turn", "wrong_stage", "invalid_json", "too_large", "invalid_question", "canceled", "wrong_conversation", "wrong_call"} {
		t.Run(kind, func(t *testing.T) {
			run, q := makeRun()
			data := json.RawMessage(`{"version":1}`)
			switch kind {
			case "missing_identity":
				q.RunID = ""
			case "wrong_turn":
				q.TurnID = "invented"
			case "wrong_stage":
				q.StageID = "invented"
			case "invalid_json":
				data = []byte("bad")
			case "too_large":
				data = []byte(`{"data":"` + strings.Repeat("x", clarification.MaxCheckpointBytes) + `"}`)
			case "invalid_question":
				q.Questions = nil
			case "canceled":
				s.UpdateRunStatus(run.ID, domain.RunCanceled, "")
			case "wrong_conversation":
				q.ConversationID = "missing"
			case "wrong_call":
				q.ToolCallID = "invented"
			}
			if _, err := s.SuspendClarification(t.Context(), q, data); err == nil {
				t.Fatal("invalid pause accepted")
			}
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_clarifications WHERE run_id=$1`, run.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial question: %d %v", count, err)
			}
		})
	}
	for _, kind := range []string{"expired", "withdraw", "wrong_version", "unknown_question", "archived", "rollback", "cancel_race"} {
		t.Run(kind, func(t *testing.T) {
			run, q := makeRun()
			saved, err := s.SuspendClarification(t.Context(), q, json.RawMessage(`{"version":1}`))
			if err != nil {
				t.Fatal(err)
			}
			answer := domain.ClarificationAnswer{Version: saved.Version, IdempotencyKey: "answer", Answers: map[string]string{"goal": "A report"}}
			switch kind {
			case "expired":
				s.db.Exec(`UPDATE run_clarifications SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, saved.ID)
				items, err := s.ListClarifications(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID)
				if err != nil || items[0].Status != "expired" {
					t.Fatalf("expiry: %+v %v", items, err)
				}
			case "withdraw":
				if _, err = s.WithdrawClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, 0); err == nil {
					t.Fatal("stale withdrawal")
				}
				if _, err = s.WithdrawClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, saved.Version); err != nil {
					t.Fatal(err)
				}
				duplicate, err := s.WithdrawClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, saved.Version)
				if err != nil || len(duplicate.Events) != 0 {
					t.Fatalf("duplicate withdrawal: %+v %v", duplicate, err)
				}
			case "wrong_version":
				answer.Version++
			case "unknown_question":
				saved.ID = "missing"
			case "archived":
				if _, err = s.db.Exec(`UPDATE workspaces SET status='archived' WHERE id=$1`, run.WorkspaceID); err != nil {
					t.Fatal(err)
				}
				defer s.db.Exec(`UPDATE workspaces SET status='active' WHERE id=$1`, run.WorkspaceID)
			case "rollback":
				if _, err = s.db.Exec(`CREATE OR REPLACE FUNCTION fail_clarification_answer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.role='user' THEN RAISE EXCEPTION 'controlled answer storage failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_clarification_answer BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION fail_clarification_answer()`); err != nil {
					t.Fatal(err)
				}
				defer s.db.Exec(`DROP TRIGGER fail_clarification_answer ON messages; DROP FUNCTION fail_clarification_answer()`)
			case "cancel_race":
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				var answerErr, cancelErr error
				go func() {
					defer wg.Done()
					<-start
					_, answerErr = s.AnswerClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, answer)
				}()
				go func() { defer wg.Done(); <-start; _, cancelErr = s.UpdateRunStatus(run.ID, domain.RunCanceled, "stop") }()
				close(start)
				wg.Wait()
				if cancelErr != nil {
					t.Fatal(cancelErr)
				}
				if answerErr != nil && !errors.Is(answerErr, clarification.ErrConflict) {
					t.Fatal(answerErr)
				}
				if _, err = s.ClaimClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID); err == nil {
					t.Fatal("canceled run resumed")
				}
				items, err := s.ListClarifications(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID)
				if err != nil || items[0].Status != "canceled" {
					t.Fatalf("cancel state: %+v %v", items, err)
				}
				return
			}
			if _, err = s.AnswerClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, answer); err == nil {
				t.Fatal("invalid answer accepted")
			}
			if kind == "rollback" {
				items, err := s.ListClarifications(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID)
				if err != nil || items[0].Status != "pending" {
					t.Fatalf("non-atomic answer: %+v %v", items, err)
				}
			}
		})
	}
	// A saved answer outlives a connection failure. Competing claims admit one.
	run, q := makeRun()
	saved, err := s.SuspendClarification(t.Context(), q, json.RawMessage(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ClarificationAnswer{Version: 1, IdempotencyKey: "once", Answers: map[string]string{"goal": "Report"}}
	if _, err = s.AnswerClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, input); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	out := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := s.ClaimClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID)
			out <- err
		}()
	}
	close(start)
	wins := 0
	for range 2 {
		err := <-out
		if err == nil {
			wins++
		} else if !errors.Is(err, clarification.ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("claim winners %d", wins)
	}
	if _, err = s.ClaimClarification(t.Context(), "stranger", run.WorkspaceID, run.ID, saved.ID); err == nil {
		t.Fatal("cross-owner claim")
	}
	// Bound the durable per-Run record count independently of model-call budget.
	for n := 0; n < 16; n++ {
		s.UpdateRunStatus(run.ID, domain.RunRunning, "")
		s.db.Exec(`UPDATE run_clarifications SET status='applied' WHERE run_id=$1`, run.ID)
		_, err = s.SuspendClarification(t.Context(), q, json.RawMessage(fmt.Sprintf(`{"sequence":%d}`, n)))
		if n == 15 && err == nil {
			t.Fatal("unbounded question count")
		}
		if n < 15 && err != nil {
			t.Fatal(err)
		}
	}
}
