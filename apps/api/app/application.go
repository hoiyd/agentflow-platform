package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/httpapi"
	"agentflow-platform/apps/api/internal/store"
)

// Application owns the API process lifecycle and its composed dependencies.
type Application struct {
	config  config.Config
	store   store.Store
	handler *httpapi.Handler
	server  *http.Server

	closeOnce sync.Once
	closeErr  error
}

func New(cfg config.Config) (*Application, error) {
	dependencies, err := buildDependencies(cfg)
	if err != nil {
		return nil, err
	}

	return &Application{
		config:  cfg,
		store:   dependencies.store,
		handler: dependencies.handler,
		server: &http.Server{
			Addr:              ":" + cfg.Port,
			Handler:           dependencies.handler.Routes(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}, nil
}

// Run serves requests until the process context is cancelled or the server exits.
func (a *Application) Run(ctx context.Context) error {
	a.logStartup()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- a.server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		return normalizeServerError(err)
	case <-ctx.Done():
		log.Println("AgentFlow API shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown AgentFlow API: %w", err)
		}
		return normalizeServerError(<-serverErrors)
	}
}

// Close drains background work before closing the persistence adapter.
func (a *Application) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}

	a.closeOnce.Do(func() {
		var closeErrors []error
		if a.handler != nil {
			if err := a.handler.Close(ctx); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("drain memory sync: %w", err))
			}
		}
		if err := closeStore(a.store); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close store: %w", err))
		}
		a.closeErr = errors.Join(closeErrors...)
	})

	return a.closeErr
}

func normalizeServerError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *Application) logStartup() {
	cfg := a.config
	log.Printf("AgentFlow API listening on http://localhost:%s", cfg.Port)
	log.Printf("AgentFlow store driver: %s", cfg.StoreDriver)
	log.Printf("AgentFlow router mode: %s", cfg.RouterMode)
	log.Printf("AgentFlow autonomous limits: max_iterations=%d max_runtime=%s max_output_chars=%d max_tool_calls=%d", cfg.AutonomousMaxIterations, cfg.AutonomousMaxRuntime, cfg.AutonomousMaxOutputCharacters, cfg.AutonomousMaxToolCalls)
	log.Printf("AgentFlow native recovery: stale_run_timeout=%s", cfg.RecoveryStaleRunTimeout)
	log.Printf("AgentFlow run concurrency: max_concurrent=%d queue_size=%d wait_timeout=%s", cfg.MaxConcurrentRuns, cfg.RunQueueSize, cfg.RunQueueWaitTimeout)
	log.Printf("AgentFlow model concurrency: max_in_flight=%d rpm=%d tpm=%d", cfg.MaxConcurrentModelRequests, cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute)
	log.Printf("AgentFlow model retry: max_attempts=%d base_delay=%s max_delay=%s", cfg.ModelRetryMaxAttempts, cfg.ModelRetryBaseDelay, cfg.ModelRetryMaxDelay)
	log.Printf("AgentFlow context policy: window=%d output_reserve=%d safety_margin=%d history_max=%d memory_max=%d knowledge_max=%d tool_result_max=%d compaction=%s soft=%.2f hard=%.2f recent=%d summary_max=%d", cfg.ModelContextWindowTokens, cfg.ModelOutputReserveTokens, cfg.ContextSafetyMarginTokens, cfg.ContextHistoryMaxTokens, cfg.ContextMemoryMaxTokens, cfg.ContextKnowledgeMaxTokens, cfg.ContextToolResultMaxTokens, cfg.ContextCompactionMode, cfg.ContextCompactionSoftThreshold, cfg.ContextCompactionHardThreshold, cfg.ContextCompactionRecentTokens, cfg.ContextCompactionSummaryMaxTokens)
	if cfg.OpenAIAPIKey == "" {
		log.Println("OPENAI_API_KEY is empty; using local streaming fallback for verification")
	}
}
