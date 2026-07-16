package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/httpapi"
	"agentflow-platform/apps/api/internal/knowledge"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/recovery"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

type applicationDependencies struct {
	store   store.Store
	handler *httpapi.Handler
	memory  *memorypkg.Syncer
}

func buildDependencies(cfg config.Config) (applicationDependencies, error) {
	appStore, err := newStore(cfg)
	if err != nil {
		return applicationDependencies{}, fmt.Errorf("create store: %w", err)
	}

	cleanupStore := true
	defer func() {
		if cleanupStore {
			_ = closeStore(appStore)
		}
	}()

	if recovered, recoveryErr := recovery.MarkStaleRunningRuns(appStore, cfg.RecoveryStaleRunTimeout); recoveryErr != nil {
		log.Printf("native recovery scan failed: %v", recoveryErr)
	} else if recovered > 0 {
		log.Printf("native recovery marked %d stale running run(s) as failed_recoverable", recovered)
	}

	modelClient := newModelClient(cfg)
	toolManager, err := tools.NewManager(cfg.ToolConfigPath)
	if err != nil {
		return applicationDependencies{}, fmt.Errorf("create tools manager: %w", err)
	}

	agentRuntime := agent.NewRuntime(agent.RuntimeOptions{
		Store:           appStore,
		ModelClient:     modelClient,
		Tools:           toolManager,
		RouterMode:      cfg.RouterMode,
		ContextAssembly: contextAssemblyConfig(cfg),
		Autonomous: agent.AutonomousLimits{
			MaxIterations:  cfg.AutonomousMaxIterations,
			MaxRuntime:     cfg.AutonomousMaxRuntime,
			MaxOutputChars: cfg.AutonomousMaxOutputCharacters,
			MaxToolCalls:   cfg.AutonomousMaxToolCalls,
		},
	})
	semanticMemory := memorypkg.NewSemanticMemory(appStore, modelClient)
	knowledgeBase := knowledge.NewKnowledgeBase(appStore, modelClient)
	memorySyncer := memorypkg.NewSyncer(appStore, modelClient)
	runController := concurrency.NewRunController(concurrency.RunOptions{
		MaxConcurrent: cfg.MaxConcurrentRuns,
		QueueSize:     cfg.RunQueueSize,
		WaitTimeout:   cfg.RunQueueWaitTimeout,
	})
	handler, err := httpapi.NewHandler(httpapi.Dependencies{
		Store:          appStore,
		ModelClient:    modelClient,
		Tools:          toolManager,
		AgentRuntime:   agentRuntime,
		Memory:         semanticMemory,
		Knowledge:      knowledgeBase,
		MemoryQueue:    memorySyncer,
		RunController:  runController,
		AllowedOrigins: splitOrigins(cfg.AllowedOrigins),
	})
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = memorySyncer.Close(ctx)
		return applicationDependencies{}, fmt.Errorf("create http handler: %w", err)
	}

	cleanupStore = false
	return applicationDependencies{store: appStore, handler: handler, memory: memorySyncer}, nil
}

func newModelClient(cfg config.Config) *openai.Client {
	client := openai.NewClientWithTimeoutAndEmbeddingModel(
		cfg.OpenAIAPIKey,
		cfg.OpenAIBaseURL,
		cfg.EmbeddingBaseURL,
		cfg.OpenAIModel,
		cfg.EmbeddingModel,
		cfg.EmbeddingDimensions,
		cfg.OpenAITimeout,
	)
	client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{
		MaxConcurrent:     cfg.MaxConcurrentModelRequests,
		RequestsPerPeriod: cfg.ModelRequestsPerMinute,
		TokensPerPeriod:   cfg.ModelTokensPerMinute,
	}))
	retryPolicy := openai.DefaultRetryPolicy()
	retryPolicy.MaxAttempts = cfg.ModelRetryMaxAttempts
	retryPolicy.BaseDelay = cfg.ModelRetryBaseDelay
	retryPolicy.MaxDelay = cfg.ModelRetryMaxDelay
	client.SetRetryPolicy(retryPolicy)
	return client
}

func contextAssemblyConfig(cfg config.Config) domain.ContextAssemblyConfig {
	return domain.ContextAssemblyConfig{
		ContextWindowTokens:        cfg.ModelContextWindowTokens,
		OutputReserveTokens:        cfg.ModelOutputReserveTokens,
		SafetyMarginTokens:         cfg.ContextSafetyMarginTokens,
		HistoryMaxTokens:           cfg.ContextHistoryMaxTokens,
		MemoryMaxTokens:            cfg.ContextMemoryMaxTokens,
		KnowledgeMaxTokens:         cfg.ContextKnowledgeMaxTokens,
		ToolResultMaxTokens:        cfg.ContextToolResultMaxTokens,
		CompactionMode:             cfg.ContextCompactionMode,
		CompactionSoftThreshold:    cfg.ContextCompactionSoftThreshold,
		CompactionHardThreshold:    cfg.ContextCompactionHardThreshold,
		CompactionRecentTokens:     cfg.ContextCompactionRecentTokens,
		CompactionSummaryMaxTokens: cfg.ContextCompactionSummaryMaxTokens,
		CompactionTimeoutMS:        cfg.ContextCompactionTimeout.Milliseconds(),
	}
}

func newStore(cfg config.Config) (store.Store, error) {
	if cfg.StoreDriver == "postgres" {
		return store.NewPostgresStore(cfg.DatabaseURL)
	}
	return store.NewFileStore(cfg.DataPath)
}

func closeStore(appStore store.Store) error {
	if closer, ok := appStore.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func splitOrigins(value string) []string {
	parts := strings.Split(value, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}
