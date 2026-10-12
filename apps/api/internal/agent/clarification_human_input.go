package agent

import (
	"context"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
)

// Textual Autonomous input uses the same durable record, not a second inbox.
func (r *Runtime) suspendHumanInputClarification(ctx context.Context, prepared PreparedCollaborationRun, step domain.CollaborationStep, question, task string) (run domain.Run, err error) {
	committed := false
	defer func() {
		if err != nil && !committed {
			err = errors.Join(err, r.failResumedClarification(ctx, prepared.Run.ID, step.ID, err))
		}
	}()
	items, err := r.store.ListRunEvents(prepared.Run.ID)
	if err != nil {
		return run, err
	}
	turnID := ""
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].TurnID != "" {
			turnID = items[i].TurnID
			break
		}
	}
	hash, _, err := checkpoint.RuntimeHashes(prepared.Run.RuntimeSnapshot)
	if err != nil {
		return run, err
	}
	data, err := json.Marshal(clarificationCheckpoint{Version: 1, SnapshotHash: hash, HumanInput: true, Request: turn.Request{RunID: prepared.Run.ID, ConversationID: prepared.Run.ConversationID, StepID: step.ID, TurnID: turnID, Input: task}})
	if err != nil {
		return run, err
	}
	_, err = r.clarifications.SuspendClarification(ctx, domain.Clarification{RunID: prepared.Run.ID, ConversationID: prepared.Run.ConversationID, StageID: step.ID, TurnID: turnID, Questions: []domain.ClarificationQuestion{{ID: "details", Question: question}}}, data)
	if err != nil {
		return run, err
	}
	committed = true
	run, _, err = r.store.GetRun(prepared.Run.ID)
	return run, err
}
