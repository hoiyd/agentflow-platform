package domain

import "time"

type RunEventType string

const EventToolEffectReconciliationStarted RunEventType = "tool.effect.reconciliation_started"

const (
	EventRunCreated                     RunEventType = "run.created"
	EventRunStarted                     RunEventType = "run.started"
	EventRunProgress                    RunEventType = "run.progress"
	EventRunWaitingForUser              RunEventType = "run.waiting_for_user"
	EventRunResumed                     RunEventType = "run.resumed"
	EventRunCancelRequested             RunEventType = "run.cancel_requested"
	EventRunCanceled                    RunEventType = "run.canceled"
	EventRunCompleted                   RunEventType = "run.completed"
	EventRunFailed                      RunEventType = "run.failed"
	EventStageStarted                   RunEventType = "stage.started"
	EventStageCompleted                 RunEventType = "stage.completed"
	EventStageFailed                    RunEventType = "stage.failed"
	EventStageCanceled                  RunEventType = "stage.canceled"
	EventTurnStarted                    RunEventType = "turn.started"
	EventTurnCompleted                  RunEventType = "turn.completed"
	EventTurnFailed                     RunEventType = "turn.failed"
	EventTurnCanceled                   RunEventType = "turn.canceled"
	EventModelStarted                   RunEventType = "model.started"
	EventModelRouteDecided              RunEventType = "model.route_decided"
	EventModelRequestPrepared           RunEventType = "model.request_prepared"
	EventModelAttemptFinished           RunEventType = "model.attempt_finished"
	EventModelDelta                     RunEventType = "model.delta"
	EventModelCompleted                 RunEventType = "model.completed"
	EventModelFailed                    RunEventType = "model.failed"
	EventContextAssembled               RunEventType = "context.assembled"
	EventCompactionStarted              RunEventType = "context.compaction_started"
	EventCompactionCompleted            RunEventType = "context.compaction_completed"
	EventCompactionFailed               RunEventType = "context.compaction_failed"
	EventToolStarted                    RunEventType = "tool.started"
	EventToolCompleted                  RunEventType = "tool.completed"
	EventToolFailed                     RunEventType = "tool.failed"
	EventToolPolicyEvaluated            RunEventType = "tool.policy_evaluated"
	EventToolGuardWarned                RunEventType = "tool.guard.warned"
	EventToolGuardBlocked               RunEventType = "tool.guard.blocked"
	EventTurnNoProgress                 RunEventType = "turn.no_progress"
	EventToolResultPersisted            RunEventType = "tool.result.persisted"
	EventToolEffectReconciled           RunEventType = "tool.effect.reconciled"
	EventToolEffectReconciliationFailed RunEventType = "tool.effect.reconciliation_failed"
	EventArtifactRead                   RunEventType = "artifact.read"
	EventArtifactExpired                RunEventType = "artifact.expired"
	EventRetrievalStarted               RunEventType = "retrieval.started"
	EventRetrievalCompleted             RunEventType = "retrieval.completed"
	EventRetrievalFailed                RunEventType = "retrieval.failed"
	EventHistorySearchStarted           RunEventType = "session_history.search_started"
	EventHistorySearchCompleted         RunEventType = "session_history.search_completed"
	EventHistorySearchFailed            RunEventType = "session_history.search_failed"
	EventTaskStateUpdated               RunEventType = "task_state.updated"
	EventCitationResolved               RunEventType = "citation.resolved"
	EventMemoryCandidateProposed        RunEventType = "memory.candidate.proposed"
	EventMemoryCandidateAccepted        RunEventType = "memory.candidate.accepted"
	EventMemoryCandidateRejected        RunEventType = "memory.candidate.rejected"
	EventMemoryCandidateFailed          RunEventType = "memory.candidate.failed"
	EventMemoryRecallFailed             RunEventType = "memory.recall.failed"
	EventMemorySyncRequested            RunEventType = "memory.sync.requested"
	EventMemorySyncRejected             RunEventType = "memory.sync.rejected"
	EventMemorySyncCompleted            RunEventType = "memory.sync.completed"
	EventMemorySyncFailed               RunEventType = "memory.sync.failed"
	EventVerificationRequested          RunEventType = "verification.requested"
	EventVerificationStarted            RunEventType = "verification.started"
	EventVerificationPassed             RunEventType = "verification.passed"
	EventVerificationFailed             RunEventType = "verification.failed"
	EventVerificationBlocked            RunEventType = "verification.blocked"
	EventVerificationStale              RunEventType = "verification.stale"
	EventRunRevisionRequested           RunEventType = "run.revision_requested"
	EventUsageRecorded                  RunEventType = "usage.recorded"
	EventBudgetExceeded                 RunEventType = "budget.exceeded"
	EventCheckpointCaptured             RunEventType = "checkpoint.captured"
	EventCheckpointRestored             RunEventType = "checkpoint.restored"
	EventCheckpointStale                RunEventType = "checkpoint.stale"
	EventCompensationStarted            RunEventType = "checkpoint.compensation_started"
	EventCompensationCompleted          RunEventType = "checkpoint.compensation_completed"
	EventCompensationFailed             RunEventType = "checkpoint.compensation_failed"
	EventAgentSelectionDecided          RunEventType = "agent.selection.decided"
)

const CurrentRunEventSchemaVersion = 1

// RunEvent is the typed execution-event contract. Durable sinks persist
// lifecycle events but may omit stream-only events such as model.delta. Trace,
// Replay, and Episode Report views do not maintain another event history.
type RunEvent struct {
	ID             string         `json:"id"`
	Type           RunEventType   `json:"type"`
	SchemaVersion  int            `json:"schema_version"`
	Sequence       int64          `json:"sequence"`
	ConversationID string         `json:"conversation_id,omitempty"`
	RunID          string         `json:"run_id"`
	StageID        string         `json:"stage_id,omitempty"`
	TurnID         string         `json:"turn_id,omitempty"`
	ParentEventID  string         `json:"parent_event_id,omitempty"`
	Payload        map[string]any `json:"payload"`
	Timestamp      time.Time      `json:"timestamp"`
}
