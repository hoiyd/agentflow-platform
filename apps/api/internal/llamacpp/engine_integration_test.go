//go:build cgo && llamacpp

package llamacpp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeEngine(t *testing.T) {
	modelPath := os.Getenv("LLAMA_CPP_TEST_MODEL")
	if modelPath == "" {
		t.Skip("LLAMA_CPP_TEST_MODEL is not set")
	}

	cfg := DefaultConfig(modelPath)
	cfg.ContextSize = 256
	cfg.BatchSize = 8
	cfg.Threads = 2
	cfg.GPULayers = 0
	cfg.Temperature = 0

	engine, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	t.Run("streams tokens", func(t *testing.T) {
		var output strings.Builder
		stats, err := engine.Generate(
			context.Background(),
			"User: Say hello.\nAssistant:",
			8,
			func(token string) error {
				output.WriteString(token)
				return nil
			},
		)
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if stats.GeneratedTokens == 0 || output.Len() == 0 {
			t.Fatalf("empty generation: stats=%+v output=%q", stats, output.String())
		}
	})

	t.Run("rejects context overflow", func(t *testing.T) {
		_, err := engine.Generate(context.Background(), "User: hello", cfg.ContextSize, nil)
		if err == nil || !strings.Contains(err.Error(), "exceed configured context size") {
			t.Fatalf("Generate() error = %v", err)
		}
	})

	t.Run("cancels native generation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		seen := 0
		_, err := engine.Generate(ctx, "User: Count forever.\nAssistant:", 64, func(string) error {
			seen++
			if seen == 1 {
				cancel()
			}
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Generate() error = %v, want context.Canceled", err)
		}
	})
}
