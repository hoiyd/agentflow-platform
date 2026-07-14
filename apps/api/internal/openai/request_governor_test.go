package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
)

func TestRequestGovernorSlotIsHeldUntilResponseBodyCloses(t *testing.T) {
	client := NewClient("test-key", "https://provider.example/v1", "test-model")
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}
	client.SetRequestGovernor(concurrency.NewModelGovernor(concurrency.ModelOptions{
		MaxConcurrent: 1,
	}))
	first, err := client.doPathRequest(context.Background(), "https://provider.example/v1", "/request", []byte(`{}`))
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	type responseResult struct {
		response *http.Response
		err      error
	}
	second := make(chan responseResult, 1)
	go func() {
		response, requestErr := client.doPathRequest(context.Background(), "https://provider.example/v1", "/request", []byte(`{}`))
		second <- responseResult{response: response, err: requestErr}
	}()

	select {
	case result := <-second:
		if result.response != nil {
			_ = result.response.Body.Close()
		}
		t.Fatal("second request acquired before the first response body closed")
	case <-time.After(30 * time.Millisecond):
	}

	if err := first.Body.Close(); err != nil {
		t.Fatalf("close first response: %v", err)
	}
	select {
	case result := <-second:
		if result.err != nil {
			t.Fatalf("second request: %v", result.err)
		}
		_ = result.response.Body.Close()
	case <-time.After(time.Second):
		t.Fatal("second request did not acquire after response body closed")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
