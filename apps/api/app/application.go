package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/config"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

// Application owns the API process lifecycle and its composed dependencies.
type Application struct {
	config        config.Config
	store         store.Store
	memoryCurator *memorypkg.Curator
	runController *concurrency.RunController
	server        *http.Server

	closeOnce sync.Once
	closeErr  error
}

func New(cfg config.Config) (*Application, error) {
	dependencies, err := buildDependencies(cfg)
	if err != nil {
		return nil, err
	}

	return &Application{
		config:        cfg,
		store:         dependencies.store,
		memoryCurator: dependencies.memoryCurator,
		runController: dependencies.runController,
		server: &http.Server{
			Addr:              serverAddress(cfg),
			Handler:           dependencies.handler.Routes(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}, nil
}

func serverAddress(cfg config.Config) string {
	host := strings.TrimSpace(cfg.BindAddress)
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, cfg.Port)
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
		drained := true
		if a.runController != nil {
			if err := a.runController.CloseAndWait(ctx); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("drain accepted runs: %w", err))
				drained = false
			}
		}
		if a.memoryCurator != nil {
			if err := a.memoryCurator.Close(ctx); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("drain memory curation: %w", err))
			}
		}
		if drained {
			if err := closeStore(a.store); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close store: %w", err))
			}
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
	log.Printf("AgentFlow API listening on http://%s", serverAddress(cfg))
	log.Printf("AgentFlow store driver: %s", cfg.StoreDriver)
	log.Printf("AgentFlow router mode: %s", cfg.RouterMode)
	log.Printf("AgentFlow autonomous profile: max_iterations=%d max_output_chars=%d run_budget_runtime_cap=%s run_budget_tool_cap=%d", cfg.AutonomousMaxIterations, cfg.AutonomousMaxOutputCharacters, cfg.AutonomousMaxRuntime, cfg.AutonomousMaxToolCalls)
	log.Printf("AgentFlow native recovery: stale_run_timeout=%s", cfg.RecoveryStaleRunTimeout)
	log.Printf("AgentFlow runtime invariants: mode=%s", cfg.RuntimeInvariantMode)
	log.Printf("AgentFlow run concurrency: max_concurrent=%d queue_size=%d wait_timeout=%s", cfg.MaxConcurrentRuns, cfg.RunQueueSize, cfg.RunQueueWaitTimeout)
	log.Printf("AgentFlow model concurrency: max_in_flight=%d rpm=%d tpm=%d", cfg.MaxConcurrentModelRequests, cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute)
	log.Printf("AgentFlow model retry: max_attempts=%d base_delay=%s max_delay=%s", cfg.ModelRetryMaxAttempts, cfg.ModelRetryBaseDelay, cfg.ModelRetryMaxDelay)
	log.Printf("AgentFlow model request capture: mode=%s max_bytes=%d retention=%s", cfg.ModelRequestCaptureMode, cfg.ModelRequestCaptureMaxBytes, cfg.ModelRequestCaptureRetention)
	log.Printf("AgentFlow context policy: window=%d output_reserve=%d safety_margin=%d history_max=%d memory_max=%d knowledge_max=%d tool_result_max=%d history_retrieval_results=%d history_retrieval_chars=%d history_retrieval_tokens=%d history_retrieval_window=%d compaction=%s soft=%.2f hard=%.2f recent=%d summary_max=%d", cfg.ModelContextWindowTokens, cfg.ModelOutputReserveTokens, cfg.ContextSafetyMarginTokens, cfg.ContextHistoryMaxTokens, cfg.ContextMemoryMaxTokens, cfg.ContextKnowledgeMaxTokens, cfg.ContextToolResultMaxTokens, cfg.ContextHistoryRetrievalMaxResults, cfg.ContextHistoryRetrievalMaxChars, cfg.ContextHistoryRetrievalMaxTokens, cfg.ContextHistoryRetrievalWindow, cfg.ContextCompactionMode, cfg.ContextCompactionSoftThreshold, cfg.ContextCompactionHardThreshold, cfg.ContextCompactionRecentTokens, cfg.ContextCompactionSummaryMaxTokens)
	if cfg.OpenAIAPIKey == "" {
		log.Println("OPENAI_API_KEY is empty; using local streaming fallback for verification")
	}
}
