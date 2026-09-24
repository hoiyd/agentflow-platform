package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

type Request struct {
	SystemPrompt    string
	History         []domain.Message
	Latest          string
	Catalog         *tool.Catalog
	Trace           provider.ChatTrace
	ExecutorOptions tool.ExecutorOptions
}

// Stream owns the bounded selection -> Tool batch -> final answer protocol.
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
	choice, err := model.SelectTools(ctx, prepared, definitions, request.Trace)
	if err != nil {
		return err
	}
	if len(choice.ToolCalls) == 0 {
		return emitText(ctx, choice.Content, events)
	}

	rawMessages := append(prepared.RawMessages, provider.Message{
		Role: "assistant", Content: choice.Content, ToolCalls: choice.ToolCalls,
		Source: contextassembly.SourceToolCall, ReferenceID: "tool_calls",
	})
	options := request.ExecutorOptions
	options.Tracer = &executionTracer{
		delegate: eventpkg.NewToolExecutionTracer(request.Trace.Recorder, request.Trace.RunID, request.Trace.StepID),
		events:   events,
	}
	options.ProgressGuard = progress.FromContext(ctx)
	executor := tool.NewExecutor(catalog, options)
	scope := eventpkg.ScopeFromContext(ctx)
	requests := make([]tool.ExecutionRequest, 0, len(choice.ToolCalls))
	for _, call := range choice.ToolCalls {
		requests = append(requests, tool.ExecutionRequest{
			CallID: call.ID, RunID: request.Trace.RunID, StageID: request.Trace.StepID, TurnID: scope.TurnID,
			Tool: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	results := executor.ExecuteBatch(ctx, requests)
	for _, result := range results {
		if exceeded, ok := budget.AsExceeded(result.Error); ok {
			return exceeded
		}
		if result.Error != nil && result.Error.Code == tool.ErrorNoProgress {
			return result.Error
		}
	}
	for index, result := range results {
		call := choice.ToolCalls[index]
		rawMessages = append(rawMessages, provider.Message{
			Role: "tool", ToolCallID: call.ID, Content: marshalResult(result),
			Source: contextassembly.SourceToolResult, ReferenceID: call.ID,
		})
	}
	prepared, err = model.PrepareFollowup(ctx, rawMessages)
	if err != nil {
		return err
	}
	emitted, err := model.StreamAnswer(ctx, prepared, provider.ChatStreamToolResult, request.Trace, events)
	if err != nil {
		return err
	}
	if !emitted {
		log.Printf("chat_fallback mode=tool_summary_no_stream tool_count=%d", len(results))
		return emitText(ctx, "Tool execution completed.", events)
	}
	return nil
}

func emitText(ctx context.Context, text string, events chan<- provider.StreamEvent) error {
	if strings.TrimSpace(text) == "" {
		text = "I do not have a response yet."
	}
	for _, part := range strings.SplitAfter(text, " ") {
		if part == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- provider.StreamEvent{Type: "delta", Delta: part}:
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
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
