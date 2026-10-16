package contexteval

import (
	"context"
	"strings"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/inference/provider"
)

// Only adapts the offline fixture to the existing Turn Engine/Tool loop. It
// deliberately does not emulate HTTP, orchestration modes or workspace grants.
type liveTurnModel struct {
	client  provider.Client
	fixture liveFixture
}

func (m liveTurnModel) Execute(ctx context.Context, r turn.Request, emit func(turn.ModelEvent)) (turn.Result, error) {
	events, errs := toolloop.Stream(ctx, m.client, toolloop.Request{SystemPrompt: r.SystemPrompt, History: r.History, Latest: r.Input, Catalog: r.Catalog,
		Trace: provider.ChatTrace{Recorder: m.fixture.recorder, RunID: r.RunID}, Sink: r.Sink})
	var output strings.Builder
	var executionErr error
	// Drain both channels on failure so no provider/Tool goroutine outlives the
	// fixture, especially cancellation, timeout and partial stream failures.
	for events != nil || errs != nil {
		select {
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if e.Type == "delta" {
				if e.Reset {
					output.Reset()
				}
				output.WriteString(e.Delta)
				emit(turn.ModelEvent{Type: turn.EventModelDelta, Delta: e.Delta, Reset: e.Reset, ModelCallID: e.ModelCallID, Attempt: e.Attempt})
			}
		case e, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if e != nil {
				executionErr = e
			}
		}
	}
	if executionErr == nil {
		executionErr = ctx.Err()
	}
	return turn.Result{Output: output.String()}, executionErr
}
