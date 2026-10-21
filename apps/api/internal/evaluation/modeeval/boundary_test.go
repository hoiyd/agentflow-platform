package modeeval

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

// Failure inventory: changing any non-mode contract must disable deltas;
// mode, candidate enumeration and loop-only limits are the declared bundle.
func TestCommonBoundaryRejectsNonModeDrift(t *testing.T) {
	base := domain.RuntimeSnapshot{Agent: domain.RuntimeAgentSnapshot{ID: "worker"}, Tools: []domain.RuntimeToolSnapshot{{Name: "file_read"}, {Name: "artifact_read", DefinitionRevision: "original"}}}
	want := commonBoundary(&base, Dataset{Material: "fixed", Task: "report"}, Options{ApprovalPolicy: "approve"})
	for _, name := range []string{"tool", "embedding", "skills", "schema", "scope", "constraint", "agent", "model", "budget"} {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.Tools = append([]domain.RuntimeToolSnapshot(nil), base.Tools...)
			switch name {
			case "tool":
				changed.Tools[1].DefinitionRevision = "changed"
			case "embedding":
				changed.Embedding.Model = "other"
			case "skills":
				changed.Skills = []domain.SkillSnapshot{{Name: "extra"}}
			case "schema":
				changed.ToolSchema = &domain.ToolSchemaConfig{Mode: "lazy"}
			case "scope":
				changed.KnowledgeScope = &domain.KnowledgeScope{}
			case "constraint":
				changed.ExecutionConstraint = "read_only"
			case "agent":
				changed.Agent.ID = "other"
			case "model":
				changed.ModelRouting.PinnedRouteID = "other"
			case "budget":
				changed.RunBudget = &domain.RuntimeRunBudget{MaxModelCalls: 1}
			}
			if got := commonBoundary(&changed, Dataset{Material: "fixed", Task: "report"}, Options{ApprovalPolicy: "approve"}); got == want {
				t.Fatal("non-mode contract drift was ignored")
			}
		})
	}
	mode := base
	mode.Mode = "autonomous"
	mode.CreatedAt = time.Now()
	mode.AutonomousLimits = &domain.RuntimeLimitsSnapshot{MaxIterations: 2}
	if commonBoundary(&mode, Dataset{Material: "fixed", Task: "report"}, Options{ApprovalPolicy: "approve"}) != want {
		t.Fatal("declared orchestration differences must not disable this dedicated experiment")
	}
}
