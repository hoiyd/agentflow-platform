package failure

import (
	"context"
	"fmt"
	"testing"
)

type testError struct{}

func (testError) Error() string { return "failed" }
func (testError) FailureInfo() Info {
	return Info{Code: "provider_busy", Source: "model", Category: CategoryAvailability, Retryable: true, Details: map[string]any{"attempts": 2}}
}

func TestDescribeFindsWrappedClassifiedError(t *testing.T) {
	info := Describe(fmt.Errorf("execute: %w", testError{}))
	if info.Code != "provider_busy" || info.Source != "model" || !info.Retryable || info.Details["attempts"] != 2 {
		t.Fatalf("unexpected failure info: %#v", info)
	}
}

func TestDescribeClassifiesContextErrors(t *testing.T) {
	if info := Describe(context.Canceled); info.Category != CategoryCanceled || info.Retryable {
		t.Fatalf("unexpected canceled classification: %#v", info)
	}
	if info := Describe(context.DeadlineExceeded); info.Category != CategoryTimeout || !info.Retryable {
		t.Fatalf("unexpected deadline classification: %#v", info)
	}
}

func TestMergeCopiesPayloadAndPreservesMessage(t *testing.T) {
	original := map[string]any{"error": "public message"}
	merged := Merge(original, testError{})
	if len(original) != 1 {
		t.Fatalf("merge mutated input: %#v", original)
	}
	if merged["error"] != "public message" || merged["error_kind"] != "provider_busy" || merged["error_category"] != string(CategoryAvailability) {
		t.Fatalf("unexpected merged payload: %#v", merged)
	}
}

func TestNewCreatesClassifiedSentinel(t *testing.T) {
	err := New(Definition{
		Message: "queue is full",
		Info: Info{
			Code: "queue_full", Source: "scheduler",
			Category: CategoryCapacity, Retryable: true,
		},
	})
	info := Describe(fmt.Errorf("reserve: %w", err))
	if info.Code != "queue_full" || info.Source != "scheduler" || !info.Retryable || err.Error() != "queue is full" {
		t.Fatalf("unexpected sentinel classification: err=%v info=%#v", err, info)
	}
}
