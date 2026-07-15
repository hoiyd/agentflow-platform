package openai

import (
	"context"
	"testing"
	"time"
)

func TestRetryPolicyRetriesTransientErrors(t *testing.T) {
	attempts := 0
	value, err := executeWithRetry(context.Background(), RetryPolicy{
		MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
	}, "chat.completion", func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", &ModelError{Kind: ErrorProviderUnavailable, Retryable: true, Message: "unavailable"}
		}
		return "ok", nil
	})
	if err != nil || value != "ok" || attempts != 3 {
		t.Fatalf("expected success after three attempts, value=%q attempts=%d err=%v", value, attempts, err)
	}
}

func TestRetryPolicyDoesNotRetryTerminalErrors(t *testing.T) {
	attempts := 0
	_, err := executeWithRetry(context.Background(), RetryPolicy{
		MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
	}, "chat.completion", func() (string, error) {
		attempts++
		return "", &ModelError{Kind: ErrorAuthentication, Message: "invalid key"}
	})
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorAuthentication || modelErr.Attempts != 1 || attempts != 1 {
		t.Fatalf("expected one terminal attempt, attempts=%d err=%#v", attempts, modelErr)
	}
}

func TestRetryPolicyReportsExhaustedAttempts(t *testing.T) {
	attempts := 0
	_, err := executeWithRetry(context.Background(), RetryPolicy{
		MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
	}, "chat.completion", func() (string, error) {
		attempts++
		return "", &ModelError{Kind: ErrorRateLimited, Retryable: true, Message: "slow down"}
	})
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorRateLimited || modelErr.Attempts != 3 || attempts != 3 {
		t.Fatalf("expected exhausted retry error, attempts=%d err=%#v", attempts, modelErr)
	}
}

func TestRetryPolicyUsesRetryAfterWithinConfiguredCap(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second}
	if delay := policy.delay(1, time.Second); delay != time.Second {
		t.Fatalf("expected Retry-After delay, got %s", delay)
	}
	if delay := policy.delay(1, 5*time.Second); delay != 2*time.Second {
		t.Fatalf("expected capped Retry-After delay, got %s", delay)
	}
}
