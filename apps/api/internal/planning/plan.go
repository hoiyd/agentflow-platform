// Package planning owns plan evidence and its consumption rules, not execution
// or authorization. Plans never confer permissions.
package planning

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

const Version = "read-only-plan-v1"

var ErrAlreadyStarted = errors.New("plan execution already started")

func Hash(plan domain.RunPlan) string {
	plan.Hash = ""
	data, _ := json.Marshal(plan)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

// ExecutionRunID gives repeated approval one durable identity, including after
// restart. A failed/canceled execution must be resumed or replanned, not cloned.
func ExecutionRunID(planRunID string) string {
	return fmt.Sprintf("run_plan_%x", sha256.Sum256([]byte(planRunID)))
}

func Validate(run domain.Run, plan *domain.RunPlan, expectedHash string, now time.Time) error {
	if run.Status != domain.RunCompleted || run.RuntimeSnapshot == nil || run.RuntimeSnapshot.ExecutionConstraint != domain.ExecutionPlanOnly {
		return errors.New("only a completed read-only planning Run can start execution")
	}
	if plan == nil || plan.Version != Version || plan.RunID != run.ID || plan.Hash == "" || plan.Hash != expectedHash || plan.Hash != Hash(*plan) {
		return errors.New("plan is missing, changed or has an unsupported version")
	}
	if !now.Before(plan.ExpiresAt) {
		return errors.New("plan expired; create a new plan")
	}
	if len(plan.Sources) == 0 || len(plan.Sources) > 64 {
		return errors.New("execution requires one to 64 explicitly read file sources")
	}
	return nil
}

func Read(events []domain.RunEvent) *domain.RunPlan {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != domain.EventPlanRecorded {
			continue
		}
		data, err := json.Marshal(events[i].Payload)
		var plan domain.RunPlan
		if err != nil || json.Unmarshal(data, &plan) != nil || plan.Hash != Hash(plan) {
			return nil
		}
		return &plan
	}
	return nil
}

func Record(run domain.Run, input, proposal string, events []domain.RunEvent) (domain.RunPlan, error) {
	if strings.TrimSpace(proposal) == "" || len(proposal) > 65536 || len(input) > 65536 {
		return domain.RunPlan{}, errors.New("plan input/proposal exceeds bounded non-empty text contract")
	}
	sources := []domain.PlanSource{}
	seen := map[string]domain.PlanSource{}
	for _, item := range events {
		if item.RunID != run.ID || item.Type != domain.EventToolCompleted || item.Payload["tool_name"] != "file_read" {
			continue
		}
		if item.Payload["truncated"] == true || item.Payload["artifact"] != nil {
			return domain.RunPlan{}, errors.New("plan source evidence is incomplete")
		}
		data, err := json.Marshal(item.Payload["result"])
		var source domain.PlanSource
		if err != nil || json.Unmarshal(data, &source) != nil || source.Root == "" || source.Path == "" || source.RootRevision == "" || source.ContentHash == "" || source.FileID == "" {
			return domain.RunPlan{}, errors.New("plan source identity is unavailable")
		}
		key := source.Root + "\x00" + source.Path
		if prior, ok := seen[key]; ok {
			if prior != source {
				return domain.RunPlan{}, errors.New("plan source changed while planning")
			}
			continue
		}
		seen[key] = source
		sources = append(sources, source)
		if len(sources) > 64 {
			return domain.RunPlan{}, errors.New("plan source limit exceeded")
		}
	}
	plan := domain.RunPlan{Version: Version, RunID: run.ID, Input: input, Proposal: proposal, Sources: sources, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	plan.Hash = Hash(plan)
	return plan, nil
}
