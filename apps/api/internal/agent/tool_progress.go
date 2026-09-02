package agent

import (
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/toolprogress"
)

// progressGuardForRun restores one bounded Guard per Run. Terminal Tool events
// are the durable source used after a process restart; in-process Turns reuse
// the same instance across Single, Multi-Agent, and Autonomous execution.
func (r *Runtime) progressGuardForRun(runID string, snapshot *domain.RuntimeSnapshot) (*toolprogress.Guard, error) {
	r.toolProgressMu.Lock()
	defer r.toolProgressMu.Unlock()
	if guard := r.toolProgressGuards[runID]; guard != nil {
		return guard, nil
	}

	config := toolprogress.DisabledConfig()
	if snapshot != nil && snapshot.SchemaVersion >= domain.ToolProgressRuntimeSnapshotVersion {
		config = snapshot.ToolProgressGuard
	}
	guard := toolprogress.New(config)
	events, err := r.store.ListRunEvents(runID)
	if err != nil {
		return nil, fmt.Errorf("restore Tool Progress Guard for run %s: %w", runID, err)
	}
	records := make([]toolprogress.Record, 0)
	for _, item := range events {
		if item.Type != domain.EventToolCompleted && item.Type != domain.EventToolFailed {
			continue
		}
		decision, ok := progressDecisionFromPayload(item.Payload)
		if ok {
			records = append(records, toolprogress.Record{Decision: decision})
		}
	}
	guard.Restore(records)
	r.toolProgressGuards[runID] = guard
	return guard, nil
}

func (r *Runtime) resetProgressGuard(run domain.Run) error {
	guard, err := r.progressGuardForRun(run.ID, run.RuntimeSnapshot)
	if err != nil {
		return err
	}
	guard.Reset()
	return nil
}

func (r *Runtime) forgetProgressGuard(runID string) {
	r.toolProgressMu.Lock()
	defer r.toolProgressMu.Unlock()
	delete(r.toolProgressGuards, runID)
}

func progressDecisionFromPayload(payload map[string]any) (toolprogress.Decision, bool) {
	version, _ := payload["progress_guard_version"].(string)
	signature, _ := payload["progress_guard_signature"].(string)
	outcome, _ := payload["progress_guard_outcome"].(string)
	action, _ := payload["progress_guard_action"].(string)
	if version == "" || action == "" {
		return toolprogress.Decision{}, false
	}
	rule, _ := payload["progress_guard_rule"].(string)
	reason, _ := payload["progress_guard_reason"].(string)
	return toolprogress.Decision{
		Version: version, Rule: toolprogress.Rule(rule), Action: toolprogress.Action(action),
		Count: intValue(payload["progress_guard_count"]), Reason: reason,
		SignatureHash: signature, OutcomeFingerprint: outcome,
		Trackable: boolValue(payload["progress_guard_trackable"]),
		Executed:  boolValue(payload["progress_guard_executed"]),
	}, true
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}
