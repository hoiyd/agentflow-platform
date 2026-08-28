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
		r.compactContextBestEffort(context.Background(), run.ID, run.ConversationID, snapshot, contextassembly.CompactionTriggerSoft, "")
	}()
}

func (r *Runtime) compactContextBestEffort(ctx context.Context, runID, conversationID string, snapshot *domain.RuntimeSnapshot, trigger string, triggerKey string) *domain.ContextCompaction {
	compaction, err := r.compactContext(ctx, runID, conversationID, snapshot, trigger, triggerKey)
	if err != nil {
		log.Printf("context_compaction_failed run_id=%s trigger=%s error=%q", runID, trigger, err.Error())
		return nil
	}
	if compaction != nil {
		log.Printf("context_compaction_completed run_id=%s trigger=%s compaction_id=%s generation=%d before_tokens=%d after_tokens=%d", runID, trigger, compaction.ID, compaction.Generation, compaction.BeforeTokens, compaction.AfterTokens)
	}
	return compaction
}

func (r *Runtime) compactContext(ctx context.Context, runID, conversationID string, snapshot *domain.RuntimeSnapshot, trigger string, triggerKey string) (*domain.ContextCompaction, error) {
	if r == nil || r.contextCompactor == nil || snapshot == nil || conversationID == "" {
		return nil, nil
	}
	if snapshot.ContextAssembly.CompactionMode == contextassembly.CompactionModeOff {
		return nil, nil
	}
	timeout := time.Duration(snapshot.ContextAssembly.CompactionTimeoutMS) * time.Millisecond
	compactCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	compactCtx = eventpkg.WithScope(compactCtx, eventpkg.Scope{RunID: runID, ConversationID: conversationID})

	var summarizer contextcompaction.Summarizer
	if r.modelClient.HasAPIKey() {
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
		RunID: runID, ConversationID: conversationID, Trigger: trigger, TriggerKey: triggerKey,
		ObservedPromptTokens: observedPromptTokens,
		Config:               snapshot.ContextAssembly, Summarizer: summarizer,
	})
	return compaction, err
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
