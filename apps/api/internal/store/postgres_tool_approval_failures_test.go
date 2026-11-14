package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Store-only failure inventory: forged identity cannot pause; owner/version/
// expiry/epoch drift cannot consume; failed event writes roll back metadata and
// Run status together; unavailable storage rejects every operation. Runtime and
// real Binding behavior are covered by TestProductionDurableToolApproval in app.
func TestPostgresToolApprovalAtomicFailures(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	makePause := func() (domain.Run, domain.ToolApproval) {
		t.Helper()
		conv, err := s.CreateConversation("approval boundaries")
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
		for _, kind := range []domain.RunEventType{domain.EventTurnStarted, domain.EventToolStarted, domain.EventToolFailed} {
			if _, err = s.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: conv.ID, TurnID: "real-turn", Type: kind, Payload: map[string]any{"tool_name": "file_patch", "tool_call_id": "real-call", "error_code": "approval_required", "arguments_hash": "args", "definition_revision": "definition"}}); err != nil {
				t.Fatal(err)
			}
		}
		return run, domain.ToolApproval{RunID: run.ID, ConversationID: conv.ID, TurnID: "real-turn", ToolCallID: "real-call", Tool: "file_patch", Arguments: json.RawMessage(`{"root":"output","path":"report.md","old_text":"old","new_text":"new"}`), ArgumentsHash: "args", DefinitionRevision: "definition", PolicyHash: "policy", SnapshotHash: "snapshot"}
	}
	for _, scenario := range []string{"missing_identity", "wrong_tool", "wrong_turn", "wrong_call", "wrong_stage", "invalid_checkpoint", "oversized", "pause_rollback", "decision_rollback", "claim_rollback", "consume_rollback", "wrong_owner", "stale_claim", "binding_drift", "epoch_drift", "late_cancel", "late_expiry", "invalid_decision", "unknown_record", "corrupt_record"} {
		t.Run(scenario, func(t *testing.T) {
			run, record := makePause()
			checkpoint := json.RawMessage(`{"version":1}`)
			switch scenario {
			case "missing_identity":
				record.TurnID = ""
			case "wrong_turn":
				record.TurnID = "invented"
			case "wrong_tool":
				record.Tool = "calculator"
			case "wrong_call":
				record.ToolCallID = "invented"
			case "wrong_stage":
				record.StageID = "invented"
			case "invalid_checkpoint":
				checkpoint = []byte(`broken`)
			case "oversized":
				checkpoint = []byte(`{"data":"` + strings.Repeat("x", approval.MaxCheckpointBytes) + `"}`)
			}
			fault := func(kind domain.RunEventType) {
				t.Helper()
				_, err := s.db.Exec(`CREATE OR REPLACE FUNCTION approval_test_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='` + string(kind) + `' THEN RAISE EXCEPTION 'approval event persistence unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER approval_test_fault BEFORE INSERT ON run_events FOR EACH ROW EXECUTE FUNCTION approval_test_fault()`)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					s.db.Exec(`DROP TRIGGER IF EXISTS approval_test_fault ON run_events; DROP FUNCTION IF EXISTS approval_test_fault()`)
				})
			}
			if scenario == "pause_rollback" {
				fault(domain.EventToolApprovalRequested)
			}
			saved, err := s.SuspendToolApproval(t.Context(), record, checkpoint)
			if strings.HasPrefix(scenario, "wrong_") && scenario != "wrong_owner" || scenario == "missing_identity" || scenario == "invalid_checkpoint" || scenario == "oversized" || scenario == "pause_rollback" {
				if err == nil {
					t.Fatal("invalid pause accepted")
				}
				var count int
				if err = s.db.QueryRow(`SELECT count(*) FROM tool_approvals WHERE run_id=$1`, run.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial pause %d %v", count, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decision := domain.ToolApprovalDecision{Version: 1, Decision: "approve", IdempotencyKey: "once"}
			if scenario == "wrong_owner" {
				if _, err = s.DecideToolApproval(t.Context(), "stranger", run.WorkspaceID, run.ID, saved.ID, decision); err == nil {
					t.Fatal("cross-owner decision")
				}
				items, err := s.ListToolApprovals(t.Context(), "stranger", run.WorkspaceID, run.ID)
				if err != nil || len(items) != 0 {
					t.Fatal(items, err)
				}
				return
			}
			if scenario == "invalid_decision" {
				for _, input := range []domain.ToolApprovalDecision{{Version: 0, Decision: "approve", IdempotencyKey: "key"}, {Version: 1, Decision: "execute", IdempotencyKey: "key"}, {Version: 1, Decision: "approve"}} {
					if _, err = s.DecideToolApproval(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, input); err == nil {
						t.Fatal("invalid decision accepted")
					}
				}
				return
			}
			if scenario == "unknown_record" {
				if _, err = s.DecideToolApproval(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, "missing", decision); !IsNotFound(err) {
					t.Fatal(err)
				}
				return
			}
			if scenario == "corrupt_record" {
				if _, err = s.db.Exec(`UPDATE tool_approvals SET record=record||'{"expires_at":"not-a-time"}'::jsonb WHERE id=$1`, saved.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ListToolApprovals(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID); err == nil {
					t.Fatal("corrupt metadata accepted")
				}
				return
			}
			if scenario == "decision_rollback" {
				fault(domain.EventToolApprovalDecided)
			}
			_, err = s.DecideToolApproval(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID, decision)
			if scenario == "decision_rollback" {
				if err == nil {
					t.Fatal("failed decision persisted")
				}
				items, err := s.ListToolApprovals(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID)
				if err != nil || items[0].Status != "pending" {
					t.Fatal(items, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "claim_rollback" {
				fault(domain.EventRunResumed)
			}
			claimed, err := s.ClaimToolApproval(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, saved.ID)
			if scenario == "claim_rollback" {
				if err == nil {
					t.Fatal("failed claim persisted")
				}
				current, _, err := s.GetRun(run.ID)
				if err != nil || current.Status != domain.RunWaitingForUser {
					t.Fatal(current, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			claim, hash := claimed.Claim, approval.BindingHash(claimed.ToolApproval)
			switch scenario {
			case "consume_rollback":
				fault(domain.EventToolApprovalConsumed)
			case "stale_claim":
				claim = "stale"
			case "binding_drift":
				hash = "unreviewed"
			case "epoch_drift":
				_, err = s.db.Exec(`UPDATE runs SET execution_started_at=execution_started_at+INTERVAL '1 second' WHERE id=$1`, run.ID)
			case "late_cancel":
				_, err = s.UpdateRunStatus(run.ID, domain.RunCanceled, "")
			case "late_expiry":
				data, _ := json.Marshal(time.Now().Add(-time.Hour))
				_, err = s.db.Exec(`UPDATE tool_approvals SET record=jsonb_set(record,'{expires_at}',$2) WHERE id=$1`, saved.ID, data)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ConsumeToolApproval(t.Context(), identity.SuperUserID, run.WorkspaceID, saved.ID, claim, hash); err == nil {
				t.Fatal("invalid execution claim consumed")
			}
			var status string
			if err = s.db.QueryRow(`SELECT record->>'status' FROM tool_approvals WHERE id=$1`, saved.ID).Scan(&status); err != nil || status != "claimed" {
				t.Fatal(status, err)
			}
		})
	}
}

func TestPostgresToolApprovalStorageUnavailable(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	record := domain.ToolApproval{RunID: "run", ConversationID: "conversation", TurnID: "turn", ToolCallID: "call", Tool: "file_patch", ArgumentsHash: "args", DefinitionRevision: "definition", PolicyHash: "policy", SnapshotHash: "snapshot"}
	if _, err = s.SuspendToolApproval(t.Context(), record, json.RawMessage(`{"version":1}`)); err == nil {
		t.Fatal("unavailable suspension")
	}
	if _, err = s.ListToolApprovals(t.Context(), "owner", "workspace", "run"); err == nil {
		t.Fatal("unavailable read")
	}
	if _, err = s.DecideToolApproval(t.Context(), "owner", "workspace", "run", "id", domain.ToolApprovalDecision{Version: 1, Decision: "approve", IdempotencyKey: "key"}); err == nil {
		t.Fatal("unavailable decision")
	}
	if _, err = s.ClaimToolApproval(t.Context(), "owner", "workspace", "run", "id"); err == nil {
		t.Fatal("unavailable claim")
	}
	if _, err = s.ConsumeToolApproval(t.Context(), "owner", "workspace", "id", "claim", "hash"); err == nil {
		t.Fatal("unavailable consumption")
	}
}

func TestToolApprovalSchema(t *testing.T) {
	for _, fragment := range []string{"CREATE TABLE IF NOT EXISTS tool_approvals", "record jsonb NOT NULL", "checkpoint jsonb NOT NULL", "claim text NOT NULL", "execution_started_at timestamptz", "tool_approvals_run_idx"} {
		if !strings.Contains(strings.Join(postgresMigrations, "\n"), fragment) {
			t.Fatalf("missing approval schema step: %s", fragment)
		}
	}
}
