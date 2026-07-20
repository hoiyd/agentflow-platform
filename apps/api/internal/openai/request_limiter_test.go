package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/modelrequest"
)

func TestRequestLimiterSlotIsHeldUntilResponseBodyCloses(t *testing.T) {
	client := NewClient("test-key", "https://provider.example/v1", "test-model")
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}
	client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{
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

func TestRequestTokenLimitIsTerminalModelError(t *testing.T) {
	client := retryTestClient()
	client.SetRequestLimiter(requestLimiterFunc(func(context.Context, string, int) (func(), error) {
		return nil, &modelrequest.TokenBucketCapacityError{EstimatedTokens: 101, Capacity: 100}
	}))
	requestSent := false
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requestSent = true
		return nil, errors.New("request should not be sent")
	})}

	_, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorRequestTokenCapacity || modelErr.Retryable || modelErr.Attempts != 1 {
		t.Fatalf("expected terminal token-budget error, got %#v", modelErr)
	}
	if requestSent {
		t.Fatal("model request was sent after local token-budget rejection")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
