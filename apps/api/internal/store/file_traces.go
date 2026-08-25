package store

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error) {
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
		step.ID = newID("step")
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
	return step, s.saveLocked()
}

func (s *FileStore) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.CollaborationSteps {
		if s.data.CollaborationSteps[i].ID == id {
			s.data.CollaborationSteps[i].Status = status
			s.data.CollaborationSteps[i].Output = strings.TrimSpace(output)
			s.data.CollaborationSteps[i].Error = strings.TrimSpace(errorMessage)
			s.data.CollaborationSteps[i].UpdatedAt = time.Now().UTC()
			return s.data.CollaborationSteps[i], s.saveLocked()
		}
	}
	return domain.CollaborationStep{}, errors.New("collaboration step not found")
}

func (s *FileStore) UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.CollaborationSteps {
		if s.data.CollaborationSteps[i].ID == id {
			s.data.CollaborationSteps[i].Output = strings.TrimSpace(output)
			s.data.CollaborationSteps[i].UpdatedAt = time.Now().UTC()
			return s.data.CollaborationSteps[i], s.saveLocked()
		}
	}
	return domain.CollaborationStep{}, errors.New("collaboration step not found")
}

func (s *FileStore) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
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

func (s *FileStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
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
	event, err := prepareRunEvent(event, next, time.Now().UTC())
	if err != nil {
		return domain.RunEvent{}, err
	}
	s.data.RunEvents = append(s.data.RunEvents, event)
	return event, s.saveLocked()
}

func prepareRunEvent(event domain.RunEvent, sequence int64, now time.Time) (domain.RunEvent, error) {
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = newID("event")
	}
	if event.Type == "" {
		return domain.RunEvent{}, errors.New("run event type is required")
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = domain.CurrentRunEventSchemaVersion
	}
	event.Sequence = sequence
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = now
	}
	return event, nil
}

func (s *FileStore) ListRunEvents(runID string) ([]domain.RunEvent, error) {
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

func (s *FileStore) ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error) {
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

func (s *FileStore) GetRunTraceSummary(runID string) (domain.RunTraceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunTraceSummary{}, ErrNotFound("run")
	}
	return buildRunTraceSummary(run, s.runEventsForRunLocked(runID)), nil
}

func (s *FileStore) ApplyRunUsage(entry domain.RunUsageEntry) (domain.RunUsageLedger, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.getRunLocked(entry.RunID)
	if !ok {
		return domain.RunUsageLedger{}, false, ErrNotFound("run")
	}
	if err := validateUsageEntry(entry); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	entries := s.runUsageEntriesForRunLocked(entry.RunID)
	for _, existing := range entries {
		if existing.OperationID != entry.OperationID || existing.Kind != entry.Kind {
			continue
		}
		ledger := budget.BuildLedger(entry.RunID, runBudget(run), entries)
		if !sameUsageEntry(existing, entry) {
			return ledger, false, errors.New("run usage operation already recorded with different values")
		}
		return ledger, false, nil
	}
	if entry.Kind == domain.UsageModelSettlement && !hasUsageReservation(entries, entry.OperationID) {
		return budget.BuildLedger(entry.RunID, runBudget(run), entries), false, errors.New("model usage settlement has no reservation")
	}
	proposed := append(append([]domain.RunUsageEntry(nil), entries...), entry)
	ledger := budget.BuildLedger(entry.RunID, runBudget(run), proposed)
	if entry.Kind != domain.UsageModelSettlement {
		current := budget.BuildLedger(entry.RunID, runBudget(run), entries)
		if err := budget.Check(runBudget(run), current.Totals, budget.EntryTotals(entry), entry.OperationID, entry.Purpose); err != nil {
			return current, false, err
		}
	}

	s.data.RunUsageEntries = append(s.data.RunUsageEntries, entry)
	if err := s.saveLocked(); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	if entry.Kind == domain.UsageModelSettlement {
		if err := budget.CheckTotals(runBudget(run), ledger.Totals, entry.OperationID, entry.Purpose); err != nil {
			return ledger, true, err
		}
	}
	return ledger, true, nil
}

func (s *FileStore) GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.getRunLocked(runID)
	if !ok {
		return domain.RunUsageLedger{}, false, nil
	}
	return budget.BuildLedger(runID, runBudget(run), s.runUsageEntriesForRunLocked(runID)), true, nil
}

func (s *FileStore) GetRunReplay(runID string) (domain.RunReplay, bool, error) {
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
	checkpoints := make([]domain.StageCheckpoint, 0)
	for _, item := range s.data.StageCheckpoints {
		if item.RunID == runID {
			checkpoints = append(checkpoints, item)
		}
	}
	toolEffectRecords := make([]domain.ToolEffectRecord, 0)
	for _, item := range s.data.ToolEffects {
		if item.RunID == runID {
			toolEffectRecords = append(toolEffectRecords, cloneToolEffect(item))
		}
	}
	taskStateRevisions := make([]domain.TaskStateRevision, 0)
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == run.ConversationID {
			taskStateRevisions = append(taskStateRevisions, cloneTaskStateRevision(item))
		}
	}
	sort.Slice(taskStateRevisions, func(i, j int) bool { return taskStateRevisions[i].Version < taskStateRevisions[j].Version })
	return domain.RunReplay{
		Run:                   cloneRun(run),
		RuntimeSnapshot:       cloneRuntimeSnapshotValue(run.RuntimeSnapshot),
		Conversation:          conversation,
		Messages:              messages,
		Steps:                 steps,
		Summary:               buildRunTraceSummary(run, runEvents),
		UsageLedger:           budget.BuildLedger(runID, runBudget(run), s.runUsageEntriesForRunLocked(runID)),
		RunEvents:             runEvents,
		StageCheckpoints:      checkpoints,
		ToolEffects:           domain.SummarizeToolEffects(toolEffectRecords),
		VerificationEvidence:  verificationEvidenceForRun(s.data.VerificationEvidence, runID),
		VerificationArtifacts: verificationArtifactsForRun(s.data.VerificationArtifacts, runID),
		TaskStateRevisions:    taskStateRevisions,
	}, true, nil
}

func cloneRuntimeSnapshot(snapshot domain.RuntimeSnapshot) *domain.RuntimeSnapshot {
	bytes, _ := json.Marshal(snapshot)
	var cloned domain.RuntimeSnapshot
	_ = json.Unmarshal(bytes, &cloned)
	return &cloned
}

func cloneRuntimeSnapshotValue(snapshot *domain.RuntimeSnapshot) *domain.RuntimeSnapshot {
	if snapshot == nil {
		return nil
	}
	return cloneRuntimeSnapshot(*snapshot)
}

func cloneRun(run domain.Run) domain.Run {
	run.RuntimeSnapshot = cloneRuntimeSnapshotValue(run.RuntimeSnapshot)
	run.CompletionContract = cloneCompletionContract(run.CompletionContract)
	return run
}

func (s *FileStore) runUsageEntriesForRunLocked(runID string) []domain.RunUsageEntry {
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

func runBudget(run domain.Run) domain.RuntimeRunBudget {
	if run.RuntimeSnapshot == nil || run.RuntimeSnapshot.RunBudget == nil {
		return domain.RuntimeRunBudget{}
	}
	return *run.RuntimeSnapshot.RunBudget
}

func validateUsageEntry(entry domain.RunUsageEntry) error {
	if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.RunID) == "" || strings.TrimSpace(entry.OperationID) == "" {
		return errors.New("run usage requires id, run_id, and operation_id")
	}
	if entry.Purpose != domain.UsagePurposePrimary && entry.Purpose != domain.UsagePurposeRouter && entry.Purpose != domain.UsagePurposeCompaction {
		return errors.New("run usage purpose is invalid")
	}
	if entry.ModelCalls < 0 || entry.ToolCalls < 0 || entry.PromptTokens < 0 || entry.CompletionTokens < 0 || entry.TotalTokens < 0 || entry.EstimatedCostMicros < 0 {
		return errors.New("run usage values cannot be negative")
	}
	switch entry.Kind {
	case domain.UsageModelReservation, domain.UsageModelSettlement:
		if entry.ModelCalls != 1 || entry.ToolCalls != 0 || entry.TotalTokens < entry.PromptTokens+entry.CompletionTokens {
			return errors.New("model usage entry has invalid counters")
		}
	case domain.UsageToolExecution:
		if entry.ToolCalls != 1 || entry.ModelCalls != 0 || entry.PromptTokens != 0 || entry.CompletionTokens != 0 || entry.TotalTokens != 0 || entry.EstimatedCostMicros != 0 {
			return errors.New("tool usage entry has invalid counters")
		}
	default:
		return errors.New("run usage kind is invalid")
	}
	if entry.Timestamp.IsZero() {
		return errors.New("run usage timestamp is required")
	}
	return nil
}

func hasUsageReservation(entries []domain.RunUsageEntry, operationID string) bool {
	for _, entry := range entries {
		if entry.OperationID == operationID && entry.Kind == domain.UsageModelReservation {
			return true
		}
	}
	return false
}

func sameUsageEntry(left, right domain.RunUsageEntry) bool {
	left.ID, right.ID = "", ""
	left.Timestamp, right.Timestamp = time.Time{}, time.Time{}
	return left == right
}

func cloneCompletionContract(contract *domain.CompletionContract) *domain.CompletionContract {
	if contract == nil {
		return nil
	}
	bytes, _ := json.Marshal(contract)
	var cloned domain.CompletionContract
	_ = json.Unmarshal(bytes, &cloned)
	return &cloned
}

func verificationEvidenceForRun(items []domain.VerificationEvidence, runID string) []domain.VerificationEvidence {
	result := []domain.VerificationEvidence{}
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, cloneVerificationEvidence(item))
		}
	}
	return result
}

func cloneVerificationEvidence(evidence domain.VerificationEvidence) domain.VerificationEvidence {
	evidence.ArtifactIDs = append([]string(nil), evidence.ArtifactIDs...)
	if evidence.Details == nil {
		evidence.Details = map[string]any{}
	} else {
		encoded, _ := json.Marshal(evidence.Details)
		evidence.Details = nil
		_ = json.Unmarshal(encoded, &evidence.Details)
	}
	return evidence
}

func verificationArtifactsForRun(items []domain.VerificationArtifact, runID string) []domain.VerificationArtifact {
	result := []domain.VerificationArtifact{}
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, item)
		}
	}
	return result
}

func (s *FileStore) stepsForRunLocked(runID string) []domain.CollaborationStep {
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

func (s *FileStore) runEventsForRunLocked(runID string) []domain.RunEvent {
	events := []domain.RunEvent{}
	for _, event := range s.data.RunEvents {
		if event.RunID == runID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	return events
}

func buildRunTraceSummary(run domain.Run, events []domain.RunEvent) domain.RunTraceSummary {
	summary := domain.RunTraceSummary{
		RunID:  run.ID,
		Status: run.Status,
	}
	if run.StartedAt != nil {
		end := time.Now().UTC()
		if run.CompletedAt != nil {
			end = *run.CompletedAt
		}
		if end.After(*run.StartedAt) {
			summary.TotalDurationMS = end.Sub(*run.StartedAt).Milliseconds()
		}
	}
	for _, event := range events {
		switch event.Type {
		case domain.EventModelCompleted:
			summary.LLMCalls++
			summary.PromptTokens += intPayload(event.Payload, "prompt_tokens")
			summary.CompletionTokens += intPayload(event.Payload, "completion_tokens")
			summary.TotalTokens += intPayload(event.Payload, "total_tokens")
			if boolPayload(event.Payload, "token_usage_estimated") {
				summary.TokenUsageEstimated = true
			}
		case domain.EventToolCompleted:
			summary.ToolCalls++
		case domain.EventToolFailed:
			summary.ToolCalls++
			summary.ErrorCount++
		case domain.EventModelFailed, domain.EventRetrievalFailed, domain.EventHistorySearchFailed, domain.EventCompactionFailed, domain.EventMemoryCandidateFailed, domain.EventMemorySyncFailed, domain.EventBudgetExceeded:
			summary.ErrorCount++
		}
	}
	return summary
}

func intPayload(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func boolPayload(payload map[string]any, key string) bool {
	value, ok := payload[key].(bool)
	return ok && value
}
