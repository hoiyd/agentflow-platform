package agent

import (
	"context"
	"strings"

	"agentflow-platform/apps/api/internal/turn"
)

// runtimeTurnModel adapts the existing executor implementations to the
// provider-neutral Turn Engine contract.
type runtimeTurnModel struct {
	runtime *Runtime
}

func (m runtimeTurnModel) Execute(ctx context.Context, request turn.Request, emit func(turn.ModelEvent)) (turn.Result, error) {
	executor := m.runtime.executorFor(request.ExecutorKind)
	events, errs := executor.Stream(ctx, ExecutorInput{
		Agent:             request.Agent,
		History:           request.History,
		Latest:            request.Input,
		Registry:          request.Registry,
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
			if event.Type == "delta" {
				output.WriteString(event.Delta)
				emit(turn.ModelEvent{Delta: event.Delta})
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
