package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
)

func TestChatHTTPStreamsToolAnswerBeforeProviderDone(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	continueAnswer := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(continueAnswer) })
	var rounds atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream   bool               `json:"stream"`
			Tools    []any              `json:"tools"`
			Messages []provider.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if !request.Stream {
			writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Calculation"}, "finish_reason": "stop"}}})
			return
		}
		if len(request.Tools) == 0 {
			t.Error("chat dropped enabled Tools")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if rounds.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checking calculator\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"original\",\"type\":\"function\",\"function\":{\"name\":\"calculator\",\"arguments\":\"{\\\"expression\\\":\\\"1 + 1\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" || !strings.Contains(last.Content, `"value":2`) {
			t.Errorf("missing actual Binding result: %#v", last)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The answer\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-continueAnswer:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" is 2\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	dependencies := completeHandlerDependencies(t)
	storage := fullStoreForTest(t, dependencies)
	persona, err := storage.CreateAgent(domain.Agent{Name: "Streaming calculator", SystemPrompt: "Use calculator.", Tools: []string{"calculator"}})
	if err != nil {
		t.Fatal(err)
	}
	client := openai.NewClientWithTimeout("fixture-not-a-secret", model.URL, "fixture-model", time.Second)
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	dependencies.AgentRuntime = newRuntimeForTest(agentpkg.RuntimeOptions{Store: storage, RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 4, MaxToolCalls: 2, MaxRuntimeMS: 4000}}, client)
	handler, err := NewHandler(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(handler.Routes())
	defer api.Close()
	defer release.Do(func() { close(continueAnswer) })
	body := fmt.Sprintf(`{"mode":"single","agent_id":%q,"message":"Calculate 1 + 1"}`, persona.ID)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, api.URL+"/api/chat", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := api.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("chat status=%d", response.StatusCode)
	}
	var answer strings.Builder
	scanner := bufio.NewScanner(response.Body)
	first, resets, done := false, 0, false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "event: done" {
			done = true
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event domain.RunEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != domain.EventModelDelta {
			continue
		}
		if reset, _ := event.Payload["reset"].(bool); reset {
			answer.Reset()
			resets++
		}
		delta, _ := event.Payload["delta"].(string)
		answer.WriteString(delta)
		if delta == "The answer" {
			first = true
			release.Do(func() { close(continueAnswer) })
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !first || !done || resets != 1 || answer.String() != "The answer is 2" || rounds.Load() != 2 {
		t.Fatalf("early=%t done=%t resets=%d output=%q rounds=%d", first, done, resets, answer.String(), rounds.Load())
	}
	runs, err := storage.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunCompleted {
		t.Fatalf("runs=%#v err=%v", runs, err)
	}
	messages, err := storage.ListMessages(runs[0].ConversationID)
	if err != nil || len(messages) != 2 || messages[1].Content != answer.String() {
		t.Fatalf("persisted messages=%#v err=%v", messages, err)
	}
	t.Logf("tool_stream_evidence run=%s mode=single provider=local-http tool=calculator stream_rounds=2 early_delta=true reset_count=1 persisted_answer=%q limitations=no-live-model-or-browser", runs[0].ID, answer.String())
}
