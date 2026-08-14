package agent

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

func (r *Runtime) scheduleSoftContextCompaction(run domain.Run) {
	go func() {
		snapshot, err := r.snapshotForRun(run.ID)
		if err != nil {
			return
		}
		r.compactContextBestEffort(context.Background(), run.ID, run.ConversationID, snapshot, contextassembly.CompactionTriggerSoft)
	}()
}

func (r *Runtime) compactContextBestEffort(ctx context.Context, runID, conversationID string, snapshot *domain.RuntimeSnapshot, trigger string) {
	if r == nil || r.contextCompactor == nil || snapshot == nil || conversationID == "" {
		return
	}
	if snapshot.ContextAssembly.CompactionMode == contextassembly.CompactionModeOff {
		return
	}
	timeout := time.Duration(snapshot.ContextAssembly.CompactionTimeoutMS) * time.Millisecond
	compactCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	compactCtx = eventpkg.WithScope(compactCtx, eventpkg.Scope{RunID: runID, ConversationID: conversationID})

	var summarizer contextcompaction.Summarizer
	if r.openAI.HasAPIKey() {
		summarizer = contextcompaction.SummarizerFunc(func(ctx context.Context, request contextcompaction.SummaryRequest) (contextcompaction.SummaryResult, error) {
			ctx = budget.WithPurpose(ctx, domain.UsagePurposeCompaction)
			client, err := r.clientFromSnapshot(snapshot)
			if err != nil {
				return contextcompaction.SummaryResult{}, err
			}
			completion, err := client.CompleteTextDetailed(ctx, request.SystemPrompt, request.Prompt)
			return contextcompaction.SummaryResult{Text: completion.Text, Model: completion.Model}, err
		})
	}
	observedPromptTokens := 0
	if trigger == contextassembly.CompactionTriggerSoft {
		observedPromptTokens = r.maxObservedPromptTokens(runID)
	}
	compaction, err := r.contextCompactor.CompactIfNeeded(compactCtx, contextcompaction.Request{
		RunID: runID, ConversationID: conversationID, Trigger: trigger,
		ObservedPromptTokens: observedPromptTokens,
		Config:               snapshot.ContextAssembly, Summarizer: summarizer,
	})
	if err != nil {
		log.Printf("context_compaction_failed run_id=%s trigger=%s error=%q", runID, trigger, err.Error())
		return
	}
	if compaction != nil {
		log.Printf("context_compaction_completed run_id=%s trigger=%s compaction_id=%s before_tokens=%d after_tokens=%d", runID, trigger, compaction.ID, compaction.BeforeTokens, compaction.AfterTokens)
	}
}

func (r *Runtime) maxObservedPromptTokens(runID string) int {
	events, err := r.store.ListRunEvents(runID)
	if err != nil {
		return 0
	}
	maxTokens := 0
	for _, runEvent := range events {
		if runEvent.Type != domain.EventModelCompleted {
			continue
		}
		if tokens := payloadInt(runEvent.Payload["prompt_tokens"]); tokens > maxTokens {
			maxTokens = tokens
		}
	}
	return maxTokens
}

func payloadInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
