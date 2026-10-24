package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/progress"
	"agentflow-platform/apps/api/internal/verification"
)

// runtimeTurnModel adapts the existing executor implementations to the
// provider-neutral Turn Engine contract.
type runtimeTurnModel struct {
	runtime *Runtime
}

func (m runtimeTurnModel) Execute(ctx context.Context, request turn.Request, emit func(turn.ModelEvent)) (result turn.Result, err error) {
	run, ok, err := m.runtime.store.GetRun(request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	if !ok {
		return turn.Result{}, fmt.Errorf("run not found")
	}
	if err := m.runtime.validateProjectGuidance(ctx, run); err != nil {
		return turn.Result{}, err
	}
	if err := m.runtime.validateKnowledgeEvidence(run); err != nil {
		return turn.Result{}, err
	}
	if run.CompletionContract != nil && run.CompletionContract.Output != nil {
		if err := verification.ValidateFrozenContract(run.CompletionContract); err != nil {
			return turn.Result{}, err
		}
	}
	ctx = skill.WithAgent(ctx, request.Agent.ID, request.SkillInput)
	if request.Catalog != nil {
		ready := []string{}
		for _, name := range request.Agent.Tools {
			if _, ok := request.Catalog.ResolveReady(name); ok {
				ready = append(ready, name)
			}
		}
		ctx = skill.WithReadyTools(ctx, ready)
	}
	snapshot, err := m.runtime.snapshotForRun(request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	if snapshot.ExecutionConstraint == domain.ExecutionPlanOnly {
		request.SystemPrompt += planningGuidance
	}
	// Planner/reviewer/finalizer prompts are synthetic internal stages, not
	// independent persisted Agents. They inherit the Run's authorized owner.
	authorityAgentID := request.Agent.ID
	if authorityAgentID == "" && request.ModelMode == turn.ModelModeText {
		authorityAgentID = snapshot.Agent.ID
	}
	if _, err := m.runtime.currentRunAgent(run, authorityAgentID); err != nil {
		return turn.Result{}, err
	}
	ctx, cancel, err := m.runtime.contextWithRunBudget(ctx, request.RunID)
	if err != nil {
		return turn.Result{}, err
	}
	defer cancel()
	guard, err := m.runtime.progressGuardForRun(request.RunID, snapshot)
	if err != nil {
		return turn.Result{}, err
	}
	ctx = progress.WithGuard(ctx, guard)
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
	isolatedContext := request.Context.Isolated
	route, err := m.runtime.selectModelRoute(ctx, request, snapshot, run.CompletionContract)
	if err != nil {
		return turn.Result{}, err
	}
	assemblySnapshot := *snapshot
	if run.CompletionContract != nil && run.CompletionContract.Output != nil {
		assemblySnapshot.ContextAssembly = contextassembly.NormalizeConfig(snapshot.ContextAssembly)
		assemblySnapshot.ContextAssembly.ContextWindowTokens = min(assemblySnapshot.ContextAssembly.ContextWindowTokens, route.Route.ContextWindowTokens)
	}
	if !isolatedContext {
		m.runtime.compactContextBestEffort(ctx, request.RunID, request.ConversationID, &assemblySnapshot, contextassembly.CompactionTriggerHard, request.TurnID, route.Client)
	}
	modelCtx := ctx
	// Only user-facing output is constrained; router/decide retain their own protocols.
	if run.CompletionContract != nil && run.CompletionContract.Output != nil &&
		(snapshot.Mode == ChatModeSingle || request.Role == "finalizer" || request.Role == "final") {
		schema, schemaErr := verification.OutputSchema(run.CompletionContract)
		if schemaErr != nil {
			return turn.Result{}, schemaErr
		}
		output := run.CompletionContract.Output
		ctx = provider.WithStructuredOutput(ctx, provider.StructuredOutput{Mode: output.Mode, Version: output.Version, ContractHash: run.CompletionContract.Hash, Schema: schema})
	}
	ctx, compaction := m.withContextSession(ctx, request, &assemblySnapshot, isolatedContext)
	if request.ModelMode == turn.ModelModeText {
		result, executeErr := m.executeText(ctx, request, route.Client)
		if executeErr == nil || isolatedContext || failure.Describe(executeErr).Code != "context_length_exceeded" {
			return result, executeErr
		}
		beforeGeneration := int64(0)
		if compaction != nil {
			beforeGeneration = compaction.Generation
		}
		advanced, compactErr := m.runtime.compactContext(modelCtx, request.RunID, request.ConversationID, &assemblySnapshot, contextassembly.CompactionTriggerOverflow, request.TurnID, route.Client)
		if compactErr != nil || advanced == nil || advanced.Generation <= beforeGeneration {
			return result, executeErr
		}
		retryCtx, _ := m.withContextSession(ctx, request, &assemblySnapshot, isolatedContext)
		return m.executeText(retryCtx, request, route.Client)
	}
	schema := domain.ToolSchemaConfig{Mode: "eager"}
	if snapshot.ToolSchema != nil {
		schema = *snapshot.ToolSchema
	}
	if request.Role == "planner" {
		schema = domain.ToolSchemaConfig{Mode: "eager"}
	}
	options := m.runtime.toolExecutionOptions
	// Only server-resolved, currently ready Workspace Bindings supply logical
	// credential grants. Token bytes stay inside the MCP connection transport.
	if request.Catalog != nil {
		options.CredentialScopes = append(append([]string(nil), options.CredentialScopes...), request.Catalog.WorkspaceMCPCredentialScopes()...)
	}
	options.PlanOnly = snapshot.ExecutionConstraint == domain.ExecutionPlanOnly
	// Reviewed plans contain private file-derived content even if ordinary
	// Conversation history is trimmed from this new Run's Context.
	options.PrivateContext = options.PrivateContext || snapshot.PlanOrigin != nil || snapshot.ProjectGuidance != nil
	return m.executeStream(ctx, request, route.Client, schema, options, emit)
}

func (m runtimeTurnModel) withContextSession(ctx context.Context, request turn.Request, snapshot *domain.RuntimeSnapshot, isolatedContext bool) (context.Context, *domain.ContextCompaction) {
	history := request.History
	if isolatedContext {
		history = nil
	}
	var compaction *domain.ContextCompaction
	if !isolatedContext && snapshot.ContextAssembly.CompactionMode != contextassembly.CompactionModeOff {
		if latest, ok, loadErr := m.runtime.store.GetLatestContextCompaction(request.ConversationID); loadErr == nil && ok {
			compaction = &latest
		}
	}
	var historySearch []domain.RetrievedSessionHistory
	if !isolatedContext {
		historySearch = m.runtime.retrieveSessionHistory(ctx, request.RunID, request.ConversationID, request.Input)
	}
	session := contextassembly.Session{
		ProjectGuidance: snapshot.ProjectGuidance,
		AgentID:         request.Agent.ID,
		Config:          snapshot.ContextAssembly, Sink: request.Sink,
		History: history, CurrentInput: request.Input,
		SkillInput: request.SkillInput,
		Memories:   request.Context.Memories, Knowledge: request.Context.Chunks,
		HistorySearch: historySearch,
		Compaction:    compaction,
	}
	if snapshot.ProjectGuidance != nil {
		session.ValidateProjectGuidance = func() error {
			run, ok, err := m.runtime.store.GetRun(request.RunID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("project guidance Run is unavailable")
			}
			return m.runtime.validateProjectGuidance(ctx, run)
		}
	}
	if !isolatedContext && m.runtime.inbox != nil {
		session.LoadSteering = func() ([]domain.RunInput, error) {
			return m.runtime.inbox.ConsumeSteering(ctx, request.RunID, request.StepID, request.TurnID)
		}
	}
	if request.Role != "planner" && (len(request.Agent.Skills) > 0 || strings.HasPrefix(strings.TrimSpace(request.SkillInput), "/skill:")) {
		if bound, err := skill.Bound(snapshot, request.Agent.ID); err == nil {
			for _, item := range bound {
				session.Skills = append(session.Skills, skill.Metadata(item))
			}
		}
		session.LoadSkills = func() ([]domain.SkillSnapshot, error) { return m.runtime.skillService.Active(ctx, snapshot) }
	}
	if !isolatedContext && m.runtime.taskStates != nil {
		session.LoadTaskState = func() (domain.TaskState, bool, error) {
			return m.runtime.taskStates.Get(request.ConversationID)
		}
	}
	if len(m.runtime.knowledgeTools) > 0 {
		session.LoadKnowledgeReads = func() ([]domain.KnowledgeToolReadResult, error) {
			run, ok, err := m.runtime.store.GetRun(request.RunID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, eligibility.ErrSourceUnavailable
			}
			events, err := m.runtime.store.ListRunEvents(request.RunID)
			if err != nil {
				return nil, err
			}
			// Assembly runs again for every Tool round. Recheck even this Turn's
			// paired observations before sending a continuation to the provider.
			if err := eligibility.ValidateSources(run.RuntimeSnapshot, run.WorkspaceID, rag.RunCitationReservations(events), func(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
				return m.runtime.store.GetDocumentInWorkspace(run.WorkspaceID, id)
			}); err != nil {
				return nil, err
			}
			reads := []domain.KnowledgeToolReadResult{}
			for _, item := range events {
				// This Turn already receives its own paired Tool observations.
				if item.TurnID == request.TurnID {
					continue
				}
				if read, ok := rag.KnowledgeReadFromEvent(item); ok {
					reads = append(reads, read)
				}
			}
			return reads, nil
		}
	}
	return contextassembly.WithSession(ctx, session), compaction
}

func (m runtimeTurnModel) executeStream(ctx context.Context, request turn.Request, client provider.Client, schema domain.ToolSchemaConfig, options tool.ExecutorOptions, emit func(turn.ModelEvent)) (turn.Result, error) {
	continuation, err := decodeContinuation(request.Continuation)
	if err != nil {
		return turn.Result{}, err
	}
	options.Authorize = m.runtime.authorizeTool(request.RunID, request.Agent)
	systemPrompt := request.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = request.Agent.SystemPrompt
	}
	events, errs := toolloop.Stream(ctx, client, toolloop.Request{
		Continuation:         continuation,
		SuspendClarification: m.runtime.clarificationBoundary(request),
		AgentID:              request.Agent.ID, SchemaConfig: schema, Sink: request.Sink,
		CheckSteering: m.checkSteering(ctx, request),
		SystemPrompt:  systemPrompt, History: request.History, Latest: request.Input,
		Catalog: request.Catalog,
		Trace: provider.ChatTrace{
			Recorder: m.runtime.trace, RunID: request.RunID, StepID: request.StepID,
			Memories: request.Context.Memories, Knowledge: request.Context.Chunks,
		},
		ExecutorOptions: options,
		RunEvents:       func() ([]domain.RunEvent, error) { return m.runtime.store.ListRunEvents(request.RunID) },
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
			case "reasoning":
				emit(turn.ModelEvent{Type: turn.EventModelReasoning, Reasoning: event.Reasoning})
			case "delta", "output_display":
				if event.Reset {
					output.Reset()
				}
				output.WriteString(event.Delta)
				emit(turn.ModelEvent{Type: turn.EventModelDelta, Delta: event.Delta, Reset: event.Reset, ModelCallID: event.ModelCallID, Attempt: event.Attempt,
					DisplayText: event.DisplayText, DisplayTruncated: event.DisplayTruncated})
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				if errors.Is(err, clarification.Pending) {
					return turn.Result{StopReason: turn.StopWaiting}, err
				}
				return turn.Result{Output: output.String()}, err
			}
		}
	}

	return turn.Result{Output: output.String()}, nil
}

func (m runtimeTurnModel) checkSteering(ctx context.Context, request turn.Request) func() (bool, error) {
	if request.Context.Isolated || m.runtime.inbox == nil {
		return nil
	}
	return func() (bool, error) {
		items, err := m.runtime.inbox.ConsumeSteering(ctx, request.RunID, request.StepID, request.TurnID)
		if err != nil {
			return false, err
		}
		// Assembly records which receipts this Turn already exposed to a request.
		for _, item := range items {
			if !contextassembly.HasSteering(ctx, item.ID) {
				return true, nil
			}
		}
		return false, nil
	}
}

func (m runtimeTurnModel) executeText(ctx context.Context, request turn.Request, client provider.Client) (turn.Result, error) {
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
