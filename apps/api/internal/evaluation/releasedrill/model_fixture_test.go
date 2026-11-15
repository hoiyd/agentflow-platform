package releasedrill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type modelGate struct {
	started chan struct{}
	release chan struct{}
	delay   time.Duration
}

type modelStub struct {
	mu   sync.Mutex
	next *modelGate
}

func newModelStub() *modelStub { return &modelStub{} }

func (s *modelStub) delayNext(delay time.Duration) <-chan struct{} {
	gate := &modelGate{started: make(chan struct{}), delay: delay}
	s.mu.Lock()
	s.next = gate
	s.mu.Unlock()
	return gate.started
}

func (s *modelStub) blockNext() <-chan struct{} {
	gate := &modelGate{started: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	s.next = gate
	s.mu.Unlock()
	return gate.started
}

func (s *modelStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	gate := s.next
	s.next = nil
	s.mu.Unlock()
	if gate != nil {
		close(gate.started)
		if gate.delay > 0 {
			select {
			case <-time.After(gate.delay):
			case <-r.Context().Done():
				return
			}
		} else {
			select {
			case <-gate.release:
			case <-r.Context().Done():
				return
			}
		}
	}
	content := "The deterministic release drill stage completed with sufficient information."
	if len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "Return only valid JSON") {
		content = `{"decision":"stop","reason":"release drill evidence is complete","question":"","final_answer":"Release drill completed from durable state."}`
	}
	w.Header().Set("Content-Type", "application/json")
	if request.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":8,\"total_tokens\":20}}\n\ndata: [DONE]\n\n", content)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]int{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
	})
}

func TestModelStubBlocksOneRequestAndRecovers(t *testing.T) {
	stub := newModelStub()
	server := httptest.NewServer(stub)
	defer server.Close()

	started := stub.blockNext()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions",
			strings.NewReader(`{"messages":[{"content":"observe"}]}`))
		request.Header.Set("Content-Type", "application/json")
		_, err := http.DefaultClient.Do(request)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stub did not block the armed request")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not stop after cancellation")
	}

	response, err := http.Post(server.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"messages":[{"content":"observe"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stub did not recover: status=%d", response.StatusCode)
	}
}
