package eventcatalog

import (
	"fmt"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type Durability string

const (
	DurableFact Durability = "durable"
	LiveOnly    Durability = "live"
)

type ScopeRequirement string

const (
	ScopeOptional ScopeRequirement = "optional"
	ScopeRequired ScopeRequirement = "required"
)

type ScopeContract struct {
	Stage ScopeRequirement `json:"stage"`
	Turn  ScopeRequirement `json:"turn"`
}

type LifecycleRole string

const (
	LifecycleNone       LifecycleRole = "none"
	LifecycleStart      LifecycleRole = "start"
	LifecycleTerminal   LifecycleRole = "terminal"
	LifecycleTransition LifecycleRole = "transition"
)

type LifecycleContract struct {
	Family      string              `json:"family"`
	Role        LifecycleRole       `json:"role"`
	TerminalFor domain.RunEventType `json:"terminal_for,omitempty"`
}

// Definition is the executable protocol contract for one RunEvent type.
// Consumers is documentation metadata; durability, schema, and scope are
// enforced at producer and persistence boundaries.
type Definition struct {
	Type          domain.RunEventType `json:"type"`
	Durability    Durability          `json:"durability"`
	SchemaVersion int                 `json:"schema_version"`
	Scope         ScopeContract       `json:"scope"`
	Producer      string              `json:"producer"`
	PayloadSchema string              `json:"payload_schema"`
	Lifecycle     LifecycleContract   `json:"lifecycle"`
	Consumers     []string            `json:"consumers"`
}

var registry = buildRegistry()

func DefinitionFor(eventType domain.RunEventType) (Definition, bool) {
	definition, ok := registry[eventType]
	definition.Consumers = append([]string(nil), definition.Consumers...)
	return definition, ok
}

func Definitions() []Definition {
	items := make([]Definition, 0, len(registry))
	for _, definition := range registry {
		definition.Consumers = append([]string(nil), definition.Consumers...)
		items = append(items, definition)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Type < items[j].Type })
	return items
}

func ValidateEnvelope(item domain.RunEvent) error {
	definition, ok := DefinitionFor(item.Type)
	if !ok {
		return fmt.Errorf("run event type %q is not registered", item.Type)
	}
	if strings.TrimSpace(item.RunID) == "" {
		return fmt.Errorf("run event %s requires run_id", item.Type)
	}
	if item.SchemaVersion != definition.SchemaVersion {
		return fmt.Errorf("run event %s schema version %d is unsupported; expected %d", item.Type, item.SchemaVersion, definition.SchemaVersion)
	}
	if definition.Scope.Stage == ScopeRequired && strings.TrimSpace(item.StageID) == "" {
		return fmt.Errorf("run event %s requires stage_id", item.Type)
	}
	if definition.Scope.Turn == ScopeRequired && strings.TrimSpace(item.TurnID) == "" {
		return fmt.Errorf("run event %s requires turn_id", item.Type)
	}
	return nil
}

func ValidateDurableFact(item domain.RunEvent) error {
	if err := ValidateEnvelope(item); err != nil {
		return err
	}
	definition, _ := DefinitionFor(item.Type)
	if definition.Durability != DurableFact {
		return fmt.Errorf("run event %s is live-only and cannot be persisted", item.Type)
	}
	return nil
}

func IsDurable(eventType domain.RunEventType) bool {
	definition, ok := DefinitionFor(eventType)
	return ok && definition.Durability == DurableFact
}

func buildRegistry() map[domain.RunEventType]Definition {
	items := map[domain.RunEventType]Definition{}
	optional := ScopeContract{Stage: ScopeOptional, Turn: ScopeOptional}
	stage := ScopeContract{Stage: ScopeRequired, Turn: ScopeOptional}
	turn := ScopeContract{Stage: ScopeOptional, Turn: ScopeRequired}
	add := func(types []domain.RunEventType, durability Durability, scope ScopeContract, payloadSchema string, lifecycle LifecycleContract, consumers ...string) {
		for _, eventType := range types {
			if _, exists := items[eventType]; exists {
				panic(fmt.Sprintf("duplicate run event registration: %s", eventType))
			}
			items[eventType] = Definition{
				Type: eventType, Durability: durability, SchemaVersion: domain.CurrentRunEventSchemaVersion,
				Scope: scope, Producer: producerFor(eventType), PayloadSchema: payloadSchema,
				Lifecycle: lifecycle, Consumers: append([]string(nil), consumers...),
			}
		}
	}
	start := func(family string) LifecycleContract { return LifecycleContract{Family: family, Role: LifecycleStart} }
	terminal := func(family string, started domain.RunEventType) LifecycleContract {
		return LifecycleContract{Family: family, Role: LifecycleTerminal, TerminalFor: started}
	}
	transition := func(family string) LifecycleContract {
		return LifecycleContract{Family: family, Role: LifecycleTransition}
	}
	none := LifecycleContract{Role: LifecycleNone}

	add([]domain.RunEventType{domain.EventRunCreated}, DurableFact, optional, "event.RunStatusPayload", start("run"), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventRunStarted, domain.EventRunWaitingForUser,
		domain.EventRunResumed, domain.EventRunCancelRequested}, DurableFact, optional, "event.RunStatusPayload", transition("run"), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventRunRevisionRequested}, DurableFact, optional, "event.TracePayload", transition("run"), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventRunCanceled, domain.EventRunCompleted, domain.EventRunFailed}, DurableFact, optional, "event.RunStatusPayload", terminal("run", domain.EventRunCreated), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventRunProgress}, LiveOnly, optional, "event.RunProgressPayload", none, "live_ui")

	add([]domain.RunEventType{domain.EventStageStarted}, DurableFact, stage, "event.StagePayload", start("stage"), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled}, DurableFact, stage, "event.StagePayload", terminal("stage", domain.EventStageStarted), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventTurnStarted}, DurableFact, turn, "event.TracePayload", start("turn"), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled}, DurableFact, turn, "event.TracePayload", terminal("turn", domain.EventTurnStarted), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventModelStarted}, DurableFact, optional, "event.ModelPayload", start("model"), "run_projection", "request_capture", "replay")
	add([]domain.RunEventType{domain.EventModelCompleted, domain.EventModelFailed}, DurableFact, optional, "event.ModelPayload", terminal("model", domain.EventModelStarted), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventModelDelta}, LiveOnly, optional, "event.ModelPayload", none, "live_ui")
	add([]domain.RunEventType{domain.EventModelRequestPrepared}, DurableFact, optional, "event.ModelRequestPreparedPayload", transition("model"), "request_capture", "replay")

	add([]domain.RunEventType{domain.EventContextAssembled}, DurableFact, optional, "event.ContextAssembledPayload", none, "request_capture", "replay")
	add([]domain.RunEventType{domain.EventCompactionStarted}, DurableFact, optional, "event.ContextCompactionPayload", start("compaction"), "replay")
	add([]domain.RunEventType{domain.EventCompactionCompleted}, DurableFact, optional, "event.ContextCompactionPayload", terminal("compaction", domain.EventCompactionStarted), "replay")
	add([]domain.RunEventType{domain.EventCompactionFailed}, DurableFact, optional, "event.ContextCompactionPayload", terminal("compaction", domain.EventCompactionStarted), "run_projection", "replay")

	add([]domain.RunEventType{domain.EventToolStarted}, DurableFact, optional, "event.ToolPayload", start("tool"), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventToolCompleted, domain.EventToolFailed}, DurableFact, optional, "event.ToolPayload", terminal("tool", domain.EventToolStarted), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventRetrievalStarted}, DurableFact, optional, "event.RetrievalPayload", start("retrieval"), "replay")
	add([]domain.RunEventType{domain.EventRetrievalCompleted}, DurableFact, optional, "event.RetrievalPayload", terminal("retrieval", domain.EventRetrievalStarted), "replay")
	add([]domain.RunEventType{domain.EventRetrievalFailed}, DurableFact, optional, "event.RetrievalPayload", terminal("retrieval", domain.EventRetrievalStarted), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventHistorySearchStarted}, DurableFact, optional, "event.SessionHistorySearchPayload", start("history_search"), "replay")
	add([]domain.RunEventType{domain.EventHistorySearchCompleted}, DurableFact, optional, "event.SessionHistorySearchPayload", terminal("history_search", domain.EventHistorySearchStarted), "replay")
	add([]domain.RunEventType{domain.EventHistorySearchFailed}, DurableFact, optional, "event.SessionHistorySearchPayload", terminal("history_search", domain.EventHistorySearchStarted), "run_projection", "replay")

	add([]domain.RunEventType{domain.EventTaskStateUpdated}, DurableFact, optional, "event.TaskStatePayload", none, "replay")
	add([]domain.RunEventType{domain.EventCitationResolved}, DurableFact, optional, "event.TracePayload", none, "replay")
	add([]domain.RunEventType{domain.EventMemoryCandidateProposed}, DurableFact, optional, "event.MemoryCandidatePayload", start("memory_candidate"), "replay")
	add([]domain.RunEventType{domain.EventMemoryCandidateAccepted, domain.EventMemoryCandidateRejected}, DurableFact, optional, "event.MemoryCandidatePayload", terminal("memory_candidate", domain.EventMemoryCandidateProposed), "replay")
	add([]domain.RunEventType{domain.EventMemoryCandidateFailed}, DurableFact, optional, "event.MemoryCandidatePayload", terminal("memory_candidate", domain.EventMemoryCandidateProposed), "run_projection", "replay")
	add([]domain.RunEventType{domain.EventMemorySyncRequested}, DurableFact, optional, "event.TracePayload", start("memory_sync"), "replay")
	add([]domain.RunEventType{domain.EventMemorySyncCompleted}, DurableFact, optional, "event.TracePayload", terminal("memory_sync", domain.EventMemorySyncRequested), "replay")
	add([]domain.RunEventType{domain.EventMemorySyncFailed}, DurableFact, optional, "event.TracePayload", terminal("memory_sync", domain.EventMemorySyncRequested), "run_projection", "replay")

	add([]domain.RunEventType{domain.EventVerificationRequested}, DurableFact, optional, "event.TracePayload", start("verification_attempt"), "verification_evidence", "replay")
	add([]domain.RunEventType{domain.EventVerificationStarted}, DurableFact, optional, "event.TracePayload", start("verifier"), "verification_evidence", "replay")
	add([]domain.RunEventType{domain.EventVerificationPassed, domain.EventVerificationFailed, domain.EventVerificationBlocked}, DurableFact, optional, "event.TracePayload", terminal("verifier", domain.EventVerificationStarted), "verification_evidence", "replay")
	add([]domain.RunEventType{domain.EventVerificationStale}, DurableFact, optional, "event.TracePayload", transition("verification_attempt"), "verification_evidence", "replay")

	add([]domain.RunEventType{domain.EventUsageRecorded}, DurableFact, optional, "event.UsagePayload", none, "usage_ledger", "replay")
	add([]domain.RunEventType{domain.EventBudgetExceeded}, DurableFact, optional, "event.BudgetExceededPayload", none, "run_projection", "usage_ledger", "replay")
	add([]domain.RunEventType{domain.EventCheckpointCaptured, domain.EventCheckpointRestored, domain.EventCheckpointStale}, DurableFact, stage, "event.TracePayload", transition("checkpoint"), "recovery", "replay")
	add([]domain.RunEventType{domain.EventCompensationStarted}, DurableFact, stage, "event.TracePayload", start("compensation"), "recovery", "replay")
	add([]domain.RunEventType{domain.EventCompensationCompleted, domain.EventCompensationFailed}, DurableFact, stage, "event.TracePayload", terminal("compensation", domain.EventCompensationStarted), "recovery", "replay")
	return items
}

func producerFor(eventType domain.RunEventType) string {
	switch eventType {
	case domain.EventStageStarted, domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled,
		domain.EventTurnStarted, domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled,
		domain.EventModelStarted, domain.EventModelDelta, domain.EventModelCompleted, domain.EventModelFailed:
		return "agent/turn"
	case domain.EventModelRequestPrepared, domain.EventContextAssembled:
		return "requestcapture/contextassembly"
	case domain.EventCompactionStarted, domain.EventCompactionCompleted, domain.EventCompactionFailed:
		return "contextcompaction"
	case domain.EventToolStarted, domain.EventToolCompleted, domain.EventToolFailed:
		return "tools"
	case domain.EventRetrievalStarted, domain.EventRetrievalCompleted, domain.EventRetrievalFailed,
		domain.EventCitationResolved:
		return "agent/rag"
	case domain.EventHistorySearchStarted, domain.EventHistorySearchCompleted, domain.EventHistorySearchFailed:
		return "sessionhistory"
	case domain.EventTaskStateUpdated:
		return "taskstate"
	case domain.EventMemoryCandidateProposed, domain.EventMemoryCandidateAccepted, domain.EventMemoryCandidateRejected,
		domain.EventMemoryCandidateFailed, domain.EventMemorySyncRequested, domain.EventMemorySyncCompleted, domain.EventMemorySyncFailed:
		return "memory"
	case domain.EventVerificationRequested, domain.EventVerificationStarted, domain.EventVerificationPassed,
		domain.EventVerificationFailed, domain.EventVerificationBlocked, domain.EventVerificationStale,
		domain.EventRunRevisionRequested:
		return "verification"
	case domain.EventUsageRecorded, domain.EventBudgetExceeded:
		return "budget"
	case domain.EventCheckpointCaptured, domain.EventCheckpointRestored, domain.EventCheckpointStale,
		domain.EventCompensationStarted, domain.EventCompensationCompleted, domain.EventCompensationFailed:
		return "checkpoint/recovery"
	default:
		return "agent/httpapi"
	}
}
