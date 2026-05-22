package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/httpapi"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func main() {
	log.SetOutput(os.Stdout)

	cfg := config.Load()

	fileStore, err := store.NewFileStore(cfg.DataPath)
	if err != nil {
		log.Fatalf("create store: %v", err)
	}

	openAIClient := openai.NewClientWithTimeout(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.OpenAIModel, cfg.OpenAITimeout)
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
	handler := httpapi.NewHandlerWithRouterMode(fileStore, openAIClient, toolManager, splitOrigins(cfg.AllowedOrigins), cfg.RouterMode)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("AgentFlow API listening on http://localhost:%s", cfg.Port)
	log.Printf("AgentFlow router mode: %s", cfg.RouterMode)
	if cfg.OpenAIAPIKey == "" {
		log.Println("OPENAI_API_KEY is empty; using local streaming fallback for verification")
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}

	_ = os.Stdout.Sync()
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
