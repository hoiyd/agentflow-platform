package taskstate

import (
	"context"
	"log"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

type Store interface {
	GetRun(string) (domain.Run, bool, error)
	GetTaskState(string) (domain.TaskState, bool, error)
	GetTaskStateRevision(string, int64) (domain.TaskStateRevision, bool, error)
	ListTaskStateRevisions(string) ([]domain.TaskStateRevision, error)
	ApplyTaskStatePatch(string, domain.TaskStatePatch, domain.TaskStateSource) (domain.TaskStateRevision, error)
}

type Service struct {
	store Store
	sink  eventpkg.Sink
}

func NewService(store Store, sink eventpkg.Sink) *Service {
	return &Service{store: store, sink: sink}
}

func (s *Service) Get(conversationID string) (domain.TaskState, bool, error) {
	return s.store.GetTaskState(strings.TrimSpace(conversationID))
}

func (s *Service) GetRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error) {
	return s.store.GetTaskStateRevision(strings.TrimSpace(conversationID), version)
}

func (s *Service) ListRevisions(conversationID string) ([]domain.TaskStateRevision, error) {
	return s.store.ListTaskStateRevisions(strings.TrimSpace(conversationID))
}

func (s *Service) Apply(ctx context.Context, conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error) {
	conversationID = strings.TrimSpace(conversationID)
	if source.RunID != "" && source.ActorID == "" {
		if run, ok, err := s.store.GetRun(source.RunID); err == nil && ok {
			source.ActorID = run.AgentID
		}
	}
	revision, err := s.store.ApplyTaskStatePatch(conversationID, patch, source)
	if err != nil {
		return domain.TaskStateRevision{}, err
	}
	if s.sink != nil && source.RunID != "" {
		operationTypes := make([]string, 0, len(patch.Operations))
		for _, operation := range patch.Operations {
			operationTypes = append(operationTypes, string(operation.Type))
		}
		payload, payloadErr := eventpkg.Payload(eventpkg.TaskStatePayload{
			RevisionID: revision.ID, Version: revision.Version, PreviousVersion: revision.PreviousVersion,
			OperationTypes: operationTypes, ActorType: revision.Source.ActorType,
		})
		if payloadErr == nil {
			payloadErr = s.sink.Publish(ctx, domain.RunEvent{
				Type: domain.EventTaskStateUpdated, RunID: source.RunID, ConversationID: conversationID,
				StageID: source.StageID, TurnID: source.TurnID, Payload: payload, Timestamp: revision.CreatedAt,
			})
		}
		if payloadErr != nil {
			log.Printf("task_state_event_error revision_id=%s run_id=%s error=%q", revision.ID, source.RunID, payloadErr.Error())
		}
	}
	return revision, nil
}
