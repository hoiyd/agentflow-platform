package toolloop

import (
	"context"
	"encoding/json"
	"fmt"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
)

// Continuation retains completed observations and the pending call. Resume
// reassembles/budgets it through the model adapter, never replays the prefix.
type Continuation struct {
	Messages      []provider.Message
	Sources       []string
	References    []string
	Identity      string
	NextRound     int
	ToolSlots     map[string]int
	PendingCallID string
}

func (c *Continuation) RestoreSources() error {
	if c.NextRound < 2 || c.Identity == "" || len(c.Messages) == 0 || len(c.Messages) != len(c.Sources) || len(c.Messages) != len(c.References) {
		return clarification.ErrInvalid
	}
	for i := range c.Messages {
		c.Messages[i].Source = c.Sources[i]
		c.Messages[i].ReferenceID = c.References[i]
	}
	last := c.Messages[len(c.Messages)-1]
	if last.Role != "assistant" || len(last.ToolCalls) != 1 || last.ToolCalls[0].ID != c.PendingCallID || last.ToolCalls[0].Function.Name != clarification.ToolName {
		return clarification.ErrInvalid
	}
	return nil
}

func validateClarificationBatch(calls []provider.ToolCall, request Request) error {
	for _, c := range calls {
		if c.Function.Name == clarification.ToolName {
			if request.SuspendClarification == nil {
				return fmt.Errorf("clarification is unavailable in this execution scope")
			}
			if len(calls) != 1 {
				return fmt.Errorf("request_user_input must be called alone; no part of the mixed batch was executed")
			}
		}
	}
	return nil
}

func suspend(ctx context.Context, request Request, args string, c Continuation) error {
	var input struct {
		Questions []domain.ClarificationQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return err
	}
	if err := clarification.ValidateQuestions(input.Questions); err != nil {
		return err
	}
	c.Sources = make([]string, len(c.Messages))
	c.References = make([]string, len(c.Messages))
	for i, m := range c.Messages {
		c.Sources[i] = m.Source
		c.References[i] = m.ReferenceID
	}
	if err := request.SuspendClarification(ctx, input.Questions, c); err != nil {
		return err
	}
	return clarification.Pending
}
