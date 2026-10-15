package planning

import (
	"agentflow-platform/apps/api/internal/domain"
	"testing"
	"time"
)

func TestRecordedPlanIntegrityExpiryAndSourceDrift(t *testing.T) {
	run := domain.Run{ID: "run-plan", Status: domain.RunCompleted, CreatedAt: time.Now().UTC(), RuntimeSnapshot: &domain.RuntimeSnapshot{ExecutionConstraint: domain.ExecutionPlanOnly}}
	source := domain.PlanSource{Root: "project", Path: "report.md", RootRevision: "root-v1", FileID: "file-v1", ContentHash: "hash-v1"}
	plan := domain.RunPlan{Version: Version, RunID: run.ID, Input: "Inspect", Proposal: "Change report; verify contents", Sources: []domain.PlanSource{source}, ExpiresAt: time.Now().Add(time.Hour)}
	plan.Hash = Hash(plan)
	if err := Validate(run, &plan, plan.Hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"hash", "proposal", "expired", "canceled", "no-sources", "version", "run"} {
		t.Run(scenario, func(t *testing.T) {
			copy, changed := plan, run
			want := plan.Hash
			switch scenario {
			case "hash":
				want = "other"
			case "proposal":
				copy.Proposal = "Different action"
			case "expired":
				copy.ExpiresAt = time.Now().Add(-time.Second)
				copy.Hash = Hash(copy)
				want = copy.Hash
			case "canceled":
				changed.Status = domain.RunCanceled
			case "no-sources":
				copy.Sources = nil
				copy.Hash = Hash(copy)
				want = copy.Hash
			case "version":
				copy.Version = "future"
				copy.Hash = Hash(copy)
				want = copy.Hash
			case "run":
				copy.RunID = "other"
				copy.Hash = Hash(copy)
				want = copy.Hash
			}
			if Validate(changed, &copy, want, time.Now()) == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}
