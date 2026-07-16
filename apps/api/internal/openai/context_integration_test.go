package openai

import (
	"context"
	"net/http"
	"sync"
	"testing"

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
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{
		ConversationID: "conv-1", RunID: "run-1", StageID: "stage-1", TurnID: "turn-1",
	})
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
