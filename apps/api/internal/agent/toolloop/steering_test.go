package toolloop

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
)

func TestSteeringAtFinalAnswerBoundary(t *testing.T) {
	model := &modelStub{selected: provider.ChatChoice{Content: "old answer"}}
	checks := 0
	events, errs := Stream(boundedContext(t), model, Request{Latest: "Question", Catalog: tool.DefaultCatalog(), CheckSteering: func() (bool, error) { checks++; return checks == 1, nil }})
	var output string
	reset := false
	for event := range events {
		if event.Reset {
			output = ""
			reset = true
		}
		output += event.Delta
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !reset || checks != 2 || output != "2" || model.final[len(model.final)-1].Content != "old answer" {
		t.Fatalf("final boundary lost: output=%q checks=%d reset=%v messages=%+v", output, checks, reset, model.final)
	}
}

func TestSteeringContinuationRequiresBoundAndPropagatesFailure(t *testing.T) {
	inboxFailure := errors.New("inbox unavailable")
	checks := 0
	for _, test := range []struct {
		name  string
		check func() (bool, error)
		code  string
		want  error
	}{
		{name: "unbounded", check: func() (bool, error) { checks++; return checks == 1, nil }, code: "tool_loop_unbounded"},
		{name: "load failure", check: func() (bool, error) { return false, inboxFailure }, want: inboxFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &modelStub{selected: provider.ChatChoice{Content: "answer"}}
			events, errs := Stream(context.Background(), model, Request{Latest: "question", CheckSteering: test.check})
			for range events {
			}
			err := <-errs
			if test.want != nil && !errors.Is(err, test.want) || test.code != "" && failure.Describe(err).Code != test.code {
				t.Fatalf("continuation guard: %v", err)
			}
			if model.final != nil {
				t.Fatal("prepared a request after rejecting continuation")
			}
		})
	}
}
