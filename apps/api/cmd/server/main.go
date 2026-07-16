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
)

func main() {
	log.SetOutput(os.Stdout)

	application, err := app.New(config.Load())
	if err != nil {
		log.Fatalf("create AgentFlow application: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
