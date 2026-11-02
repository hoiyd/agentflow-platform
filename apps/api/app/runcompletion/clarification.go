package runcompletion

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

// CompleteClarification reuses a persisted candidate after execution recovery,
// otherwise saving it through the ordinary completion gate. Resume does not
// generate a title or enqueue another user turn for Memory.
func CompleteClarification(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run, answer string) (chunk domain.ChatChunk, resultErr error) {
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
			_, err := dependencies.Runtime.FailRun(run.ID, resultErr)
			resultErr = errors.Join(resultErr, err)
		}
	}()
	bound, err := boundCandidate(scoped, run)
	if err == nil {
		if bound.output != answer {
			return domain.ChatChunk{}, errInvalidCandidate
		}
		completed, err := Resolve(ctx, scoped, dependencies, run.ID, bound.question, answer)
		if err != nil {
			return domain.ChatChunk{}, err
		}
		var invalidCitationIDs, invalidWebCitationIDs []string
		for _, item := range bound.events {
			if item.Sequence < bound.binding.Sequence || item.Type != domain.EventCitationResolved || item.Payload["message_id"] != bound.message.ID {
				continue
			}
			switch item.Payload["protocol_version"] {
			case domain.RAGCitationProtocolVersion:
				decodePayloadField(item.Payload, "invalid_source_ids", &invalidCitationIDs)
			case domain.WebCitationProtocolVersion:
				decodePayloadField(item.Payload, "invalid_source_ids", &invalidWebCitationIDs)
			}
		}
		return completionChunk(completed, bound.message, "", invalidCitationIDs, invalidWebCitationIDs), nil
	}
	// Invalid bindings wrap ErrNoCandidate; only an absent receipt may save anew.
	if err != ErrNoCandidate {
		return domain.ChatChunk{}, err
	}
	task, err := storedCompletionQuestion(scoped, run, "")
	if err != nil {
		return domain.ChatChunk{}, err
	}
	return Complete(ctx, scoped, dependencies, Request{WorkspaceID: run.WorkspaceID, RunID: run.ID, ConversationID: run.ConversationID, UserInput: task, Assistant: answer})
}

func questionFromHistory(run domain.Run, question string, messages []domain.Message, events []domain.RunEvent) (string, error) {
	for _, message := range messages {
		if message.Role == "user" && !message.CreatedAt.After(run.CreatedAt) {
			question = message.Content
		}
	}
	return clarification.AddAnswers(question, events)
}
