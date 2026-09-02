package config

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BindAddress         string
	Port                string
	OpenAIAPIKey        string
	OpenAIBaseURL       string
	OpenAIModel         string
	EmbeddingBaseURL    string
	EmbeddingModel      string
	EmbeddingDimensions int
	OpenAITimeout       time.Duration
	// MaxConcurrentRuns caps active Agent runs across all conversations.
	MaxConcurrentRuns int
	// RunQueueSize is the additional bounded waiting capacity beyond active runs.
	RunQueueSize int
	// RunQueueWaitTimeout limits how long an admitted run may wait for execution.
	RunQueueWaitTimeout time.Duration
	// MaxConcurrentChildRuns caps active delegated Runs. Child admission never queues.
	MaxConcurrentChildRuns int
	// MaxChildRunsPerParent bounds fan-out from one parent Run.
	MaxChildRunsPerParent int
	// ChildRunTimeout is frozen into each delegated Run.
	ChildRunTimeout time.Duration
	// ChildRunSummaryMaxCharacters bounds output admitted back into the parent context.
	ChildRunSummaryMaxCharacters int
	// ChildRunMaxModelCalls, tokens, and tools form the independent child Run budget.
	ChildRunMaxModelCalls  int
	ChildRunMaxTotalTokens int
	ChildRunMaxToolCalls   int
	// MaxConcurrentModelRequests caps in-flight Chat and Embedding HTTP requests.
	MaxConcurrentModelRequests int
	// ModelRequestsPerMinute configures the per-API-key request token bucket; zero disables it.
	ModelRequestsPerMinute int
	// ModelTokensPerMinute configures the approximate input-token bucket; zero disables it.
	ModelTokensPerMinute int
	// ModelRetryMaxAttempts includes the initial request and all retries.
	ModelRetryMaxAttempts int
	// ModelRetryBaseDelay is the first exponential-backoff delay.
	ModelRetryBaseDelay time.Duration
	// ModelRetryMaxDelay caps exponential backoff and provider Retry-After values.
	ModelRetryMaxDelay time.Duration
	// ModelRequestCaptureMode controls persisted model payload content: metadata_only, redacted, or full.
	ModelRequestCaptureMode string
	// ModelRequestCaptureMaxBytes caps stored full/redacted canonical JSON; the envelope hash is always retained.
	ModelRequestCaptureMaxBytes int
	// ModelRequestCaptureRetention limits how long redacted/full payload content remains readable and persisted.
	ModelRequestCaptureRetention time.Duration
	// RunMaxModelCalls limits logical LLM calls in one Run; provider retries do not add calls.
	RunMaxModelCalls int
	// RunMaxPromptTokens limits cumulative input usage, including open reservations.
	RunMaxPromptTokens int
	// RunMaxCompletionTokens limits cumulative output usage and bounds each request when possible.
	RunMaxCompletionTokens int
	// RunMaxTotalTokens limits cumulative input plus output usage.
	RunMaxTotalTokens int
	// RunMaxToolCalls limits admitted, validated tool executions across all stages.
	RunMaxToolCalls int
	// RunMaxRuntime limits active execution time; waiting_for_user time is excluded.
	RunMaxRuntime time.Duration
	// RunMaxEstimatedCostMicros limits configured model cost in integer microdollars.
	RunMaxEstimatedCostMicros int64
	// ModelInputCostPerMillionMicros is frozen into each Run for deterministic estimates.
	ModelInputCostPerMillionMicros int64
	// ModelOutputCostPerMillionMicros is frozen into each Run for deterministic estimates.
	ModelOutputCostPerMillionMicros int64
	// ModelContextWindowTokens is the provider model's total input and output context capacity.
	ModelContextWindowTokens int
	// ModelOutputReserveTokens reserves capacity for the model response.
	ModelOutputReserveTokens int
	// ContextSafetyMarginTokens protects against tokenizer estimation error.
	ContextSafetyMarginTokens int
	// ContextHistoryMaxTokens caps prior conversation messages in one model call.
	ContextHistoryMaxTokens int
	// ContextMemoryMaxTokens caps semantic memory injected into one model call.
	ContextMemoryMaxTokens int
	// ContextKnowledgeMaxTokens caps retrieved document chunks in one model call.
	ContextKnowledgeMaxTokens int
	// ContextToolResultMaxTokens caps required tool-result messages deterministically.
	ContextToolResultMaxTokens int
	// ContextHistoryRetrievalMaxResults caps original Message/Event sources reintroduced per model call.
	ContextHistoryRetrievalMaxResults int
	// ContextHistoryRetrievalMaxChars caps retrieved original-history text per model call.
	ContextHistoryRetrievalMaxChars int
	// ContextHistoryRetrievalMaxTokens caps retrieved original-history context per model call.
	ContextHistoryRetrievalMaxTokens int
	// ContextHistoryRetrievalWindow includes this many adjacent sources on each side of a direct match.
	ContextHistoryRetrievalWindow int
	// ContextCompactionMode supports auto or off.
	ContextCompactionMode string
	// ContextCompactionSoftThreshold triggers best-effort post-run compaction.
	ContextCompactionSoftThreshold float64
	// ContextCompactionHardThreshold triggers preflight compaction before a model call.
	ContextCompactionHardThreshold float64
	// ContextCompactionRecentTokens protects the recent raw conversation tail.
	ContextCompactionRecentTokens int
	// ContextCompactionSummaryMaxTokens caps the persisted structured summary.
	ContextCompactionSummaryMaxTokens int
	// ContextCompactionTimeout limits one auxiliary summary request.
	ContextCompactionTimeout time.Duration
	// MemoryAdaptiveExtractionMode supports off, shadow, or auto.
	MemoryAdaptiveExtractionMode string
	// MemoryAdaptiveMinConfidence is the commit threshold for model-proposed candidates.
	MemoryAdaptiveMinConfidence float64
	// MemorySyncQueueSize bounds accepted post-response turn synchronization work.
	MemorySyncQueueSize int
	// MemorySyncJobTimeout bounds proposal and commit work for one accepted turn.
	MemorySyncJobTimeout time.Duration
	// MemoryProviderMaxAttempts includes the initial provider operation attempt.
	MemoryProviderMaxAttempts int
	// MemoryProviderRetryBaseDelay is the first exponential retry delay.
	MemoryProviderRetryBaseDelay time.Duration
	RouterMode                   string
	// AutonomousMaxIterations is a mode-owned loop bound, independent of model-call count.
	AutonomousMaxIterations int
	// AutonomousMaxRuntime is folded into the frozen Run Budget for new Autonomous Runs.
	AutonomousMaxRuntime time.Duration
	// AutonomousMaxOutputCharacters bounds accumulated loop text, not provider tokens.
	AutonomousMaxOutputCharacters int
	// AutonomousMaxToolCalls is folded into the frozen Run Budget for new Autonomous Runs.
	AutonomousMaxToolCalls  int
	RecoveryStaleRunTimeout time.Duration
	StoreDriver             string
	DatabaseURL             string
	DataPath                string
	ToolConfigPath          string
	// ToolResultMaxBatchBytes caps aggregate raw Tool results returned by one model Tool-call batch.
	ToolResultMaxBatchBytes int
	// ToolArtifactMaxBytes rejects persistence beyond this hard per-artifact bound.
	ToolArtifactMaxBytes int
	// ToolArtifactPreviewBytes caps model-visible content when a full result becomes an Artifact.
	ToolArtifactPreviewBytes int
	// ToolArtifactRetention is persisted with each Artifact; expiry survives process restarts.
	ToolArtifactRetention time.Duration
	// ToolProgressGuardEnabled controls no-progress detection for new Runs.
	ToolProgressGuardEnabled bool
	// ToolProgressWarnAfter emits model-visible and durable diagnostics after this repeat count.
	ToolProgressWarnAfter int
	// ToolProgressBlockAfter prevents this repeated Tool Call before it consumes Budget.
	ToolProgressBlockAfter int
	// ToolProgressHaltAfter terminates the Turn when blocked attempts continue.
	ToolProgressHaltAfter int
	// VerificationWorkspaceRoot bounds command verifier working directories. Empty disables command execution.
	VerificationWorkspaceRoot string
	// VerificationAllowedCommands is a comma-separated executable allowlist for command verifiers.
	VerificationAllowedCommands string
	// VerificationAllowedHTTPHosts extends HTTP verification beyond loopback hosts.
	VerificationAllowedHTTPHosts string
	// VerificationMaxArtifactBytes caps persisted raw output per verifier while retaining its full byte count and hash.
	VerificationMaxArtifactBytes int
	AllowedOrigins               string
}

func Load() Config {
	loadDotEnv(".env")

	return Config{
		BindAddress:                       getEnv("BIND_ADDRESS", "127.0.0.1"),
		Port:                              getEnv("PORT", "8080"),
		OpenAIAPIKey:                      getEnv("OPENAI_API_KEY", ""),
		OpenAIBaseURL:                     getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIModel:                       getEnv("OPENAI_MODEL", "gpt-4o-mini"),
		EmbeddingBaseURL:                  getEnv("EMBEDDING_BASE_URL", "http://localhost:11434/api/embed"),
		EmbeddingModel:                    getEnv("EMBEDDING_MODEL", "embeddinggemma"),
		EmbeddingDimensions:               getIntEnv("EMBEDDING_DIMENSIONS", 1536),
		OpenAITimeout:                     getDurationEnv("OPENAI_REQUEST_TIMEOUT", 5*time.Minute),
		MaxConcurrentRuns:                 getIntEnv("MAX_CONCURRENT_RUNS", 8),
		RunQueueSize:                      getNonNegativeIntEnv("RUN_QUEUE_SIZE", 32),
		RunQueueWaitTimeout:               getDurationEnv("RUN_QUEUE_WAIT_TIMEOUT", 30*time.Second),
		MaxConcurrentChildRuns:            getIntEnv("MAX_CONCURRENT_CHILD_RUNS", 2),
		MaxChildRunsPerParent:             getIntEnv("MAX_CHILD_RUNS_PER_PARENT", 1),
		ChildRunTimeout:                   getDurationEnv("CHILD_RUN_TIMEOUT", 2*time.Minute),
		ChildRunSummaryMaxCharacters:      getIntEnv("CHILD_RUN_SUMMARY_MAX_CHARACTERS", 4000),
		ChildRunMaxModelCalls:             getIntEnv("CHILD_RUN_MAX_MODEL_CALLS", 8),
		ChildRunMaxTotalTokens:            getIntEnv("CHILD_RUN_MAX_TOTAL_TOKENS", 12000),
		ChildRunMaxToolCalls:              getIntEnv("CHILD_RUN_MAX_TOOL_CALLS", 8),
		MaxConcurrentModelRequests:        getIntEnv("MAX_CONCURRENT_MODEL_REQUESTS", 8),
		ModelRequestsPerMinute:            getNonNegativeIntEnv("MODEL_REQUESTS_PER_MINUTE", 60),
		ModelTokensPerMinute:              getNonNegativeIntEnv("MODEL_TOKENS_PER_MINUTE", 120000),
		ModelRetryMaxAttempts:             getIntEnv("MODEL_RETRY_MAX_ATTEMPTS", 3),
		ModelRetryBaseDelay:               getDurationEnv("MODEL_RETRY_BASE_DELAY", 500*time.Millisecond),
		ModelRetryMaxDelay:                getDurationEnv("MODEL_RETRY_MAX_DELAY", 5*time.Second),
		ModelRequestCaptureMode:           normalizeModelRequestCaptureMode(getEnv("MODEL_REQUEST_CAPTURE_MODE", "metadata_only")),
		ModelRequestCaptureMaxBytes:       getIntEnv("MODEL_REQUEST_CAPTURE_MAX_BYTES", 262144),
		ModelRequestCaptureRetention:      getDurationEnv("MODEL_REQUEST_CAPTURE_RETENTION", 7*24*time.Hour),
		RunMaxModelCalls:                  getNonNegativeIntEnv("RUN_MAX_MODEL_CALLS", 32),
		RunMaxPromptTokens:                getNonNegativeIntEnv("RUN_MAX_PROMPT_TOKENS", 200000),
		RunMaxCompletionTokens:            getNonNegativeIntEnv("RUN_MAX_COMPLETION_TOKENS", 50000),
		RunMaxTotalTokens:                 getNonNegativeIntEnv("RUN_MAX_TOTAL_TOKENS", 250000),
		RunMaxToolCalls:                   getNonNegativeIntEnv("RUN_MAX_TOOL_CALLS", 50),
		RunMaxRuntime:                     getDurationEnv("RUN_MAX_RUNTIME", 15*time.Minute),
		RunMaxEstimatedCostMicros:         getUSDAsMicrosEnv("RUN_MAX_ESTIMATED_COST_USD", 0),
		ModelInputCostPerMillionMicros:    getUSDAsMicrosEnv("MODEL_INPUT_COST_PER_MILLION_TOKENS_USD", 0),
		ModelOutputCostPerMillionMicros:   getUSDAsMicrosEnv("MODEL_OUTPUT_COST_PER_MILLION_TOKENS_USD", 0),
		ModelContextWindowTokens:          getIntEnv("MODEL_CONTEXT_WINDOW_TOKENS", 128000),
		ModelOutputReserveTokens:          getIntEnv("MODEL_OUTPUT_RESERVE_TOKENS", 8192),
		ContextSafetyMarginTokens:         getIntEnv("CONTEXT_SAFETY_MARGIN_TOKENS", 4096),
		ContextHistoryMaxTokens:           getIntEnv("CONTEXT_HISTORY_MAX_TOKENS", 64000),
		ContextMemoryMaxTokens:            getIntEnv("CONTEXT_MEMORY_MAX_TOKENS", 8000),
		ContextKnowledgeMaxTokens:         getIntEnv("CONTEXT_KNOWLEDGE_MAX_TOKENS", 16000),
		ContextToolResultMaxTokens:        getIntEnv("CONTEXT_TOOL_RESULT_MAX_TOKENS", 2000),
		ContextHistoryRetrievalMaxResults: getIntEnv("CONTEXT_HISTORY_RETRIEVAL_MAX_RESULTS", 8),
		ContextHistoryRetrievalMaxChars:   getIntEnv("CONTEXT_HISTORY_RETRIEVAL_MAX_CHARACTERS", 12000),
		ContextHistoryRetrievalMaxTokens:  getIntEnv("CONTEXT_HISTORY_RETRIEVAL_MAX_TOKENS", 3000),
		ContextHistoryRetrievalWindow:     getNonNegativeIntEnv("CONTEXT_HISTORY_RETRIEVAL_WINDOW", 1),
		ContextCompactionMode:             normalizeCompactionMode(getEnv("CONTEXT_COMPACTION_MODE", "auto")),
		ContextCompactionSoftThreshold:    getFloatEnv("CONTEXT_COMPACTION_SOFT_THRESHOLD", 0.70),
		ContextCompactionHardThreshold:    getFloatEnv("CONTEXT_COMPACTION_HARD_THRESHOLD", 0.85),
		ContextCompactionRecentTokens:     getIntEnv("CONTEXT_COMPACTION_RECENT_TOKENS", 16000),
		ContextCompactionSummaryMaxTokens: getIntEnv("CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS", 2000),
		ContextCompactionTimeout:          getDurationEnv("CONTEXT_COMPACTION_TIMEOUT", 45*time.Second),
		MemoryAdaptiveExtractionMode:      normalizeAdaptiveMemoryMode(getEnv("MEMORY_ADAPTIVE_EXTRACTION_MODE", "shadow")),
		MemoryAdaptiveMinConfidence:       getUnitFloatEnv("MEMORY_ADAPTIVE_MIN_CONFIDENCE", 0.85),
		MemorySyncQueueSize:               getIntEnv("MEMORY_SYNC_QUEUE_SIZE", 256),
		MemorySyncJobTimeout:              getDurationEnv("MEMORY_SYNC_JOB_TIMEOUT", 30*time.Second),
		MemoryProviderMaxAttempts:         getIntEnv("MEMORY_PROVIDER_MAX_ATTEMPTS", 3),
		MemoryProviderRetryBaseDelay:      getDurationEnv("MEMORY_PROVIDER_RETRY_BASE_DELAY", 100*time.Millisecond),
		RouterMode:                        normalizeRouterMode(getEnv("ROUTER_MODE", "auto")),
		AutonomousMaxIterations:           getIntEnv("AUTONOMOUS_MAX_ITERATIONS", 5),
		AutonomousMaxRuntime:              getAutonomousRuntime(),
		AutonomousMaxOutputCharacters:     getIntEnv("AUTONOMOUS_MAX_OUTPUT_CHARS", 60000),
		AutonomousMaxToolCalls:            getIntEnv("AUTONOMOUS_MAX_TOOL_CALLS", 20),
		RecoveryStaleRunTimeout:           getDurationEnv("RECOVERY_STALE_RUN_TIMEOUT", 60*time.Second),
		StoreDriver:                       normalizeStoreDriver(getEnv("STORE_DRIVER", "file")),
		DatabaseURL:                       getEnv("DATABASE_URL", ""),
		DataPath:                          getEnv("DATA_PATH", ".data/agentflow.json"),
		ToolConfigPath:                    getEnv("TOOL_CONFIG_PATH", ".data/tools.json"),
		ToolResultMaxBatchBytes:           getIntEnv("TOOL_RESULT_MAX_BATCH_BYTES", 8000),
		ToolArtifactMaxBytes:              getIntEnv("TOOL_ARTIFACT_MAX_BYTES", 5*1024*1024),
		ToolArtifactPreviewBytes:          getIntEnv("TOOL_ARTIFACT_PREVIEW_BYTES", 1000),
		ToolArtifactRetention:             getDurationEnv("TOOL_ARTIFACT_RETENTION", 7*24*time.Hour),
		ToolProgressGuardEnabled:          getBoolEnv("TOOL_PROGRESS_GUARD_ENABLED", true),
		ToolProgressWarnAfter:             getIntEnv("TOOL_PROGRESS_WARN_AFTER", 2),
		ToolProgressBlockAfter:            getIntEnv("TOOL_PROGRESS_BLOCK_AFTER", 4),
		ToolProgressHaltAfter:             getIntEnv("TOOL_PROGRESS_HALT_AFTER", 5),
		VerificationWorkspaceRoot:         getEnv("VERIFICATION_WORKSPACE_ROOT", ""),
		VerificationAllowedCommands:       getEnv("VERIFICATION_ALLOWED_COMMANDS", ""),
		VerificationAllowedHTTPHosts:      getEnv("VERIFICATION_ALLOWED_HTTP_HOSTS", ""),
		VerificationMaxArtifactBytes:      getIntEnv("VERIFICATION_MAX_ARTIFACT_BYTES", 65536),
		AllowedOrigins:                    getEnv("ALLOWED_ORIGINS", "http://localhost:3000"),
	}
}

func normalizeStoreDriver(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "postgres", "postgresql":
		return "postgres"
	default:
		return "file"
	}
}

func normalizeRouterMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "query_match":
		return "query_match"
	default:
		return "auto"
	}
}

func normalizeCompactionMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "off") {
		return "off"
	}
	return "auto"
}

func normalizeModelRequestCaptureMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "redacted", "full":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "metadata_only"
	}
}

func normalizeAdaptiveMemoryMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "shadow", "auto":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "off"
	}
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func getBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getIntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getNonNegativeIntEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func getUSDAsMicrosEnv(key string, fallback float64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return int64(math.Round(fallback * 1_000_000))
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		parsed = fallback
	}
	return int64(math.Round(parsed * 1_000_000))
}

func getFloatEnv(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func getUnitFloatEnv(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 || parsed > 1 {
		return fallback
	}
	return parsed
}

func getAutonomousRuntime() time.Duration {
	if duration := getDurationEnv("AUTONOMOUS_MAX_RUNTIME", 0); duration > 0 {
		return duration
	}
	if seconds := getIntEnv("AUTONOMOUS_MAX_RUNTIME_SECONDS", 300); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 5 * time.Minute
}

func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
