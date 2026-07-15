package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/httpapi"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/recovery"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func main() {
	log.SetOutput(os.Stdout)

	cfg := config.Load()

	appStore, err := newStore(cfg)
	if err != nil {
		log.Fatalf("create store: %v", err)
	}
	if closer, ok := appStore.(interface{ Close() error }); ok {
		defer func() {
			if err := closer.Close(); err != nil {
				log.Printf("close store: %v", err)
			}
		}()
	}
	if recovered, err := recovery.MarkStaleRunningRuns(appStore, cfg.RecoveryStaleRunTimeout); err != nil {
		log.Printf("native recovery scan failed: %v", err)
	} else if recovered > 0 {
		log.Printf("native recovery marked %d stale running run(s) as failed_recoverable", recovered)
	}

	openAIClient := openai.NewClientWithTimeoutAndEmbeddingModel(
		cfg.OpenAIAPIKey,
		cfg.OpenAIBaseURL,
		cfg.EmbeddingBaseURL,
		cfg.OpenAIModel,
		cfg.EmbeddingModel,
		cfg.EmbeddingDimensions,
		cfg.OpenAITimeout,
	)
	openAIClient.SetRequestGovernor(concurrency.NewModelGovernor(concurrency.ModelOptions{
		MaxConcurrent:     cfg.MaxConcurrentModelRequests,
		RequestsPerPeriod: cfg.ModelRequestsPerMinute,
		TokensPerPeriod:   cfg.ModelTokensPerMinute,
	}))
	toolManager, err := tools.NewManager(cfg.ToolConfigPath)
	if err != nil {
		log.Fatalf("create tools manager: %v", err)
	}
	handler := httpapi.NewHandlerWithRouterModeAndLimits(appStore, openAIClient, toolManager, splitOrigins(cfg.AllowedOrigins), cfg.RouterMode, agent.AutonomousLimits{
		MaxIterations:  cfg.AutonomousMaxIterations,
		MaxRuntime:     cfg.AutonomousMaxRuntime,
		MaxOutputChars: cfg.AutonomousMaxOutputCharacters,
		MaxToolCalls:   cfg.AutonomousMaxToolCalls,
	})
	handler.SetRunController(concurrency.NewRunController(concurrency.RunOptions{
		MaxConcurrent: cfg.MaxConcurrentRuns,
		QueueSize:     cfg.RunQueueSize,
		WaitTimeout:   cfg.RunQueueWaitTimeout,
	}))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := handler.Close(ctx); err != nil {
			log.Printf("drain memory sync: %v", err)
		}
	}()

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("AgentFlow API listening on http://localhost:%s", cfg.Port)
	log.Printf("AgentFlow store driver: %s", cfg.StoreDriver)
	log.Printf("AgentFlow router mode: %s", cfg.RouterMode)
	log.Printf("AgentFlow autonomous limits: max_iterations=%d max_runtime=%s max_output_chars=%d max_tool_calls=%d", cfg.AutonomousMaxIterations, cfg.AutonomousMaxRuntime, cfg.AutonomousMaxOutputCharacters, cfg.AutonomousMaxToolCalls)
	log.Printf("AgentFlow native recovery: stale_run_timeout=%s", cfg.RecoveryStaleRunTimeout)
	log.Printf("AgentFlow run concurrency: max_concurrent=%d queue_size=%d wait_timeout=%s", cfg.MaxConcurrentRuns, cfg.RunQueueSize, cfg.RunQueueWaitTimeout)
	log.Printf("AgentFlow model concurrency: max_in_flight=%d rpm=%d tpm=%d", cfg.MaxConcurrentModelRequests, cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute)
	if cfg.OpenAIAPIKey == "" {
		log.Println("OPENAI_API_KEY is empty; using local streaming fallback for verification")
	}

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("serve AgentFlow API: %v", err)
		}
	case <-shutdownSignal.Done():
		log.Println("AgentFlow API shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown AgentFlow API: %v", err)
		}
	}

	_ = os.Stdout.Sync()
}

func newStore(cfg config.Config) (store.Store, error) {
	if cfg.StoreDriver == "postgres" {
		return store.NewPostgresStore(cfg.DatabaseURL)
	}
	return store.NewFileStore(cfg.DataPath)
}

func splitOrigins(value string) []string {
	parts := strings.Split(value, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}
