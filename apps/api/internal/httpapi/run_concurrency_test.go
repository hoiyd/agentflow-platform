package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

func TestReserveRunReturnsRetryAfterWhenFull(t *testing.T) {
	controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1, QueueSize: 0, WaitTimeout: time.Second})
	occupied, _ := controller.Reserve()
	release, err := occupied.Start(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("occupy controller: %v", err)
	}
	defer release()

	handler := &Handler{store: fixturestore.New(), runController: controller}
	recorder := httptest.NewRecorder()
	handler.chat(recorder, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"queued task"}`)))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") != overloadRetryAfterSeconds {
		t.Fatalf("expected Retry-After header, got %q", recorder.Header().Get("Retry-After"))
	}
}

func TestStartRunReturnsServiceUnavailableAfterQueueTimeout(t *testing.T) {
	controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1, QueueSize: 1, WaitTimeout: 10 * time.Millisecond})
	occupied, _ := controller.Reserve()
	release, err := occupied.Start(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("occupy controller: %v", err)
	}
	defer release()

	handler := &Handler{store: fixturestore.New(), runController: controller}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"queued task"}`))
	handler.chat(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") != overloadRetryAfterSeconds {
		t.Fatalf("expected Retry-After header, got %q", recorder.Header().Get("Retry-After"))
	}
}
