package llamacpp

import (
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig("model.gguf")

	if cfg.ModelPath != "model.gguf" {
		t.Fatalf("ModelPath = %q", cfg.ModelPath)
	}
	if cfg.ContextSize != 2048 || cfg.BatchSize != 512 {
		t.Fatalf("unexpected context defaults: %+v", cfg)
	}
	if cfg.Threads < 1 {
		t.Fatalf("Threads = %d", cfg.Threads)
	}
	if cfg.TopK != 40 || cfg.TopP != 0.95 || cfg.Temperature != 0.2 {
		t.Fatalf("unexpected sampler defaults: %+v", cfg)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "model", mutate: func(c *Config) { c.ModelPath = "" }, want: "model path"},
		{name: "context", mutate: func(c *Config) { c.ContextSize = 0 }, want: "context size"},
		{name: "temperature", mutate: func(c *Config) { c.Temperature = -1 }, want: "temperature"},
		{name: "top-k", mutate: func(c *Config) { c.TopK = -1 }, want: "top-k"},
		{name: "top-p", mutate: func(c *Config) { c.TopP = 1.1 }, want: "top-p"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig("model.gguf")
			tt.mutate(&cfg)
			if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
