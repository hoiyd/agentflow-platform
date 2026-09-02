package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const InternalStateProvider = "internal_state_v1"

var (
	ErrCheckpointStale     = errors.New("stage checkpoint does not match the frozen runtime snapshot")
	ErrNeedsReconciliation = errors.New("stage checkpoint requires reconciliation before resume")
)

type Store interface {
	GetRun(string) (domain.Run, bool, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
	ListRunEvents(string) ([]domain.RunEvent, error)
	SaveStageCheckpoint(domain.StageCheckpoint) (domain.StageCheckpoint, error)
	GetStageCheckpoint(string, string) (domain.StageCheckpoint, bool, error)
	ListStageCheckpoints(string) ([]domain.StageCheckpoint, error)
	ListToolEffects(string) ([]domain.ToolEffectRecord, error)
}

type RestoreReport struct {
	CommittedStageIDs   []string
	CompensatedStageIDs []string
}

type InternalProvider struct{ store Store }

func NewInternalProvider(store Store) *InternalProvider {
	return &InternalProvider{store: store}
}

func (p *InternalProvider) RecordStageTransition(ctx context.Context, step domain.CollaborationStep, eventType domain.RunEventType) (domain.RunEvent, error) {
	if p == nil || p.store == nil {
		return domain.RunEvent{}, errors.New("checkpoint provider is unavailable")
	}
	if !isStageTransition(eventType) {
		return domain.RunEvent{}, fmt.Errorf("unsupported stage checkpoint event %s", eventType)
	}
	run, ok, err := p.store.GetRun(step.RunID)
	if err != nil {
		return domain.RunEvent{}, err
	}
	if !ok {
		return domain.RunEvent{}, errors.New("run not found")
	}
	snapshotHash, toolHash, err := RuntimeHashes(run.RuntimeSnapshot)
	if err != nil {
		return domain.RunEvent{}, err
	}
	events, err := p.store.ListRunEvents(step.RunID)
	if err != nil {
		return domain.RunEvent{}, err
	}
	cursor := eventCursor(events)
	base := domain.StageCheckpoint{
		Provider: InternalStateProvider, RunID: step.RunID, ConversationID: step.ConversationID,
		StageID: step.ID, InputHash: hashValue(step.Input), RuntimeSnapshotHash: snapshotHash,
		ToolDefinitionsHash: toolHash, EventCursor: cursor,
	}
	existing, found, err := p.store.GetStageCheckpoint(step.RunID, step.ID)
	if err != nil {
		return domain.RunEvent{}, err
	}
	if eventType == domain.EventStageStarted && found {
		return domain.RunEvent{}, fmt.Errorf("stage %s already has a checkpoint", step.ID)
	}
	if eventType != domain.EventStageStarted && !found {
		openStart, hasOpenStart := openStageStart(events, step.ID)
		if hasOpenStart {
			base.EventCursor = openStart.Sequence
		}
		base.Status = domain.CheckpointPrepared
		if _, err := p.store.SaveStageCheckpoint(base); err != nil {
			return domain.RunEvent{}, fmt.Errorf("prepare stage checkpoint: %w", err)
		}
		if !hasOpenStart {
			started, err := p.store.CreateRunEvent(stageRunEvent(step, domain.EventStageStarted))
			if err != nil {
				return domain.RunEvent{}, err
			}
			base.EventCursor = started.Sequence
		}
		base.Status = domain.CheckpointExecuting
		existing, err = p.store.SaveStageCheckpoint(base)
		if err != nil {
			return domain.RunEvent{}, fmt.Errorf("start stage checkpoint: %w", err)
		}
		found = true
	}
	if eventType == domain.EventStageStarted {
		base.Status = domain.CheckpointPrepared
		if _, err := p.store.SaveStageCheckpoint(base); err != nil {
			return domain.RunEvent{}, fmt.Errorf("prepare stage checkpoint: %w", err)
		}
	} else if found {
		base.ID = existing.ID
	}

	stageEvent, err := p.store.CreateRunEvent(stageRunEvent(step, eventType))
	if err != nil {
		return domain.RunEvent{}, err
	}
	base.EventCursor = stageEvent.Sequence
	switch eventType {
	case domain.EventStageStarted:
		base.Status = domain.CheckpointExecuting
	case domain.EventStageCompleted:
		base.Status = domain.CheckpointCommitted
		base.OutputHash = hashValue(step.Output)
	case domain.EventStageFailed, domain.EventStageCanceled:
		base.Status = domain.CheckpointNeedsReconciliation
		base.Error = strings.TrimSpace(step.Error)
	default:
		return domain.RunEvent{}, fmt.Errorf("unsupported stage checkpoint event %s", eventType)
	}
	stored, err := p.store.SaveStageCheckpoint(base)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("persist stage checkpoint: %w", err)
	}
	if _, err := p.store.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCheckpointCaptured, RunID: step.RunID, ConversationID: step.ConversationID,
		StageID: step.ID, ParentEventID: stageEvent.ID, Payload: checkpointPayload(stored),
	}); err != nil {
		return domain.RunEvent{}, err
	}
	return stageEvent, nil
}

func isStageTransition(eventType domain.RunEventType) bool {
	switch eventType {
	case domain.EventStageStarted, domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled:
		return true
	default:
		return false
	}
}

func stageRunEvent(step domain.CollaborationStep, eventType domain.RunEventType) domain.RunEvent {
	return domain.RunEvent{
		Type: eventType, RunID: step.RunID, ConversationID: step.ConversationID, StageID: step.ID,
		Payload: map[string]any{
			"name": step.Role, "agent_id": step.AgentID, "iteration": step.Iteration,
			"input": step.Input, "output": step.Output, "error": step.Error,
		},
	}
}

func (p *InternalProvider) RestoreRun(ctx context.Context, run domain.Run) (RestoreReport, error) {
	var report RestoreReport
	if p == nil || p.store == nil {
		return report, errors.New("checkpoint provider is unavailable")
	}
	snapshotHash, toolHash, err := RuntimeHashes(run.RuntimeSnapshot)
	if err != nil {
		return report, err
	}
	checkpoints, err := p.store.ListStageCheckpoints(run.ID)
	if err != nil {
		return report, err
	}
	effects, err := p.store.ListToolEffects(run.ID)
	if err != nil {
		return report, err
	}
	effectsByStage := make(map[string][]domain.ToolEffectRecord)
	for _, effect := range effects {
		effectsByStage[effect.StageID] = append(effectsByStage[effect.StageID], effect)
	}
	events, err := p.store.ListRunEvents(run.ID)
	if err != nil {
		return report, err
	}
	for _, item := range checkpoints {
		if item.Provider != InternalStateProvider || item.RuntimeSnapshotHash != snapshotHash || item.ToolDefinitionsHash != toolHash {
			_ = p.publish(ctx, run, item.StageID, domain.EventCheckpointStale, map[string]any{
				"checkpoint_id": item.ID, "provider": item.Provider,
				"runtime_snapshot_match": item.RuntimeSnapshotHash == snapshotHash,
				"tool_definitions_match": item.ToolDefinitionsHash == toolHash,
			})
			return report, fmt.Errorf("%w: stage %s", ErrCheckpointStale, item.StageID)
		}
		if item.Status == domain.CheckpointPrepared || item.Status == domain.CheckpointExecuting {
			if terminal, ok := stageTerminalAfter(events, item.StageID, item.EventCursor); ok {
				if terminal.Type == domain.EventStageCompleted && hasUncertainEffect(effectsByStage[item.StageID]) {
					return report, fmt.Errorf("%w: stage %s completed with an uncertain tool effect", ErrNeedsReconciliation, item.StageID)
				}
				reconciled, err := p.reconcileStageTerminal(item, terminal)
				if err != nil {
					return report, err
				}
				item = reconciled
			}
		}
		switch item.Status {
		case domain.CheckpointCommitted:
			report.CommittedStageIDs = append(report.CommittedStageIDs, item.StageID)
			if err := p.publish(ctx, run, item.StageID, domain.EventCheckpointRestored, checkpointPayload(item)); err != nil {
				return report, err
			}
		case domain.CheckpointCompensated:
			report.CompensatedStageIDs = append(report.CompensatedStageIDs, item.StageID)
		case domain.CheckpointPrepared, domain.CheckpointExecuting, domain.CheckpointNeedsReconciliation:
			if hasUncertainEffect(effectsByStage[item.StageID]) {
				_ = p.publish(ctx, run, item.StageID, domain.EventCheckpointStale, map[string]any{
					"checkpoint_id": item.ID, "reason": "tool_effect_needs_reconciliation",
				})
				return report, fmt.Errorf("%w: stage %s has an uncertain tool effect", ErrNeedsReconciliation, item.StageID)
			}
			compensated, err := p.compensateInternalState(ctx, run, item)
			if err != nil {
				return report, err
			}
			report.CompensatedStageIDs = append(report.CompensatedStageIDs, compensated.StageID)
		default:
			return report, fmt.Errorf("unknown checkpoint status %q", item.Status)
		}
	}
	return report, nil
}

func (p *InternalProvider) reconcileStageTerminal(item domain.StageCheckpoint, terminal domain.RunEvent) (domain.StageCheckpoint, error) {
	next := item
	next.EventCursor = terminal.Sequence
	switch terminal.Type {
	case domain.EventStageCompleted:
		if item.Status == domain.CheckpointPrepared {
			next.Status = domain.CheckpointExecuting
			stored, err := p.store.SaveStageCheckpoint(next)
			if err != nil {
				return domain.StageCheckpoint{}, err
			}
			next = stored
		}
		output, _ := terminal.Payload["output"].(string)
		next.Status = domain.CheckpointCommitted
		next.OutputHash = hashValue(output)
		next.Error = ""
	case domain.EventStageFailed, domain.EventStageCanceled:
		next.Status = domain.CheckpointNeedsReconciliation
		next.Error, _ = terminal.Payload["error"].(string)
	default:
		return domain.StageCheckpoint{}, fmt.Errorf("event %s is not a stage terminal", terminal.Type)
	}
	return p.store.SaveStageCheckpoint(next)
}

func (p *InternalProvider) compensateInternalState(ctx context.Context, run domain.Run, item domain.StageCheckpoint) (domain.StageCheckpoint, error) {
	if err := p.publish(ctx, run, item.StageID, domain.EventCompensationStarted, checkpointPayload(item)); err != nil {
		return domain.StageCheckpoint{}, err
	}
	if item.Status != domain.CheckpointNeedsReconciliation {
		item.Status = domain.CheckpointNeedsReconciliation
		item.Error = "interrupted stage abandoned during resume"
		stored, err := p.store.SaveStageCheckpoint(item)
		if err != nil {
			_ = p.publish(ctx, run, item.StageID, domain.EventCompensationFailed, map[string]any{"error": err.Error()})
			return domain.StageCheckpoint{}, err
		}
		item = stored
	}
	item.Status = domain.CheckpointCompensated
	stored, err := p.store.SaveStageCheckpoint(item)
	if err != nil {
		_ = p.publish(ctx, run, item.StageID, domain.EventCompensationFailed, map[string]any{"error": err.Error()})
		return domain.StageCheckpoint{}, err
	}
	if err := p.publish(ctx, run, item.StageID, domain.EventCompensationCompleted, checkpointPayload(stored)); err != nil {
		return domain.StageCheckpoint{}, err
	}
	return stored, nil
}

func (p *InternalProvider) publish(_ context.Context, run domain.Run, stageID string, eventType domain.RunEventType, payload map[string]any) error {
	_, err := p.store.CreateRunEvent(domain.RunEvent{
		Type: eventType, RunID: run.ID, ConversationID: run.ConversationID, StageID: stageID, Payload: payload,
	})
	return err
}

func RuntimeHashes(snapshot *domain.RuntimeSnapshot) (string, string, error) {
	if snapshot == nil {
		return "", "", errors.New("runtime snapshot is required for checkpointing")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", "", err
	}
	tools, err := json.Marshal(snapshot.Tools)
	if err != nil {
		return "", "", err
	}
	return hashBytes(encoded), hashBytes(tools), nil
}

func hashValue(value string) string { return hashBytes([]byte(value)) }

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func eventCursor(events []domain.RunEvent) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Sequence
}

func checkpointPayload(item domain.StageCheckpoint) map[string]any {
	return map[string]any{
		"checkpoint_id": item.ID, "provider": item.Provider, "status": item.Status,
		"input_hash": item.InputHash, "output_hash": item.OutputHash,
		"runtime_snapshot_hash": item.RuntimeSnapshotHash,
		"tool_definitions_hash": item.ToolDefinitionsHash, "event_cursor": item.EventCursor,
	}
}

func hasUncertainEffect(effects []domain.ToolEffectRecord) bool {
	for _, item := range effects {
		if item.Status != domain.ToolEffectCommitted && item.Status != domain.ToolEffectCompensated {
			return true
		}
	}
	return false
}

func stageTerminalAfter(events []domain.RunEvent, stageID string, cursor int64) (domain.RunEvent, bool) {
	for _, item := range events {
		if item.Sequence <= cursor || item.StageID != stageID {
			continue
		}
		switch item.Type {
		case domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled:
			return item, true
		}
	}
	return domain.RunEvent{}, false
}

func openStageStart(events []domain.RunEvent, stageID string) (domain.RunEvent, bool) {
	var started domain.RunEvent
	open := false
	for _, item := range events {
		if item.StageID != stageID {
			continue
		}
		switch item.Type {
		case domain.EventStageStarted:
			started = item
			open = true
		case domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled:
			open = false
		}
	}
	return started, open
}
