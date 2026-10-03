package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentflow-platform/apps/api/app"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/redaction"
)

func main() {
	log.SetOutput(redaction.Writer{Writer: os.Stdout})

	cfg := config.Load()
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("create AgentFlow application: %v", err)
	}
	defer func() {
		shutdownTimeout := 5 * time.Second
		if cfg.SandboxEnabled {
			shutdownTimeout = 35 * time.Second // Allow independent VM cleanup and receipt settlement.
		}
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			log.Printf("close AgentFlow application: %v", err)
		}
	}()

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := application.Run(shutdownSignal); err != nil {
		log.Printf("serve AgentFlow API: %v", err)
	}

	_ = os.Stdout.Sync()
}
