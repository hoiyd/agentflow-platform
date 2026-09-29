package toolloop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
	"agentflow-platform/apps/api/internal/tool/webcitation"
)

type Request struct {
	SystemPrompt    string
	History         []domain.Message
	Latest          string
	Catalog         *tool.Catalog
	Trace           provider.ChatTrace
	ExecutorOptions tool.ExecutorOptions
	RunEvents       func() ([]domain.RunEvent, error)
}

// Stream owns the bounded model -> Tools -> observations -> model protocol.
// Model adapters only prepare context and perform individual model calls.
func Stream(ctx context.Context, model provider.ChatModel, request Request) (<-chan provider.StreamEvent, <-chan error) {
	events := make(chan provider.StreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := run(ctx, model, request, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func run(ctx context.Context, model provider.ChatModel, request Request, events chan<- provider.StreamEvent) error {
	catalog := request.Catalog
	if catalog == nil {
		var err error
		catalog, err = tool.NewCatalog()
		if err != nil {
			return err
		}
	}
	// An in-flight Turn keeps the same bindings, enabled set and security policy.
	catalog, err := catalog.CloneWith()
	if err != nil {
		return err
	}
	definitions := catalog.Definitions()
	prepared, err := model.PrepareAgentChat(ctx, provider.ChatRequest{
		SystemPrompt: request.SystemPrompt, History: request.History, Latest: request.Latest,
		ToolNames: catalog.EnabledNames(), Definitions: definitions,
	})
	if err != nil {
		return err
	}
	if !model.HasAPIKey() || len(definitions) == 0 {
		_, err = model.StreamAnswer(ctx, prepared, provider.ChatStreamAnswer, request.Trace, events)
		return err
	}
	options := request.ExecutorOptions
	options.Tracer = &executionTracer{
		delegate: eventpkg.NewToolExecutionTracer(request.Trace.Recorder, request.Trace.RunID, request.Trace.StepID),
		events:   events,
	}
	if guard := progress.FromContext(ctx); guard != nil {
		options.ProgressGuard = guard
	}
	executor := tool.NewExecutor(catalog, options)
	scope := eventpkg.ScopeFromContext(ctx)
	identity := budget.NewOperationID("call")
	if request.Trace.RunID != "" && request.Trace.StepID != "" {
		// Stage retry revisits logical slots; the Effect Journal also checks arguments.
		sum := sha256.Sum256([]byte(request.Trace.RunID + "\x00" + request.Trace.StepID))
		identity = fmt.Sprintf("call_%x", sum[:16])
	}
	rawMessages := append([]provider.Message(nil), prepared.RawMessages...)
	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		choice, err := model.SelectTools(ctx, prepared, definitions, request.Trace)
		if err != nil {
			return err
		}
		if len(choice.ToolCalls) == 0 {
			return emitText(ctx, choice.Content, events)
		}
		if _, deadline := ctx.Deadline(); !deadline && !budget.HasModelCallLimit(ctx) {
			return failure.New(failure.Definition{Message: "Tool loop requires a finite model-call/token budget or an active deadline", Info: failure.Info{Code: "tool_loop_unbounded", Source: "toolloop", Category: failure.CategoryValidation}})
		}
		if err := validateCalls(choice.ToolCalls); err != nil {
			return err
		}
		calls := append([]provider.ToolCall(nil), choice.ToolCalls...)
		requests := make([]tool.ExecutionRequest, len(calls))
		for index := range calls {
			calls[index].ID = fmt.Sprintf("%s_%d_%d", identity, round, index+1)
			requests[index] = tool.ExecutionRequest{
				CallID: calls[index].ID, RunID: request.Trace.RunID, StageID: request.Trace.StepID, TurnID: scope.TurnID,
				Tool: calls[index].Function.Name, Arguments: json.RawMessage(calls[index].Function.Arguments),
			}
		}
		rawMessages = append(rawMessages, provider.Message{
			Role: "assistant", Content: choice.Content, ToolCalls: calls,
			ReasoningContent: choice.ReasoningContent,
			Source:           contextassembly.SourceToolCall, ReferenceID: calls[0].ID,
		})
		results := executor.ExecuteBatch(ctx, requests)
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, result := range results {
			if err := terminalToolError(catalog, result); err != nil {
				return err
			}
		}
		if err := labelWebSources(request.RunEvents, results); err != nil {
			return err
		}
		for _, result := range results {
			rawMessages = append(rawMessages, provider.Message{
				Role: "tool", ToolCallID: result.CallID, Content: marshalResult(result),
				Source: contextassembly.SourceToolResult, ReferenceID: result.CallID,
			})
		}
		prepared, err = model.PrepareFollowup(ctx, rawMessages, definitions)
		if err != nil {
			return err
		}
	}
}

func validateCalls(calls []provider.ToolCall) error {
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if ids[call.ID] || strings.TrimSpace(call.ID) == "" || call.Type != "function" || strings.TrimSpace(call.Function.Name) == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return failure.New(failure.Definition{Message: "model returned an invalid Tool-call batch", Info: failure.Info{Code: "tool_call_protocol_invalid", Source: "toolloop", Category: failure.CategoryValidation}})
		}
		ids[call.ID] = true
	}
	return nil
}

func terminalToolError(catalog *tool.Catalog, result tool.ExecutionResult) error {
	if result.Error == nil {
		return nil
	}
	if exceeded, ok := budget.AsExceeded(result.Error); ok {
		return exceeded
	}
	blockedBeforeExecution := result.ProgressDecision != nil && result.ProgressDecision.Action == progress.ActionBlockCall
	if binding, ok := catalog.Resolve(result.Tool); ok && binding.Descriptor.SideEffect.RequiresJournal() && result.PolicyDecision != nil && result.PolicyDecision.Allowed && !blockedBeforeExecution {
		// A handler can return a validation-style error after starting a write.
		// Its journal uncertainty still takes precedence over model correction.
		return result.Error
	}
	switch result.Error.Code {
	case tool.ErrorBudgetExceeded, tool.ErrorNoProgress, tool.ErrorExecutionCanceled, tool.ErrorEffectReconciliation, tool.ErrorEffectJournal, tool.ErrorSecurityAudit, tool.ErrorIdempotencyRequired:
		return result.Error
	}
	return nil
}

func labelWebSources(load func() ([]domain.RunEvent, error), results []tool.ExecutionResult) error {
	if load != nil {
		needsCatalog := false
		for _, result := range results {
			needsCatalog = needsCatalog || result.Tool == "web_search" && result.Error == nil && !result.Truncated
		}
		if needsCatalog {
			runEvents, err := load()
			if err != nil {
				return fmt.Errorf("load web source catalog: %w", err)
			}
			catalog := webcitation.FromEvents(runEvents)
			for index := range results {
				result := &results[index]
				if result.Tool != "web_search" || result.Error != nil || result.Truncated {
					continue
				}
				if !catalog.HasCall(result.CallID) {
					return fmt.Errorf("web source catalog missing completed call %s", result.CallID)
				}
				result.Result, err = catalog.Relabel(result.CallID, result.Result)
				if err != nil {
					return fmt.Errorf("label web sources: %w", err)
				}
			}
		}
	}
	return nil
}

func emitText(ctx context.Context, text string, events chan<- provider.StreamEvent) error {
	if strings.TrimSpace(text) == "" {
		return failure.New(failure.Definition{Message: "model returned an empty final answer", Info: failure.Info{Code: "invalid_response", Source: "model", Category: failure.CategoryValidation}})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case events <- provider.StreamEvent{Type: "delta", Delta: text}:
		return nil
	}
}

func marshalResult(result tool.ExecutionResult) string {
	data, err := json.Marshal(result)
	if err != nil {
		message, _ := redaction.Text(err.Error())
		return fmt.Sprintf(`{"tool":%q,"error":%q}`, result.Tool, message)
	}
	redacted, _, err := redaction.JSON(data)
	if err != nil {
		return fmt.Sprintf(`{"tool":%q,"error":"tool result redaction failed"}`, result.Tool)
	}
	return string(redacted)
}

type executionTracer struct {
	delegate tool.ExecutionTracer
	events   chan<- provider.StreamEvent
}

func (t *executionTracer) ToolStarted(ctx context.Context, request tool.ExecutionRequest) {
	if t.delegate != nil {
		t.delegate.ToolStarted(ctx, request)
	}
	log.Printf("tool_call_start id=%s tool=%s arguments_bytes=%d arguments_hash=%s", request.CallID, request.Tool, len(request.Arguments), request.ArgumentsHash)
	select {
	case <-ctx.Done():
	case t.events <- provider.StreamEvent{Type: "tool_start", ToolName: request.Tool, ToolCallID: request.CallID}:
	}
}

func (t *executionTracer) ToolFinished(ctx context.Context, result tool.ExecutionResult) {
	if t.delegate != nil {
		t.delegate.ToolFinished(ctx, result)
	}
	select {
	case <-ctx.Done():
	case t.events <- provider.StreamEvent{Type: "tool_end", ToolName: result.Tool, ToolCallID: result.CallID, Error: result.ErrorMessage()}:
	}
	status := "tool_end"
	if result.Error != nil {
		status = "tool_error"
	}
	encodedResult, _ := json.Marshal(result.Result)
	log.Printf("tool_call_end id=%s tool=%s status=%s duration_ms=%d arguments_hash=%s result_bytes=%d error=%q",
		result.CallID, result.Tool, status, result.DurationMS, result.ArgumentsHash, len(encodedResult), result.ErrorMessage())
}

func (t *executionTracer) ToolPolicyEvaluated(ctx context.Context, request tool.ExecutionRequest, decision policy.Decision) error {
	delegate, ok := t.delegate.(tool.PolicyDecisionTracer)
	if !ok {
		return errors.New("durable Tool policy tracer is unavailable")
	}
	return delegate.ToolPolicyEvaluated(ctx, request, decision)
}

func (t *executionTracer) ToolProgressEvaluated(ctx context.Context, request tool.ExecutionRequest, decision progress.Decision) {
	if delegate, ok := t.delegate.(tool.ProgressDecisionTracer); ok {
		delegate.ToolProgressEvaluated(ctx, request, decision)
	}
}
