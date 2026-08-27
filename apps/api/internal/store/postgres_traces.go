package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/eventcatalog"
	"agentflow-platform/apps/api/internal/projection"
)

func (s *PostgresStore) CreateCollaborationStep(step domain.CollaborationStep) (domain.CollaborationStep, error) {
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

	_, err := s.db.Exec(`
		INSERT INTO collaboration_steps (id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		step.ID, step.RunID, step.ConversationID, step.Role, step.AgentID, string(step.Status), step.Iteration, step.Input, step.Output, step.Error, step.CreatedAt, step.UpdatedAt)
	return step, err
}

func (s *PostgresStore) UpdateCollaborationStep(id string, status domain.CollaborationStepStatus, output string, errorMessage string) (domain.CollaborationStep, error) {
	return s.scanStepQuery(`
		UPDATE collaboration_steps
		SET status = $1, output = $2, error = $3, updated_at = $4
		WHERE id = $5
		RETURNING id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at`,
		string(status), strings.TrimSpace(output), strings.TrimSpace(errorMessage), time.Now().UTC(), id)
}

func (s *PostgresStore) UpdateCollaborationStepOutput(id string, output string) (domain.CollaborationStep, error) {
	return s.scanStepQuery(`
		UPDATE collaboration_steps
		SET output = $1, updated_at = $2
		WHERE id = $3
		RETURNING id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at`,
		strings.TrimSpace(output), time.Now().UTC(), id)
}

func (s *PostgresStore) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, conversation_id, role, agent_id, status, iteration, input, output, error, created_at, updated_at
		FROM collaboration_steps
		WHERE run_id = $1
		ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.CollaborationStep{}
	for rows.Next() {
		step, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, step)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
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
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if err := eventcatalog.ValidateDurableFact(event); err != nil {
		return domain.RunEvent{}, err
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return domain.RunEvent{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RunEvent{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, event.RunID); err != nil {
		return domain.RunEvent{}, err
	}
	err = tx.QueryRow(`
		INSERT INTO run_events (id, run_id, conversation_id, stage_id, turn_id, parent_event_id, type, schema_version, sequence, payload, timestamp)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,
			(SELECT COALESCE(MAX(sequence),0)+1 FROM run_events WHERE run_id=$2),$9,$10)
		RETURNING sequence`, event.ID, event.RunID, event.ConversationID, event.StageID, event.TurnID,
		event.ParentEventID, string(event.Type), event.SchemaVersion, payload, event.Timestamp).Scan(&event.Sequence)
	if err != nil {
		return domain.RunEvent{}, err
	}
	return event, tx.Commit()
}

func (s *PostgresStore) ListRunEvents(runID string) ([]domain.RunEvent, error) {
	rows, err := s.db.Query(`SELECT id,run_id,conversation_id,stage_id,turn_id,parent_event_id,type,schema_version,sequence,payload,timestamp FROM run_events WHERE run_id=$1 ORDER BY sequence`, runID)
	if err != nil {
		return nil, err
	}
	return scanRunEvents(rows)
}

func (s *PostgresStore) ListConversationRunEvents(conversationID string) ([]domain.RunEvent, error) {
	rows, err := s.db.Query(`
		SELECT e.id,e.run_id,e.conversation_id,e.stage_id,e.turn_id,e.parent_event_id,e.type,e.schema_version,e.sequence,e.payload,e.timestamp
		FROM run_events e
		JOIN runs r ON r.id = e.run_id
		WHERE r.conversation_id=$1
		ORDER BY e.timestamp,e.run_id,e.sequence`, conversationID)
	if err != nil {
		return nil, err
	}
	return scanRunEvents(rows)
}

type runEventRows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}

func scanRunEvents(rows runEventRows) ([]domain.RunEvent, error) {
	defer rows.Close()
	items := []domain.RunEvent{}
	for rows.Next() {
		var item domain.RunEvent
		var conversationID, stageID, turnID, parentID sql.NullString
		var eventType string
		var payload []byte
		if err := rows.Scan(&item.ID, &item.RunID, &conversationID, &stageID, &turnID, &parentID, &eventType, &item.SchemaVersion, &item.Sequence, &payload, &item.Timestamp); err != nil {
			return nil, err
		}
		if conversationID.Valid {
			item.ConversationID = conversationID.String
		}
		if stageID.Valid {
			item.StageID = stageID.String
		}
		if turnID.Valid {
			item.TurnID = turnID.String
		}
		if parentID.Valid {
			item.ParentEventID = parentID.String
		}
		item.Type = domain.RunEventType(eventType)
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ApplyRunUsage(entry domain.RunUsageEntry) (domain.RunUsageLedger, bool, error) {
	if err := validateUsageEntry(entry); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	run, ok, err := s.GetRun(entry.RunID)
	if err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	if !ok {
		return domain.RunUsageLedger{}, false, ErrNotFound("run")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, entry.RunID); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	entries, err := listRunUsageEntries(tx, entry.RunID)
	if err != nil {
		return domain.RunUsageLedger{}, false, err
	}
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
	if entry.Kind != domain.UsageModelSettlement {
		current := budget.BuildLedger(entry.RunID, runBudget(run), entries)
		if err := budget.Check(runBudget(run), current.Totals, budget.EntryTotals(entry), entry.OperationID, entry.Purpose); err != nil {
			return current, false, err
		}
	}
	_, err = tx.Exec(`
		INSERT INTO run_usage_entries (
			id, run_id, operation_id, stage_id, turn_id, kind, purpose, model, tool_name,
			model_calls, tool_calls, prompt_tokens, completion_tokens, total_tokens,
			estimated_cost_micros, estimated, timestamp
		) VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		entry.ID, entry.RunID, entry.OperationID, entry.StageID, entry.TurnID, string(entry.Kind),
		string(entry.Purpose), entry.Model, entry.ToolName, entry.ModelCalls, entry.ToolCalls,
		entry.PromptTokens, entry.CompletionTokens, entry.TotalTokens, entry.EstimatedCostMicros,
		entry.Estimated, entry.Timestamp)
	if err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	entries = append(entries, entry)
	ledger := budget.BuildLedger(entry.RunID, runBudget(run), entries)
	if err := tx.Commit(); err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	if entry.Kind == domain.UsageModelSettlement {
		if err := budget.CheckTotals(runBudget(run), ledger.Totals, entry.OperationID, entry.Purpose); err != nil {
			return ledger, true, err
		}
	}
	return ledger, true, nil
}

func (s *PostgresStore) GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error) {
	run, ok, err := s.GetRun(runID)
	if err != nil || !ok {
		return domain.RunUsageLedger{}, ok, err
	}
	entries, err := listRunUsageEntries(s.db, runID)
	if err != nil {
		return domain.RunUsageLedger{}, false, err
	}
	return budget.BuildLedger(runID, runBudget(run), entries), true, nil
}

type usageQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

func listRunUsageEntries(queryer usageQueryer, runID string) ([]domain.RunUsageEntry, error) {
	rows, err := queryer.Query(`
		SELECT id, run_id, operation_id, COALESCE(stage_id,''), COALESCE(turn_id,''),
			kind, purpose, model, tool_name, model_calls, tool_calls, prompt_tokens,
			completion_tokens, total_tokens, estimated_cost_micros, estimated, timestamp
		FROM run_usage_entries WHERE run_id = $1 ORDER BY timestamp, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []domain.RunUsageEntry{}
	for rows.Next() {
		var entry domain.RunUsageEntry
		var kind, purpose string
		if err := rows.Scan(
			&entry.ID, &entry.RunID, &entry.OperationID, &entry.StageID, &entry.TurnID,
			&kind, &purpose, &entry.Model, &entry.ToolName, &entry.ModelCalls, &entry.ToolCalls,
			&entry.PromptTokens, &entry.CompletionTokens, &entry.TotalTokens,
			&entry.EstimatedCostMicros, &entry.Estimated, &entry.Timestamp,
		); err != nil {
			return nil, err
		}
		entry.Kind = domain.RunUsageEntryKind(kind)
		entry.Purpose = domain.RunUsagePurpose(purpose)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *PostgresStore) GetRunTraceSummary(runID string) (domain.RunTraceSummary, error) {
	run, ok, err := s.GetRun(runID)
	if err != nil {
		return domain.RunTraceSummary{}, err
	}
	if !ok {
		return domain.RunTraceSummary{}, ErrNotFound("run")
	}
	events, err := s.ListRunEvents(runID)
	if err != nil {
		return domain.RunTraceSummary{}, err
	}
	return projection.BuildRunTraceSummary(run, events), nil
}

func (s *PostgresStore) GetRunReplay(runID string) (domain.RunReplay, bool, error) {
	run, ok, err := s.GetRun(runID)
	if err != nil || !ok {
		return domain.RunReplay{}, ok, err
	}
	conversation, ok, err := s.GetConversation(run.ConversationID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	if !ok {
		return domain.RunReplay{}, false, errors.New("conversation not found")
	}
	messages, err := s.ListMessages(run.ConversationID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	steps, err := s.ListCollaborationSteps(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	runEvents, err := s.ListRunEvents(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	verificationEvidence, err := s.ListVerificationEvidence(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	verificationArtifacts, err := s.ListVerificationArtifacts(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	usageLedger, _, err := s.GetRunUsageLedger(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	checkpoints, err := s.ListStageCheckpoints(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	toolEffects, err := s.ListToolEffects(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	taskStateRevisions, err := s.ListTaskStateRevisions(run.ConversationID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	childDelegations, err := s.ListRunDelegations(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	parentDelegation, hasParent, err := s.GetParentDelegation(runID)
	if err != nil {
		return domain.RunReplay{}, false, err
	}
	var parentDelegationRef *domain.RunDelegation
	if hasParent {
		parentDelegationRef = &parentDelegation
	}
	readModel := projection.BuildSnapshot(run, runEvents, usageLedger, verificationEvidence)
	return domain.RunReplay{
		Run:                   run,
		Projection:            readModel,
		RuntimeSnapshot:       cloneRuntimeSnapshotValue(run.RuntimeSnapshot),
		Conversation:          conversation,
		Messages:              messages,
		Steps:                 steps,
		Summary:               readModel.Run.Summary,
		RunEvents:             runEvents,
		UsageLedger:           readModel.Usage.Ledger,
		StageCheckpoints:      checkpoints,
		ToolEffects:           domain.SummarizeToolEffects(toolEffects),
		VerificationEvidence:  verificationEvidence,
		VerificationArtifacts: verificationArtifacts,
		TaskStateRevisions:    taskStateRevisions,
		ParentDelegation:      parentDelegationRef,
		ChildDelegations:      childDelegations,
	}, true, nil
}

func (s *PostgresStore) scanStepQuery(query string, args ...any) (domain.CollaborationStep, error) {
	step, err := scanStep(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CollaborationStep{}, errors.New("collaboration step not found")
	}
	return step, err
}

func scanStep(row scanner) (domain.CollaborationStep, error) {
	var step domain.CollaborationStep
	var status string
	var agentID sql.NullString
	if err := row.Scan(&step.ID, &step.RunID, &step.ConversationID, &step.Role, &agentID, &status, &step.Iteration, &step.Input, &step.Output, &step.Error, &step.CreatedAt, &step.UpdatedAt); err != nil {
		return domain.CollaborationStep{}, err
	}
	if agentID.Valid {
		step.AgentID = agentID.String
	}
	step.Status = domain.CollaborationStepStatus(status)
	return step, nil
}
