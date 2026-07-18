package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

var ErrNotBuilt = errors.New("libllama support is not built; rebuild with CGO_ENABLED=1 and -tags llamacpp")

type Config struct {
	ModelPath   string
	ContextSize int
	BatchSize   int
	Threads     int
	GPULayers   int
	Temperature float32
	TopK        int
	TopP        float32
	Seed        uint32
}

func (c Config) validate() error {
	if strings.TrimSpace(c.ModelPath) == "" {
		return errors.New("model path is required")
	}
	if c.ContextSize <= 0 || c.BatchSize <= 0 || c.Threads <= 0 {
		return errors.New("context size, batch size, and threads must be positive")
	}
	if c.Temperature < 0 {
		return errors.New("temperature must not be negative")
	}
	if c.TopK < 0 {
		return errors.New("top-k must not be negative")
	}
	if c.TopP < 0 || c.TopP > 1 {
		return fmt.Errorf("top-p must be between 0 and 1: %v", c.TopP)
	}
	return nil
}

func DefaultConfig(modelPath string) Config {
	return Config{
		ModelPath:   modelPath,
		ContextSize: 2048,
		BatchSize:   512,
		Threads:     runtime.NumCPU(),
		GPULayers:   0,
		Temperature: 0.2,
		TopK:        40,
		TopP:        0.95,
		Seed:        42,
	}
}

type Stats struct {
	GeneratedTokens int
	Duration        time.Duration
}

type TokenHandler func(token string) error

type Generator interface {
	ModelDescription() string
	Generate(ctx context.Context, prompt string, maxTokens int, onToken TokenHandler) (Stats, error)
	Close() error
}
