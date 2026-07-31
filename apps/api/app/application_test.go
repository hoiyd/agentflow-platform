package app

import (
	"testing"

	"agentflow-platform/apps/api/internal/config"
)

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
