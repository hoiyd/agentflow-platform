package openai

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tools"
)

func TestToolRoundTripCreatesManifestPerLogicalModelCall(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"1 + 1\"}"}}]}}]}`), nil
		}
		return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"2\"}}]}\n\ndata: [DONE]\n\n"), nil
	})}
	store := &recordingEventStore{}
	recorder := eventpkg.NewRecorder(store)
	budgetController := &modelBudgetController{}
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{
		ConversationID: "conv-1", RunID: "run-1", StageID: "stage-1", TurnID: "turn-1",
	})
	ctx = budget.WithController(ctx, budgetController)
	ctx = contextassembly.WithSession(ctx, contextassembly.Session{
		Config: contextassembly.DefaultConfig(), Sink: eventpkg.StoreSink{Store: store}, CurrentInput: "calculate 1 + 1",
	})
	history := []domain.Message{{ID: "message-1", Role: "user", Content: "calculate 1 + 1"}}

	events, errs := client.StreamAgentChatWithToolsTrace(
		ctx, "You calculate.", history, "calculate 1 + 1", tools.DefaultCatalog(), recorder,
		"run-1", "stage-1", nil, nil,
	)
	var output string
	for item := range events {
		output += item.Delta
	}
	if err := <-errs; err != nil {
		t.Fatalf("stream agent chat: %v", err)
	}
	if output != "2" || attempts != 2 {
		t.Fatalf("unexpected tool round trip: output=%q attempts=%d", output, attempts)
	}

	items := store.items()
	if countEvents(items, domain.EventContextAssembled) != 2 || countEvents(items, domain.EventModelStarted) != 2 || countEvents(items, domain.EventModelCompleted) != 2 {
		t.Fatalf("expected two logical model call lifecycles, got %#v", items)
	}
	manifestIDs := map[string]bool{}
	for _, item := range items {
		if item.Type == domain.EventModelStarted {
			id, _ := item.Payload["manifest_id"].(string)
			if id == "" {
				t.Fatalf("model.started is missing manifest_id: %#v", item)
			}
			manifestIDs[id] = true
		}
	}
	if len(manifestIDs) != 2 {
		t.Fatalf("expected distinct manifest ids, got %#v", manifestIDs)
	}
	if budgetController.begins != 2 || budgetController.settles != 2 || len(budgetController.estimates) != 2 {
		t.Fatalf("expected two settled logical model calls, controller=%#v", budgetController)
	}
	if budgetController.estimates[0].OperationID == budgetController.estimates[1].OperationID {
		t.Fatalf("tool selection and final response reused operation id %q", budgetController.estimates[0].OperationID)
	}
}

func TestToolStreamReturnsDirectModelAnswer(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return modelHTTPResponse(200, `{
			"choices":[{"message":{"role":"assistant","content":"direct answer"}}],
			"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}
		}`), nil
	})}

	events, errs := client.StreamAgentChatWithToolsTrace(
		context.Background(), "Answer directly when no tool is needed.", nil, "say hello", tools.DefaultCatalog(), nil, "run-1", "stage-1",
		[]domain.RetrievedMemory{{Memory: domain.Memory{ID: "memory-1", Content: "Remember the preferred greeting."}, Score: 0.9}},
		[]domain.RetrievedDocumentChunk{{Chunk: domain.DocumentChunk{ID: "chunk-1", Content: "A grounded greeting."}, Score: 0.8}},
	)
	var output strings.Builder
	for event := range events {
		if event.Type == "delta" {
			output.WriteString(event.Delta)
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("stream direct answer: %v", err)
	}
	if output.String() != "direct answer" || attempts != 1 {
		t.Fatalf("unexpected direct answer: output=%q attempts=%d", output.String(), attempts)
	}
}

func TestToolStreamFallsBackToJSONToolCallAndSummary(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		switch attempts {
		case 1:
			return modelHTTPResponse(400, `{"error":{"message":"tool_choice is not supported","code":"invalid_request_error"}}`), nil
		case 2:
			return modelHTTPResponse(200, `{
				"choices":[{"message":{"role":"assistant","content":"{\"action\":\"tool_call\",\"tool\":\"calculator\",\"arguments\":{\"expression\":\"2 + 3\"}}"}}]
			}`), nil
		default:
			return modelHTTPResponse(200, "data: [DONE]\n\n"), nil
		}
	})}

	events, errs := client.StreamAgentChatWithTools(
		context.Background(), "Use the calculator when needed.", nil, "calculate 2 + 3", tools.DefaultCatalog(),
	)
	var output strings.Builder
	var started, finished bool
	for event := range events {
		switch event.Type {
		case "delta":
			output.WriteString(event.Delta)
		case "tool_start":
			started = event.ToolName == "calculator"
		case "tool_end":
			finished = event.ToolName == "calculator" && event.Error == ""
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("stream capability fallback: %v", err)
	}
	if attempts != 3 || !started || !finished || output.String() != "Tool execution completed." {
		t.Fatalf("unexpected capability fallback: attempts=%d started=%t finished=%t output=%q", attempts, started, finished, output.String())
	}
}

func TestToolStreamReturnsTerminalSelectionError(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return modelHTTPResponse(401, `{"error":{"message":"invalid API key","code":"invalid_api_key"}}`), nil
	})}

	events, errs := client.StreamAgentChatWithTools(
		context.Background(), "Use tools when needed.", nil, "calculate 2 + 3", tools.DefaultCatalog(),
	)
	for range events {
	}
	modelErr, ok := AsModelError(<-errs)
	if !ok || modelErr.Kind != ErrorAuthentication || attempts != 1 {
		t.Fatalf("expected terminal authentication error: attempts=%d err=%#v", attempts, modelErr)
	}
}

func TestToolStreamReturnsCapabilityFallbackError(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(400, `{"error":{"message":"tool_choice is not supported","code":"invalid_request_error"}}`), nil
		}
		return modelHTTPResponse(401, `{"error":{"message":"invalid API key","code":"invalid_api_key"}}`), nil
	})}

	events, errs := client.StreamAgentChatWithTools(
		context.Background(), "Use tools when needed.", nil, "calculate 2 + 3", tools.DefaultCatalog(),
	)
	for range events {
	}
	modelErr, ok := AsModelError(<-errs)
	if !ok || modelErr.Kind != ErrorAuthentication || attempts != 2 {
		t.Fatalf("expected capability fallback error: attempts=%d err=%#v", attempts, modelErr)
	}
}

func TestToolStreamReturnsFinalStreamError(t *testing.T) {
	client := retryTestClient()
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(200, `{
				"choices":[{"message":{"role":"assistant","tool_calls":[{
					"id":"call-1","type":"function","function":{"name":"calculator","arguments":"{\"expression\":\"2 + 3\"}"}
				}]}}]
			}`), nil
		}
		return modelHTTPResponse(401, `{"error":{"message":"invalid API key","code":"invalid_api_key"}}`), nil
	})}

	events, errs := client.StreamAgentChatWithTools(
		context.Background(), "Use the calculator when needed.", nil, "calculate 2 + 3", tools.DefaultCatalog(),
	)
	for range events {
	}
	modelErr, ok := AsModelError(<-errs)
	if !ok || modelErr.Kind != ErrorAuthentication || attempts != 2 {
		t.Fatalf("expected final stream error: attempts=%d err=%#v", attempts, modelErr)
	}
}

type recordingEventStore struct {
	mu     sync.Mutex
	events []domain.RunEvent
}

func (s *recordingEventStore) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Sequence = int64(len(s.events) + 1)
	item.SchemaVersion = domain.CurrentRunEventSchemaVersion
	s.events = append(s.events, item)
	return item, nil
}

func (s *recordingEventStore) items() []domain.RunEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.RunEvent(nil), s.events...)
}

func countEvents(events []domain.RunEvent, eventType domain.RunEventType) int {
	count := 0
	for _, item := range events {
		if item.Type == eventType {
			count++
		}
	}
	return count
}
