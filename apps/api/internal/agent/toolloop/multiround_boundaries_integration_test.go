package toolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
)

func TestMultiRoundFrozenToolsIgnoreLiveCatalogChanges(t *testing.T) {
	var live *tool.Catalog
	var unexpected atomic.Int32
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, input wireRequest) any {
		if round == 1 {
			if err := live.SetEnabled("calculator", false); err != nil {
				t.Error(err)
			}
			if err := live.Register(tool.Binding{Descriptor: tool.Descriptor{Name: "new_tool", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { unexpected.Add(1); return "unauthorized", nil }}); err != nil {
				t.Error(err)
			}
			return call("calculator", `{"expression":"1 + 1"}`)
		}
		for _, definition := range input.Tools {
			encoded, _ := json.Marshal(definition)
			if strings.Contains(string(encoded), "new_tool") {
				t.Error("live Tool leaked into frozen definitions")
			}
		}
		last := input.Messages[len(input.Messages)-1]
		if round == 2 {
			if !strings.Contains(last.Content, `"value":2`) {
				t.Error("live disable changed the active Turn")
			}
			return call("new_tool", `{}`)
		}
		if !strings.Contains(last.Content, "tool_not_found") {
			t.Error("new Tool gained execution authority")
		}
		return answer("authority unchanged")
	})
	live = f.request.Catalog
	output, err := f.execute()
	if err != nil || output != "authority unchanged" || unexpected.Load() != 0 {
		t.Fatalf("output=%q unauthorized=%d err=%v", output, unexpected.Load(), err)
	}
}

func TestMultiRoundReadTimeoutIsAnObservation(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 3}, func(round int, input wireRequest) any {
		if round == 1 {
			return call("slow_read", `{}`)
		}
		if !strings.Contains(input.Messages[len(input.Messages)-1].Content, "execution_timeout") {
			t.Error("timeout observation missing")
		}
		return answer("source timed out")
	})
	catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "slow_read", Parameters: tool.ObjectSchema(nil, nil)}, Policy: tool.ExecutionPolicy{Timeout: 10 * time.Millisecond}, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) { <-ctx.Done(); return nil, ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	output, err := f.execute()
	if err != nil || output != "source timed out" || f.requests.Load() != 2 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}

func TestMultiRoundDeadlineBoundsCallsWithoutQuantityLimit(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{}, func(round int, _ wireRequest) any {
		if round == 1 {
			return call("calculator", `{"expression":"1 + 1"}`)
		}
		return answer("2")
	})
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	f.ctx = ctx
	if output, err := f.execute(); err != nil || output != "2" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	// An expired enclosing deadline must not issue another provider request.
	cancel()
	if _, err := f.execute(); !errors.Is(err, context.Canceled) || f.requests.Load() != 2 {
		t.Fatalf("requests=%d err=%v", f.requests.Load(), err)
	}
}

func TestMultiRoundWebSourcesKeepRunScopedIdentities(t *testing.T) {
	var searches atomic.Int32
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, input wireRequest) any {
		if round > 1 {
			want := `"source_id":"W1"`
			if round == 3 {
				want = `"source_id":"W2"`
			}
			if !strings.Contains(input.Messages[len(input.Messages)-1].Content, want) {
				t.Errorf("round=%d lost citation identity %s", round, want)
			}
		}
		if round < 3 {
			return call("web_search", `{}`)
		}
		return answer("Evidence from [W1] and [W2].")
	})
	catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "web_search", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) {
		url := "https://example.com/first"
		if searches.Add(1) == 2 {
			url = "https://example.com/second"
		}
		return map[string]any{"results": []any{map[string]any{"source_id": "W1", "title": "Evidence", "url": url}}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	output, err := f.execute()
	if err != nil || output != "Evidence from [W1] and [W2]." || searches.Load() != 2 {
		t.Fatalf("output=%q searches=%d err=%v", output, searches.Load(), err)
	}
}

func TestMultiRoundHistoryClippingPreservesToolPairs(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, input wireRequest) any {
		for index, message := range input.Messages {
			if strings.Contains(message.Content, "old-irrelevant-history") {
				t.Error("oversized history was not clipped")
			}
			if message.Role == "tool" && (index == 0 || len(input.Messages[index-1].ToolCalls) != 1 || input.Messages[index-1].ToolCalls[0].ID != message.ToolCallID) {
				t.Error("clipping broke call/result pairing")
			}
		}
		if round < 3 {
			return call("calculator", `{"expression":"1 + 1"}`)
		}
		return answer("paired")
	})
	config := contextassembly.DefaultConfig()
	config.HistoryMaxTokens = 20
	f.ctx = contextassembly.WithSession(f.ctx, contextassembly.Session{Config: config, Sink: eventpkg.StoreSink{Store: f.store}, CurrentInput: "solve using tools"})
	f.request.History = []domain.Message{{Role: "user", Content: strings.Repeat("old-irrelevant-history ", 1000)}}
	output, err := f.execute()
	if err != nil || output != "paired" || f.requests.Load() != 3 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}
