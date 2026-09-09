package fixturestore

import (
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"

	"agentflow-platform/apps/api/internal/projection"
	"agentflow-platform/apps/api/internal/store"

	"errors"
	"sort"
	"strings"
	"time"
)

func (s *Store) CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasRunLocked(step.RunID) {
		return domain.CollaborationStep{}, errors.New("run not found")
	}
	if !s.hasConversationLocked(step.ConversationID) {
		return domain.CollaborationStep{}, errors.New("conversation not found")
	}
	now := time.Now().UTC()
	step.ID = strings.TrimSpace(step.ID)
	if step.ID == "" {
		step.ID = store.NewID("step")
	}
	step.Role = strings.TrimSpace(step.Role)
	if step.Role == "" {
		return domain.CollaborationStep{}, errors.New("collaboration role is required")
	}
	if step.Status == "" {
		step.Status = domain.CollaborationStepQueued
	}
	step.Input = strings.TrimSpace(step.Input)
	step.Output = strings.TrimSpace(step.Output)
	step.Error = strings.TrimSpace(step.Error)
	if step.Iteration < 0 {
		step.Iteration = 0
	}
	step.CreatedAt = now
	step.UpdatedAt = now
	s.data.CollaborationSteps = append(s.data.CollaborationSteps, step)
	return step, nil
}

func (s *Store) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.CollaborationSteps {
		if s.data.CollaborationSteps[i].ID == id {
			s.data.CollaborationSteps[i].Status = status
			s.data.CollaborationSteps[i].Output = strings.TrimSpace(output)
			s.data.CollaborationSteps[i].Error = strings.TrimSpace(errorMessage)
			s.data.CollaborationSteps[i].UpdatedAt = time.Now().UTC()
			return s.data.CollaborationSteps[i], nil
		}
	}
	return domain.CollaborationStep{}, errors.New("collaboration step not found")
}

func (s *Store) UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.CollaborationSteps {
		if s.data.CollaborationSteps[i].ID == id {
			s.data.CollaborationSteps[i].Output = strings.TrimSpace(output)
			s.data.CollaborationSteps[i].UpdatedAt = time.Now().UTC()
			return s.data.CollaborationSteps[i], nil
		}
	}
	return domain.CollaborationStep{}, errors.New("collaboration step not found")
}

func (s *Store) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := []domain.CollaborationStep{}
	for _, step := range s.data.CollaborationSteps {
		if step.RunID == runID {
			items = append(items, step)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Store) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(event.RunID) {
		return domain.RunEvent{}, errors.New("run not found")
	}
	var next int64 = 1
	for _, existing := range s.data.RunEvents {
		if existing.RunID == event.RunID && existing.Sequence >= next {
			next = existing.Sequence + 1
		}
	}
	event, err := store.PrepareRunEvent(event, next, time.Now().UTC())
	if err != nil {
		return domain.RunEvent{}, err
	}
	s.data.RunEvents = append(s.data.RunEvents, event)
	return event, nil
}

func (s *Store) ListRunEvents(runID string) ([]domain.RunEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.RunEvent{}
	for _, event := range s.data.RunEvents {
		if event.RunID == runID {
			items = append(items, event)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Sequence < items[j].Sequence })
	return items, nil
}

func (s *Store) ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runIDs := make(map[string]bool)
	for _, run := range s.data.Runs {
		if run.ConversationID == conversationID {
			runIDs[run.ID] = true
		}
	}
	items := []domain.RunEvent{}
	for _, event := range s.data.RunEvents {
		if event.ConversationID == conversationID || runIDs[event.RunID] {
			items = append(items, event)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Timestamp.Equal(items[j].Timestamp) {
			if items[i].RunID == items[j].RunID {
				return items[i].Sequence < items[j].Sequence
			}
			return items[i].RunID < items[j].RunID
		}
		return items[i].Timestamp.Before(items[j].Timestamp)
	})
	return items, nil
}

func (s *Store) ApplyRunUsage(entry domain.RunUsageEntry) (domain.RunUsageLedger, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.getRunLocked(entry.RunID)
	if !ok {
		return domain.RunUsageLedger{}, false, store.ErrNotFound("run")
	}
	if err := store.ValidateUsageEntry(entry); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	entries := s.runUsageEntriesForRunLocked(entry.RunID)
	for _, existing := range entries {
		if existing.OperationID != entry.OperationID || existing.Kind != entry.Kind {
			continue
		}
		ledger := budget.BuildLedger(entry.RunID, store.RunBudget(run), entries)
		if !store.SameUsageEntry(existing, entry) {
			return ledger, false, errors.New("run usage operation already recorded with different values")
		}
		return ledger, false, nil
	}
	if entry.Kind == domain.UsageModelSettlement && !store.HasUsageReservation(entries, entry.OperationID) {
		return budget.BuildLedger(entry.RunID, store.RunBudget(run), entries), false, errors.New("model usage settlement has no reservation")
	}
	proposed := append(append([]domain.RunUsageEntry(nil), entries...), entry)
	ledger := budget.BuildLedger(entry.RunID, store.RunBudget(run), proposed)
	if entry.Kind != domain.UsageModelSettlement {
		current := budget.BuildLedger(entry.RunID, store.RunBudget(run), entries)
		if err := budget.Check(store.RunBudget(run), current.Totals, budget.EntryTotals(entry), entry.OperationID, entry.Purpose); err != nil {
			return current, false, err
		}
	}

	s.data.RunUsageEntries = append(s.data.RunUsageEntries, entry)

	if entry.Kind == domain.UsageModelSettlement {
		if err := budget.CheckTotals(store.RunBudget(run), ledger.Totals, entry.OperationID, entry.Purpose); err != nil {
			return ledger, true, err
		}
	}
	return ledger, true, nil
}

func (s *Store) GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunUsageLedger{}, false, nil
	}
	return budget.BuildLedger(runID, store.RunBudget(run), s.runUsageEntriesForRunLocked(runID)), true, nil
}

func (s *Store) GetRunReplay(runID string) (domain.RunReplay, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunReplay{}, false, nil
	}
	conversation, ok := s.getConversationLocked(run.ConversationID)
	if !ok {
		return domain.RunReplay{}, false, errors.New("conversation not found")
	}
	messages := s.messagesForConversationLocked(run.ConversationID)
	steps := s.stepsForRunLocked(runID)
	runEvents := s.runEventsForRunLocked(runID)
	usageLedger := budget.BuildLedger(runID, store.RunBudget(run), s.runUsageEntriesForRunLocked(runID))
	verificationEvidence := store.VerificationEvidenceForRun(s.data.VerificationEvidence, runID)
	readModel := projection.BuildSnapshot(run, runEvents, usageLedger, verificationEvidence)
	checkpoints := make([]domain.StageCheckpoint, 0)
	for _, item := range s.data.StageCheckpoints {
		if item.RunID == runID {
			checkpoints = append(checkpoints, item)
		}
	}
	toolEffectRecords := make([]domain.ToolEffectRecord, 0)
	for _, item := range s.data.ToolEffects {
		if item.RunID == runID {
			toolEffectRecords = append(toolEffectRecords, store.CloneToolEffect(item))
		}
	}
	taskStateRevisions := make([]domain.TaskStateRevision, 0)
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == run.ConversationID {
			taskStateRevisions = append(taskStateRevisions, store.CloneTaskStateRevision(item))
		}
	}
	sort.Slice(taskStateRevisions, func(i, j int) bool { return taskStateRevisions[i].Version < taskStateRevisions[j].Version })
	childDelegations := []domain.RunDelegation{}
	var parentDelegation *domain.RunDelegation
	for _, item := range s.data.RunDelegations {
		if item.ParentRunID == runID {
			childDelegations = append(childDelegations, item)
		}
		if item.ChildRunID == runID {
			copy := item
			parentDelegation = &copy
		}
	}
	sort.Slice(childDelegations, func(i, j int) bool { return childDelegations[i].CreatedAt.Before(childDelegations[j].CreatedAt) })
	replay := domain.RunReplay{
		Run:                   store.CloneRun(run),
		Projection:            readModel,
		RuntimeSnapshot:       store.CloneRuntimeSnapshotValue(run.RuntimeSnapshot),
		Conversation:          conversation,
		Messages:              messages,
		Steps:                 steps,
		Summary:               readModel.Run.Summary,
		UsageLedger:           readModel.Usage.Ledger,
		RunEvents:             runEvents,
		StageCheckpoints:      checkpoints,
		ToolEffects:           domain.SummarizeToolEffects(toolEffectRecords),
		ToolArtifacts:         store.ToolArtifactsForRun(s.data.ToolArtifacts, runID),
		VerificationEvidence:  verificationEvidence,
		VerificationArtifacts: store.VerificationArtifactsForRun(s.data.VerificationArtifacts, runID),
		TaskStateRevisions:    taskStateRevisions,
		ParentDelegation:      parentDelegation,
		ChildDelegations:      childDelegations,
	}
	replay.RecoverySummary = projection.BuildRecoverySummary(replay)
	return replay, true, nil
}

func (s *Store) runUsageEntriesForRunLocked(runID string) []domain.RunUsageEntry {
	entries := []domain.RunUsageEntry{}
	for _, entry := range s.data.RunUsageEntries {
		if entry.RunID == runID {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	return entries
}

func (s *Store) stepsForRunLocked(runID string) []domain.CollaborationStep {
	steps := []domain.CollaborationStep{}
	for _, step := range s.data.CollaborationSteps {
		if step.RunID == runID {
			steps = append(steps, step)
		}
	}
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].CreatedAt.Before(steps[j].CreatedAt)
	})
	return steps
}

func (s *Store) runEventsForRunLocked(runID string) []domain.RunEvent {
	events := []domain.RunEvent{}
	for _, event := range s.data.RunEvents {
		if event.RunID == runID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	return events
}
