package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestModelErrorClassifiesProviderResponses(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		kind       ErrorKind
		retryable  bool
		retryAfter time.Duration
	}{
		{name: "authentication", status: 401, body: `{"error":{"message":"Invalid API key","type":"authentication_error","code":"invalid_api_key"}}`, kind: ErrorAuthentication},
		{name: "quota", status: 429, body: `{"error":{"message":"Current quota exceeded","type":"insufficient_quota","code":"insufficient_quota"}}`, kind: ErrorQuotaExceeded},
		{name: "rate limit", status: 429, body: `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`, kind: ErrorRateLimited, retryable: true, retryAfter: 2 * time.Second},
		{name: "model not found", status: 404, body: `{"error":{"message":"No such model","code":"model_not_found"}}`, kind: ErrorModelNotFound},
		{name: "context length", status: 400, body: `{"error":{"message":"Maximum context length exceeded","code":"context_length_exceeded"}}`, kind: ErrorContextLengthExceeded},
		{name: "content policy", status: 400, body: `{"error":{"message":"Blocked by content policy","code":"content_policy_violation"}}`, kind: ErrorContentPolicy},
		{name: "invalid request", status: 400, body: `{"error":{"message":"temperature is invalid","code":"invalid_request_error"}}`, kind: ErrorInvalidRequest},
		{name: "conflict timeout", status: 409, body: `{"error":{"message":"lock timeout"}}`, kind: ErrorTimeout, retryable: true},
		{name: "provider unavailable", status: 503, body: `service unavailable`, kind: ErrorProviderUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := make(http.Header)
			if test.retryAfter > 0 {
				headers.Set("Retry-After", "2")
			}
			response := &http.Response{
				StatusCode: test.status,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(test.body)),
			}
			err := modelErrorFromHTTPResponse("chat.completion", response)
			_ = response.Body.Close()
			if err.Kind != test.kind || err.Retryable != test.retryable {
				t.Fatalf("expected kind=%s retryable=%t, got %#v", test.kind, test.retryable, err)
			}
			if err.RetryAfter != test.retryAfter {
				t.Fatalf("expected retry_after=%s, got %s", test.retryAfter, err.RetryAfter)
			}
		})
	}
}

func TestModelErrorClassifiesContextCancellation(t *testing.T) {
	canceled := classifyModelError("chat.stream", context.Canceled)
	if canceled.Kind != ErrorCanceled || canceled.Retryable {
		t.Fatalf("unexpected canceled error: %#v", canceled)
	}
	timedOut := classifyModelError("chat.stream", context.DeadlineExceeded)
	if timedOut.Kind != ErrorTimeout || !timedOut.Retryable {
		t.Fatalf("unexpected timeout error: %#v", timedOut)
	}
}

func TestAuthenticationErrorDoesNotExposeProviderMessage(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"message":"Incorrect API key sk-sensitive-value","code":"invalid_api_key"}}`,
		)),
	}
	err := modelErrorFromHTTPResponse("chat.completion", response)
	_ = response.Body.Close()
	if strings.Contains(err.Error(), "sk-sensitive-value") || err.Message != "provider authentication failed" {
		t.Fatalf("authentication error leaked provider message: %v", err)
	}
}

func TestCapabilityFallbackRequiresMatchingInvalidRequest(t *testing.T) {
	streamUnsupported := &ModelError{Kind: ErrorInvalidRequest, Message: "stream_options.include_usage is not supported"}
	if !isStreamUsageUnsupported(streamUnsupported) {
		t.Fatal("expected stream usage capability fallback")
	}
	toolUnsupported := &ModelError{Kind: ErrorInvalidRequest, Message: "tool_choice is not supported"}
	if !isToolCallingUnsupported(toolUnsupported) {
		t.Fatal("expected tool calling capability fallback")
	}
	auth := &ModelError{Kind: ErrorAuthentication, Message: "tools request rejected"}
	if isToolCallingUnsupported(auth) {
		t.Fatal("authentication errors must not trigger capability fallback")
	}
}

func TestModelErrorMetadataIncludesRetryEvidence(t *testing.T) {
	metadata := modelErrorMetadata(&ModelError{
		Kind: ErrorRateLimited, Operation: "chat.completion", StatusCode: 429,
		ProviderType: "rate_limit_error", ProviderCode: "rate_limit_exceeded",
		Retryable: true, RetryAfter: 2 * time.Second, Attempts: 3,
	})
	if metadata["error_kind"] != ErrorRateLimited || metadata["operation"] != "chat.completion" || metadata["attempts"] != 3 {
		t.Fatalf("unexpected model error metadata: %#v", metadata)
	}
	if metadata["status_code"] != 429 || metadata["retry_after_ms"] != int64(2000) {
		t.Fatalf("missing retry evidence: %#v", metadata)
	}
}
