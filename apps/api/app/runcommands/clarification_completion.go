package runcommands

import (
	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"context"
	"errors"
)

func (c Commands) completeClarification(ctx context.Context, scoped store.WorkspaceStore, run domain.Run, answer string, observer Observer) (resultErr error) {
	defer func() {
		if resultErr == nil {
			return
		}
		current, found, loadErr := scoped.GetRun(run.ID)
		if loadErr != nil {
			resultErr = errors.Join(resultErr, loadErr)
			return
		}
		if found && current.Status == domain.RunRunning {
			_, err := c.Runtime.FailRun(run.ID, resultErr)
			resultErr = errors.Join(resultErr, err)
		}
	}()
	messages, err := scoped.ListMessages(run.ConversationID)
	if err != nil {
		return err
	}
	task := ""
	for _, message := range messages {
		if message.Role == "user" && !message.CreatedAt.After(run.CreatedAt) {
			task = message.Content
		}
	}
	events, err := scoped.ListRunEvents(run.ID)
	if err != nil {
		return err
	}
	task, err = clarification.AddAnswers(task, events)
	if err != nil {
		return err
	}
	// A crash after candidate persistence may already have a message receipt.
	// Reuse only an exact Run-linked candidate, never infer identity from text.
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != domain.EventCitationResolved {
			continue
		}
		id, _ := events[i].Payload["message_id"].(string)
		for _, message := range messages {
			if message.ID == id && message.Role == "assistant" && message.Content == answer {
				completed, err := runcompletion.Resolve(ctx, scoped, runcompletion.Dependencies{Runtime: c.Runtime, Verification: c.Verification}, run.ID, task, answer)
				if err != nil {
					return err
				}
				if observer.Done != nil {
					observer.Done(domain.ChatChunk{Type: "done", RunID: run.ID, ConversationID: run.ConversationID, AgentID: completed.AgentID, Status: string(completed.Status), MessageID: message.ID, VerificationStatus: string(completed.VerificationStatus), Citations: message.Citations, WebCitations: message.WebCitations})
				}
				return nil
			}
		}
	}
	return c.complete(ctx, scoped, runcompletion.Request{WorkspaceID: run.WorkspaceID, RunID: run.ID, ConversationID: run.ConversationID, UserInput: task, Assistant: answer}, observer)
}
