package config

import (
	"testing"
	"time"
)

func TestOwnerModelConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("MAX_CONCURRENT_OWNER_MODEL_REQUESTS", "3")
	t.Setenv("OWNER_MODEL_QUEUE_SIZE", "0")
	t.Setenv("OWNER_MODEL_QUEUE_WAIT_TIMEOUT", "2s")
	cfg := Load()
	if cfg.MaxConcurrentOwnerModelRequests != 3 || cfg.OwnerModelQueueSize != 0 || cfg.OwnerModelQueueWaitTimeout != 2*time.Second {
		t.Fatal("owner settings ignored")
	}
}
