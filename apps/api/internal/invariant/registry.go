package invariant

import (
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

type Input struct {
	Replay        domain.RunReplay
	ModelRequests []domain.ModelRequestRecord
}

type Check interface {
	Name() string
	Evaluate(Input) []domain.RuntimeInvariantFailure
}

type CheckFunc struct {
	CheckName string
	Run       func(Input) []domain.RuntimeInvariantFailure
}

func (c CheckFunc) Name() string { return c.CheckName }

func (c CheckFunc) Evaluate(input Input) []domain.RuntimeInvariantFailure {
	if c.Run == nil {
		return nil
	}
	return c.Run(input)
}

type Registry struct {
	checks []Check
}

func NewRegistry(checks ...Check) *Registry {
	return &Registry{checks: append([]Check(nil), checks...)}
}

func (r *Registry) Evaluate(input Input) []domain.RuntimeInvariantFailure {
	if r == nil {
		return []domain.RuntimeInvariantFailure{}
	}
	failures := []domain.RuntimeInvariantFailure{}
	for _, check := range r.checks {
		if check != nil {
			failures = append(failures, check.Evaluate(input)...)
		}
	}
	sort.SliceStable(failures, func(i, j int) bool {
		if failures[i].Sequence != failures[j].Sequence {
			return failures[i].Sequence < failures[j].Sequence
		}
		if failures[i].Owner != failures[j].Owner {
			return failures[i].Owner < failures[j].Owner
		}
		return failures[i].Code < failures[j].Code
	})
	return failures
}
