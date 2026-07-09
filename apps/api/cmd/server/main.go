package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent"
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
	mcpClient := tools.NewRoutedMCPClient()
	defer func() {
		if err := mcpClient.Close(); err != nil {
			log.Printf("close mcp client: %v", err)
		}
	}()
	toolManager, err := tools.NewManager(context.Background(), cfg.ToolConfigPath, mcpClient)
	if err != nil {
		log.Fatalf("create tools manager: %v", err)
	}
	handler := httpapi.NewHandlerWithRouterModeAndLimits(appStore, openAIClient, toolManager, splitOrigins(cfg.AllowedOrigins), cfg.RouterMode, agent.AutonomousLimits{
		MaxIterations:  cfg.AutonomousMaxIterations,
		MaxRuntime:     cfg.AutonomousMaxRuntime,
		MaxOutputChars: cfg.AutonomousMaxOutputCharacters,
		MaxToolCalls:   cfg.AutonomousMaxToolCalls,
	})

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
	if cfg.OpenAIAPIKey == "" {
		log.Println("OPENAI_API_KEY is empty; using local streaming fallback for verification")
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
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
