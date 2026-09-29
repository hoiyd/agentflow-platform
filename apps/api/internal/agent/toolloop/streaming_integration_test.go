package toolloop_test

import (
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

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/tool"
)

func TestToolLoopStreamsBeforeProviderDone(t *testing.T) {
	for _, useTool := range []bool{false, true} {
		t.Run(fmt.Sprintf("tool_used=%t", useTool), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			continueAnswer := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(continueAnswer) })
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input wireRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || !input.Stream || len(input.Tools) == 0 {
					t.Errorf("Tool round must be a real stream with schemas: %#v err=%v", input, err)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				round := requests.Add(1)
				if useTool && round == 1 {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checking the calculation\"}}]}\n\n")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private continuation\",\"tool_calls\":[{\"index\":0,\"id\":\"upstream-id\",\"type\":\"function\",\"function\":{\"name\":\"calculator\",\"arguments\":\"{\\\"expression\\\":\"}}]}}]}\n\n")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"1 + 1\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if useTool {
					last := input.Messages[len(input.Messages)-1]
					assistant := input.Messages[len(input.Messages)-2]
					if last.Role != "tool" || !strings.Contains(last.Content, `"value":2`) || assistant.ReasoningContent == nil || *assistant.ReasoningContent != "private continuation" || last.ToolCallID != assistant.ToolCalls[0].ID {
						t.Errorf("follow-up lost Tool/continuation protocol: %#v", input.Messages)
					}
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
			defer server.Close()
			client := openai.NewClientWithTimeout("fixture-not-a-secret", server.URL, "fixture-model", 2*time.Second)
			client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
			events, errs := toolloop.Stream(ctx, client, toolloop.Request{Latest: "calculate 1 + 1", Catalog: tool.DefaultCatalog()})
			var output strings.Builder
			resets, deltas := 0, 0
			for item := range events {
				// Read serialized events so the test also checks the optional reset flag.
				encoded, _ := json.Marshal(item)
				var wire map[string]any
				_ = json.Unmarshal(encoded, &wire)
				if wire["Reset"] == true {
					output.Reset()
					resets++
				}
				if item.Type == "delta" {
					output.WriteString(item.Delta)
					if item.Delta == "The answer" {
						deltas++
						release.Do(func() { close(continueAnswer) })
					}
				}
			}
			if err := <-errs; err != nil || output.String() != "The answer is 2" || deltas != 1 || requests.Load() != int32(1+boolInt(useTool)) || resets != boolInt(useTool) {
				t.Fatalf("output=%q early_deltas=%d resets=%d requests=%d err=%v", output.String(), deltas, resets, requests.Load(), err)
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
