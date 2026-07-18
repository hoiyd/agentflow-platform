package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentflow-platform/apps/api/internal/llamacpp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	modelPath := flag.String("model", "", "path to a GGUF model")
	prompt := flag.String("prompt", "User: Reply with one short sentence about Go.\nAssistant:", "raw model prompt")
	maxTokens := flag.Int("max-tokens", 48, "maximum generated tokens")
	contextSize := flag.Int("context-size", 2048, "llama context size")
	batchSize := flag.Int("batch-size", 512, "prompt evaluation batch size")
	threads := flag.Int("threads", 0, "CPU threads; 0 uses the Go runtime default")
	gpuLayers := flag.Int("gpu-layers", 0, "model layers to offload to GPU")
	temperature := flag.Float64("temperature", 0.2, "sampling temperature; 0 uses greedy decoding")
	timeout := flag.Duration("timeout", 2*time.Minute, "generation timeout")
	flag.Parse()

	if !llamacpp.Available() {
		return llamacpp.ErrNotBuilt
	}
	if *modelPath == "" {
		return errors.New("-model is required")
	}

	config := llamacpp.DefaultConfig(*modelPath)
	config.ContextSize = *contextSize
	config.BatchSize = *batchSize
	config.GPULayers = *gpuLayers
	config.Temperature = float32(*temperature)
	if *threads > 0 {
		config.Threads = *threads
	}

	engine, err := llamacpp.New(config)
	if err != nil {
		return err
	}
	defer engine.Close()

	fmt.Fprintf(os.Stderr, "model: %s\n", engine.ModelDescription())
	fmt.Fprintln(os.Stderr, "stream:")

	baseContext, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(baseContext, *timeout)
	defer cancel()

	stats, err := engine.Generate(ctx, *prompt, *maxTokens, func(token string) error {
		_, writeErr := fmt.Fprint(os.Stdout, token)
		return writeErr
	})
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return err
	}

	seconds := stats.Duration.Seconds()
	tokensPerSecond := 0.0
	if seconds > 0 {
		tokensPerSecond = float64(stats.GeneratedTokens) / seconds
	}
	fmt.Fprintf(
		os.Stderr,
		"generated=%d duration=%s tokens_per_second=%.2f\n",
		stats.GeneratedTokens,
		stats.Duration.Round(time.Millisecond),
		tokensPerSecond,
	)
	return nil
}
