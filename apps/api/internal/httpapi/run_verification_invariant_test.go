package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
)

// Use REF02's actual candidate binding, failed completion and HTTP Reverify.
// Only copies of the resulting events are changed for diagnostic negative cases.
func TestReverifyCommittedLifecycleInvariant(t *testing.T) {
	for _, firstEvidenceFails := range []bool{false, true} {
		name := "failed_recoverable"
		if firstEvidenceFails {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			f := newVerificationSubjectFixture(t, "text")
			started, err := f.storage.TransitionRun(domain.RunTransition{RunID: f.run.ID, From: domain.RunRunning, To: domain.RunRunning, EventType: domain.EventRunStarted})
			if err != nil || !started.Applied {
				t.Fatalf("initial accepted lifecycle: %v", err)
			}
			f.complete(t, firstEvidenceFails)
			prior, _, err := f.storage.GetRun(f.run.ID)
			want := domain.RunFailedRecoverable
			if firstEvidenceFails {
				want = domain.RunFailed
			}
			if err != nil || prior.Status != want {
				t.Fatalf("expected actual %s candidate, got %s: %v", want, prior.Status, err)
			}
			f.advance(t)
			response := f.verify(f.storage, true)
			if response.Code != http.StatusOK {
				t.Fatalf("real Reverify failed: %d %s", response.Code, response.Body.String())
			}
			run, _, err := f.storage.GetRun(f.run.ID)
			if err != nil || run.Status != domain.RunCompleted || run.VerificationStatus != domain.VerificationPassed {
				t.Fatalf("candidate not verified: %+v %v", run, err)
			}
			items, err := f.storage.ListRunEvents(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.Type == domain.EventRunResumed {
					t.Fatal("Reverify must not invent Resume")
				}
			}
			if failures := event.CheckRuntimeInvariants(run, items); len(failures) != 0 {
				t.Errorf("actual verified lifecycle rejected: %+v", failures)
			}
			for _, negative := range []string{"source alone", "missing request", "unverified result", "wrong verifier"} {
				t.Run(negative, func(t *testing.T) {
					current := run
					if negative == "unverified result" {
						current.VerificationStatus = domain.VerificationBlocked
					}
					copied := make([]domain.RunEvent, 0, len(items))
					for _, item := range items {
						if negative == "source alone" && item.Type == domain.EventVerificationPassed || negative == "missing request" && item.Type == domain.EventVerificationRequested {
							continue
						}
						if negative == "wrong verifier" && item.Type == domain.EventVerificationPassed {
							payload := map[string]any{}
							for k, v := range item.Payload {
								payload[k] = v
							}
							payload["verifier_id"] = "unknown-verifier"
							item.Payload = payload
						}
						item.Sequence = int64(len(copied) + 1)
						copied = append(copied, item)
					}
					if failures := event.CheckRuntimeInvariants(current, copied); len(failures) == 0 {
						t.Fatal("completion metadata accepted without verified boundary proof")
					}
				})
			}
			data, _ := json.Marshal(map[string]any{"schema": "ref01-reverify-invariant-v1", "prior_status": prior.Status, "run": run, "events": items, "limitations": []string{"real Postgres candidate/verification/Reverify; local HTTP verifier only"}})
			t.Log("REF01_REVERIFY_EVIDENCE " + string(data))
		})
	}
}

func TestRejectedReverifyCommittedLifecycleInvariant(t *testing.T) {
	for _, scenario := range []string{"exhausted failed", "exhausted waiting", "blocked waiting", "blocked then passed"} {
		t.Run(scenario, func(t *testing.T) {
			mode := "text"
			if scenario == "exhausted failed" {
				mode = "rejected_plan"
			}
			f := newVerificationSubjectFixture(t, mode)
			if scenario == "exhausted waiting" {
				f.output = "Rejected candidate"
			}
			started, err := f.storage.TransitionRun(domain.RunTransition{RunID: f.run.ID, From: domain.RunRunning, To: domain.RunRunning, EventType: domain.EventRunStarted})
			if err != nil || !started.Applied {
				t.Fatalf("initial lifecycle: %v", err)
			}
			f.complete(t, false)
			prior, _, err := f.storage.GetRun(f.run.ID)
			if err != nil || prior.Status != domain.RunFailedRecoverable {
				t.Fatalf("missing actual initial rejection: %s %v", prior.Status, err)
			}
			attempts := 2
			if scenario == "exhausted failed" {
				attempts = 1
			}
			for attempt := 0; attempt < attempts; attempt++ {
				healthy := scenario != "blocked waiting" && (scenario != "blocked then passed" || attempt == attempts-1)
				response := f.verify(f.storage, healthy)
				if response.Code != http.StatusOK {
					t.Fatalf("actual rejected Reverify: %d %s", response.Code, response.Body.String())
				}
				run, _, err := f.storage.GetRun(f.run.ID)
				if err != nil {
					t.Fatal(err)
				}
				items, err := f.storage.ListRunEvents(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					if item.Type == domain.EventRunResumed {
						t.Fatal("Reverify invented Resume")
					}
				}
				if failures := event.CheckRuntimeInvariants(run, items); len(failures) != 0 {
					t.Errorf("actual rejection/verification lifecycle rejected: %+v", failures)
				}
				if attempt == attempts-1 {
					want := domain.RunWaitingForUser
					if scenario == "exhausted failed" {
						want = domain.RunFailed
					}
					if scenario == "blocked then passed" {
						want = domain.RunCompleted
					}
					if run.Status != want {
						t.Fatalf("wrong actual outcome: %s want %s", run.Status, want)
					}
					if scenario != "blocked then passed" {
						for _, negative := range []string{"source alone", "missing request", "wrong aggregate"} {
							current := run
							if negative == "wrong aggregate" {
								current.VerificationStatus = domain.VerificationPassed
							}
							copied := make([]domain.RunEvent, 0, len(items))
							for _, item := range items {
								if negative == "source alone" && (item.Type == domain.EventVerificationFailed || item.Type == domain.EventVerificationBlocked) || negative == "missing request" && item.Type == domain.EventVerificationRequested {
									continue
								}
								item.Sequence = int64(len(copied) + 1)
								copied = append(copied, item)
							}
							if failures := event.CheckRuntimeInvariants(current, copied); len(failures) == 0 {
								t.Errorf("%s accepted unproven rejection boundary", negative)
							}
						}
					}
				}
				data, _ := json.Marshal(map[string]any{"schema": "ref01-rejected-reverify-invariant-v1", "scenario": scenario, "attempt": attempt + 2, "run": run, "events": items, "limitations": []string{"real REF02 candidate/HTTP Reverify/Postgres; deterministic local verifier"}})
				t.Log("REF01_REJECTED_REVERIFY_EVIDENCE " + string(data))
			}
		})
	}
}
