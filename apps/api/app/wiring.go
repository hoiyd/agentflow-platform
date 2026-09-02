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
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/httpapi"
	"agentflow-platform/apps/api/internal/knowledge"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/recovery"
	"agentflow-platform/apps/api/internal/requestcapture"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/toolprogress"
	"agentflow-platform/apps/api/internal/tools"
	"agentflow-platform/apps/api/internal/verification"
)

type applicationDependencies struct {
	store          store.Store
	handler        *httpapi.Handler
	memoryProvider memorypkg.Provider
	runController  *concurrency.RunController
}

func buildDependencies(cfg config.Config) (applicationDependencies, error) {
	baseStore, err := newStore(cfg)
	if err != nil {
		return applicationDependencies{}, fmt.Errorf("create store: %w", err)
	}
	eventHub := event.NewHub(256)
	appStore := newObservableStore(baseStore, eventHub)

	cleanupStore := true
	defer func() {
		if cleanupStore {
			_ = closeStore(appStore)
		}
	}()

	if recovered, recoveryErr := recovery.MarkStaleRunningRuns(appStore, cfg.RecoveryStaleRunTimeout); recoveryErr != nil {
		return applicationDependencies{}, fmt.Errorf("repair interrupted runs: %w", recoveryErr)
	} else if recovered > 0 {
		log.Printf("native recovery repaired %d stale running run(s) as failed_recoverable", recovered)
	}
	if reconciled, recoveryErr := recovery.ReconcileChildRunDelegations(appStore); recoveryErr != nil {
		return applicationDependencies{}, fmt.Errorf("reconcile child run delegations: %w", recoveryErr)
	} else if reconciled > 0 {
		log.Printf("native recovery reconciled %d child run delegation(s)", reconciled)
	}

	modelClient := newModelClient(cfg)
	modelClient.SetToolEffectJournal(appStore)
	modelClient.SetToolArtifactStore(appStore)
	modelClient.SetToolArtifactPolicy(openai.ToolArtifactPolicy{
		MaxBatchResultBytes: cfg.ToolResultMaxBatchBytes,
		MaxArtifactBytes:    cfg.ToolArtifactMaxBytes,
		PreviewBytes:        cfg.ToolArtifactPreviewBytes,
		Retention:           cfg.ToolArtifactRetention,
	})
	modelClient.SetRequestRecorder(requestcapture.NewRecorder(appStore, requestcapture.Options{
		Mode: domain.ModelRequestCaptureMode(cfg.ModelRequestCaptureMode), MaxBytes: cfg.ModelRequestCaptureMaxBytes,
		Retention: cfg.ModelRequestCaptureRetention,
	}))
	toolManager, err := tools.NewManager(cfg.ToolConfigPath)
	if err != nil {
		return applicationDependencies{}, fmt.Errorf("create tools manager: %w", err)
	}
	verifierRegistry := verification.NewRegistry(verification.Options{
		WorkspaceRoot:           cfg.VerificationWorkspaceRoot,
		AllowedCommands:         splitCSV(cfg.VerificationAllowedCommands),
		AllowedHTTPHosts:        splitCSV(cfg.VerificationAllowedHTTPHosts),
		MaxArtifactBytes:        cfg.VerificationMaxArtifactBytes,
		AnswerRelevanceEmbedder: newAnswerRelevanceEmbedder(modelClient),
	})
	verificationEngine := verification.NewEngine(appStore, verifierRegistry)
	retrievalPipeline := rag.NewRetrievalPipeline(appStore)
	knowledgeBase := knowledge.NewKnowledgeBaseWithRetriever(appStore, modelClient, retrievalPipeline)
	memoryProvider := newMemoryProvider(cfg, appStore, modelClient)
	if err := memoryProvider.Initialize(context.Background()); err != nil {
		return applicationDependencies{}, fmt.Errorf("initialize memory provider: %w", err)
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
		RunBudget: domain.RuntimeRunBudget{
			MaxModelCalls: cfg.RunMaxModelCalls, MaxPromptTokens: cfg.RunMaxPromptTokens,
			MaxCompletionTokens: cfg.RunMaxCompletionTokens, MaxTotalTokens: cfg.RunMaxTotalTokens,
			MaxToolCalls: cfg.RunMaxToolCalls, MaxRuntimeMS: cfg.RunMaxRuntime.Milliseconds(),
			MaxEstimatedCostMicros:           cfg.RunMaxEstimatedCostMicros,
			InputCostPerMillionTokensMicros:  cfg.ModelInputCostPerMillionMicros,
			OutputCostPerMillionTokensMicros: cfg.ModelOutputCostPerMillionMicros,
		},
		ToolProgressGuard: toolprogress.Config{
			Version: toolprogress.CurrentVersion, Enabled: cfg.ToolProgressGuardEnabled,
			WarnAfter: cfg.ToolProgressWarnAfter, BlockAfter: cfg.ToolProgressBlockAfter,
			HaltAfter: cfg.ToolProgressHaltAfter, HistoryMax: 8,
		},
		KnowledgeRetriever: retrievalPipeline,
		MemoryRecall:       memoryProvider,
		LiveEvents:         eventHub,
		ChildRuns: agent.ChildRunLimits{
			MaxConcurrent: cfg.MaxConcurrentChildRuns, MaxPerParent: cfg.MaxChildRunsPerParent,
			Timeout: cfg.ChildRunTimeout, SummaryMaxChars: cfg.ChildRunSummaryMaxCharacters,
			RunBudget: domain.RuntimeRunBudget{
				MaxModelCalls: cfg.ChildRunMaxModelCalls, MaxTotalTokens: cfg.ChildRunMaxTotalTokens,
				MaxToolCalls: cfg.ChildRunMaxToolCalls, MaxRuntimeMS: cfg.ChildRunTimeout.Milliseconds(),
				InputCostPerMillionTokensMicros:  cfg.ModelInputCostPerMillionMicros,
				OutputCostPerMillionTokensMicros: cfg.ModelOutputCostPerMillionMicros,
			},
		},
	})
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
		Memory:         memoryProvider,
		Knowledge:      knowledgeBase,
		RunController:  runController,
		Verification:   verificationEngine,
		AllowedOrigins: splitOrigins(cfg.AllowedOrigins),
	})
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = memoryProvider.Close(ctx)
		return applicationDependencies{}, fmt.Errorf("create http handler: %w", err)
	}

	cleanupStore = false
	return applicationDependencies{store: appStore, handler: handler, memoryProvider: memoryProvider, runController: runController}, nil
}

type answerRelevanceEmbeddingClient interface {
	EmbedText(context.Context, string) (openai.Embedding, error)
}

func newAnswerRelevanceEmbedder(client answerRelevanceEmbeddingClient) verification.AnswerRelevanceEmbedder {
	return func(ctx context.Context, input string) (verification.AnswerRelevanceEmbedding, error) {
		embedding, err := client.EmbedText(ctx, input)
		if err != nil {
			return verification.AnswerRelevanceEmbedding{}, err
		}
		return verification.AnswerRelevanceEmbedding{
			Vector: embedding.Vector, Model: embedding.Model, Provider: embedding.Provider,
			Estimated: embedding.Estimated, Dimensions: embedding.Dimensions,
		}, nil
	}
}

func newMemoryProvider(cfg config.Config, appStore store.Store, modelClient *openai.Client) *memorypkg.BuiltinProvider {
	var fallback memorypkg.CandidateExtractor
	adaptiveEnabled := cfg.MemoryAdaptiveExtractionMode == memorypkg.AdaptiveModeShadow || cfg.MemoryAdaptiveExtractionMode == memorypkg.AdaptiveModeAuto
	if strings.TrimSpace(cfg.OpenAIAPIKey) != "" && adaptiveEnabled {
		fallback = memorypkg.AdaptiveCandidateExtractor{Model: modelClient}
	}
	return memorypkg.NewBuiltinProvider(appStore, modelClient, memorypkg.ProviderOptions{
		QueueSize: cfg.MemorySyncQueueSize, JobTimeout: cfg.MemorySyncJobTimeout,
		MaxAttempts: cfg.MemoryProviderMaxAttempts, RetryBaseDelay: cfg.MemoryProviderRetryBaseDelay,
		Extractor: memorypkg.CompositeCandidateExtractor{
			Primary: memorypkg.RuleBasedCandidateExtractor{}, Fallback: fallback,
		},
		Policy:       memorypkg.ConservativeCandidatePolicy{MinConfidence: cfg.MemoryAdaptiveMinConfidence},
		AdaptiveMode: cfg.MemoryAdaptiveExtractionMode,
	})
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
		HistoryRetrievalEnabled:    true,
		HistoryRetrievalMaxResults: cfg.ContextHistoryRetrievalMaxResults,
		HistoryRetrievalMaxChars:   cfg.ContextHistoryRetrievalMaxChars,
		HistoryRetrievalMaxTokens:  cfg.ContextHistoryRetrievalMaxTokens,
		HistoryRetrievalWindow:     cfg.ContextHistoryRetrievalWindow,
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
	return splitCSV(value)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}
