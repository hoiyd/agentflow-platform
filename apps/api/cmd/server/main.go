package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/httpapi"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

func main() {
	cfg := config.Load()

	fileStore, err := store.NewFileStore(cfg.DataPath)
	if err != nil {
		log.Fatalf("create store: %v", err)
	}

	openAIClient := openai.NewClient(cfg.OpenAIAPIKey, cfg.OpenAIModel)
	handler := httpapi.NewHandler(fileStore, openAIClient, splitOrigins(cfg.AllowedOrigins))

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("AgentFlow API listening on http://localhost:%s", cfg.Port)
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
