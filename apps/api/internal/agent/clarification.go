package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

type clarificationCheckpoint struct {
	Version      int
	SnapshotHash string
	Request      turn.Request
	Continuation *toolloop.Continuation
	HumanInput   bool
}

func decodeContinuation(data json.RawMessage) (*toolloop.Continuation, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var c toolloop.Continuation
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Identity == "" || c.NextRound < 2 || len(c.Messages) != len(c.Sources) || len(c.Messages) != len(c.References) {
		return nil, clarification.ErrInvalid
	}
	for i := range c.Messages {
		c.Messages[i].Source = c.Sources[i]
		c.Messages[i].ReferenceID = c.References[i]
	}
	return &c, nil
}

func (r *Runtime) clarificationBoundary(request turn.Request) func(context.Context, []domain.ClarificationQuestion, toolloop.Continuation) error {
	if r.clarifications == nil || request.Context.Isolated || request.Role != "" && request.Role != "act" && request.Role != "planner" {
		return nil
	}
	return func(ctx context.Context, questions []domain.ClarificationQuestion, c toolloop.Continuation) error {
		run, ok, err := r.store.GetRun(request.RunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRuntimeSnapshotUnavailable
		}
		hash, _, err := checkpoint.RuntimeHashes(run.RuntimeSnapshot)
		if err != nil {
			return err
		}
		frozen := request
		frozen.Catalog = nil
		frozen.Sink = nil
		frozen.Continuation = nil
		data, err := json.Marshal(clarificationCheckpoint{Version: 1, SnapshotHash: hash, Request: frozen, Continuation: &c})
		if err != nil {
			return err
		}
		_, err = r.clarifications.SuspendClarification(ctx, domain.Clarification{RunID: request.RunID, ConversationID: request.ConversationID, StageID: request.StepID, TurnID: request.TurnID, ToolCallID: c.PendingCallID, Questions: questions}, data)
		if errors.Is(err, clarification.ErrConflict) {
			current, found, loadErr := r.store.GetRun(request.RunID)
			if loadErr == nil && found && (current.Status == domain.RunCanceling || current.Status == domain.RunCanceled) {
				return context.Canceled
			}
		}
		return err
	}
}

type resumedIterationKey struct{}

func clarificationAnswerText(c domain.Clarification) string {
	parts := []string{}
	for _, q := range c.Questions {
		parts = append(parts, q.Question+"\n"+c.Answers[q.ID])
	}
	return strings.Join(parts, "\n\n")
}

// Explicit input handoff does not grant Worker any parent Tool authority.
func (r *Runtime) taskWithClarifications(runID, task string) (string, error) {
	items, err := r.store.ListRunEvents(runID)
	if err != nil {
		return "", err
	}
	return clarification.AddAnswers(task, items)
}

func (r *Runtime) clarificationCatalog(catalog *tool.Catalog) (*tool.Catalog, error) {
	binding, ok := catalog.ResolveReady(clarification.ToolName)
	if !ok {
		return tool.NewCatalog()
	}
	return tool.NewCatalogWithPolicy(catalog.SecurityPolicy(), binding)
}
