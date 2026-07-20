package agent

import (
	"context"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/turn"
)

// runtimeTurnModel adapts the existing executor implementations to the
// provider-neutral Turn Engine contract.
type runtimeTurnModel struct {
	runtime *Runtime
}

func (m runtimeTurnModel) Execute(ctx context.Context, request turn.Request, emit func(turn.ModelEvent)) (result turn.Result, err error) {
	snapshot, err := m.runtime.snapshotForRun(request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	ctx, cancel, err := m.runtime.contextWithRunBudget(ctx, request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	defer cancel()
	if request.Role == "router" {
		ctx = budget.WithPurpose(ctx, domain.UsagePurposeRouter)
	} else {
		ctx = budget.WithPurpose(ctx, domain.UsagePurposePrimary)
	}
	defer func() {
		if err != nil {
			err = runBudgetCause(ctx, err)
		}
	}()
	m.runtime.compactContextBestEffort(ctx, request.RunID, request.ConversationID, snapshot, contextassembly.CompactionTriggerHard)
	var compaction *domain.ContextCompaction
	if snapshot.ContextAssembly.CompactionMode != contextassembly.CompactionModeOff {
		if latest, ok, loadErr := m.runtime.store.GetLatestContextCompaction(request.ConversationID); loadErr == nil && ok {
			compaction = &latest
		}
	}
	ctx = contextassembly.WithSession(ctx, contextassembly.Session{
		Config: snapshot.ContextAssembly, Sink: request.Sink,
		History: request.History, CurrentInput: request.Input,
		Memories: request.Context.Memories, Knowledge: request.Context.Chunks,
		Compaction: compaction,
	})
	if request.ModelMode == turn.ModelModeText {
		return m.executeText(ctx, request)
	}
	client, err := m.runtime.clientForRun(request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	executor := m.runtime.executorFor(request.ExecutorKind, client)
	events, errs := executor.Stream(ctx, ExecutorInput{
		Agent:             request.Agent,
		History:           request.History,
		Latest:            request.Input,
		Catalog:           request.Catalog,
		RunID:             request.RunID,
		StepID:            request.StepID,
		RetrievedMemories: request.Context.Memories,
		RetrievedChunks:   request.Context.Chunks,
	})

	var output strings.Builder
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return turn.Result{Output: output.String()}, ctx.Err()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch event.Type {
			case "delta":
				output.WriteString(event.Delta)
				emit(turn.ModelEvent{Type: turn.EventModelDelta, Delta: event.Delta})
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return turn.Result{Output: output.String()}, err
			}
		}
	}

	return turn.Result{Output: output.String()}, nil
}

func (m runtimeTurnModel) executeText(ctx context.Context, request turn.Request) (turn.Result, error) {
	payload := map[string]any{
		"role": request.Role, "agent_id": request.Agent.ID, "system": request.SystemPrompt,
		"input": request.Input, "input_chars": len(request.Input),
	}
	for key, value := range request.Metadata {
		payload[key] = value
	}
	for key, value := range retrievalTracePayload(request.Context.Memories, request.Context.Chunks) {
		payload[key] = value
	}
	client, err := m.runtime.clientForRun(request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	prepared, err := client.PrepareText(ctx, request.SystemPrompt, request.Input)
	if err != nil {
		return turn.Result{}, err
	}
	if prepared.Manifest.ID != "" {
		payload["manifest_id"] = prepared.Manifest.ID
		payload["model_call_id"] = prepared.Manifest.ModelCallID
		payload["context_estimated_input_tokens"] = prepared.Manifest.EstimatedInputTokens
	}
	ctx = budget.WithOperation(ctx, prepared.Manifest.ModelCallID)
	span := m.runtime.trace.LLMStart(ctx, request.RunID, request.StepID, payload)
	startedAt := time.Now()
	completion, err := client.CompletePreparedText(ctx, prepared)
	if err != nil {
		m.runtime.trace.Error(ctx, request.RunID, request.StepID, map[string]any{
			"source": "llm", "role": request.Role, "agent_id": request.Agent.ID, "error": err.Error(),
		})
		return turn.Result{}, err
	}
	m.runtime.trace.LLMEnd(ctx, span, map[string]any{
		"role": request.Role, "agent_id": request.Agent.ID, "model": completion.Model,
		"output": completion.Text, "output_chars": len(completion.Text),
		"duration_ms":   time.Since(startedAt).Milliseconds(),
		"prompt_tokens": completion.Usage.PromptTokens, "completion_tokens": completion.Usage.CompletionTokens,
		"total_tokens": completion.Usage.TotalTokens, "token_usage_estimated": completion.Usage.Estimated,
	})
	return turn.Result{Output: completion.Text, Usage: turn.Usage{
		Model: completion.Model, PromptTokens: completion.Usage.PromptTokens,
		CompletionTokens: completion.Usage.CompletionTokens, TotalTokens: completion.Usage.TotalTokens,
		Estimated: completion.Usage.Estimated,
	}}, nil
}
