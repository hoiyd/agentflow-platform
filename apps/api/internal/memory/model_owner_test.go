package memory

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

func TestMemoryDoesNotRetryOwnerOverload(t *testing.T) {
	p := &BuiltinProvider{options: ProviderOptions{MaxAttempts: 3}}
	calls := 0
	err := p.retryStore(context.Background(), "recall.search", func() error { calls++; return &requestcontrol.OwnerAdmissionError{Code: "owner_model_queue_full"} })
	if err == nil || calls != 1 {
		t.Fatalf("memory amplified local overload: %v %d", err, calls)
	}
}
