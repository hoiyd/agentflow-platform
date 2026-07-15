package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
)

func TestReserveRunReturnsRetryAfterWhenFull(t *testing.T) {
	controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1, QueueSize: 0, WaitTimeout: time.Second})
	occupied, _ := controller.Reserve()
	release, err := occupied.Start(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("occupy controller: %v", err)
	}
	defer release()

	handler := &Handler{runController: controller}
	recorder := httptest.NewRecorder()
	if _, ok := handler.reserveRunCapacity(recorder); ok {
		t.Fatal("expected overloaded request to be rejected")
	}
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

	queued, err := controller.Reserve()
	if err != nil {
		t.Fatalf("reserve queued run: %v", err)
	}
	handler := &Handler{runController: controller}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	if _, ok := handler.acquireRunSlot(recorder, request, queued, "conversation-2"); ok {
		t.Fatal("expected queued request to time out")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") != overloadRetryAfterSeconds {
		t.Fatalf("expected Retry-After header, got %q", recorder.Header().Get("Retry-After"))
	}
}
