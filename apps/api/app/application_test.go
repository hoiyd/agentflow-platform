package app

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
)

func TestNormalizeServerErrorAndStartupLogging(t *testing.T) {
	if err := normalizeServerError(nil); err != nil {
		t.Fatalf("normalize nil server error: %v", err)
	}
	if err := normalizeServerError(http.ErrServerClosed); err != nil {
		t.Fatalf("normalize closed server error: %v", err)
	}
	want := errors.New("listen failed")
	if err := normalizeServerError(want); !errors.Is(err, want) {
		t.Fatalf("normalize unexpected server error: %v", err)
	}

	application := &Application{config: config.Config{
		Port: "8080", StoreDriver: "file", RouterMode: "auto",
		AutonomousMaxIterations: 5, AutonomousMaxOutputCharacters: 60000,
		AutonomousMaxRuntime: 5 * time.Minute, AutonomousMaxToolCalls: 20,
		RecoveryStaleRunTimeout: time.Minute, MaxConcurrentRuns: 4,
		RunQueueSize: 8, RunQueueWaitTimeout: time.Second,
		MaxConcurrentModelRequests: 2, ModelRequestsPerMinute: 60, ModelTokensPerMinute: 10000,
		ModelRetryMaxAttempts: 3, ModelRetryBaseDelay: time.Millisecond, ModelRetryMaxDelay: time.Second,
	}}
	application.logStartup()
}

func TestServerAddressDefaultsToLoopback(t *testing.T) {
	t.Parallel()

	got := serverAddress(config.Config{Port: "8080"})
	if got != "127.0.0.1:8080" {
		t.Fatalf("serverAddress() = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestServerAddressAllowsExplicitInterface(t *testing.T) {
	t.Parallel()

	got := serverAddress(config.Config{BindAddress: "0.0.0.0", Port: "8080"})
	if got != "0.0.0.0:8080" {
		t.Fatalf("serverAddress() = %q, want %q", got, "0.0.0.0:8080")
	}
}
