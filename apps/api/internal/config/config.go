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
	RouterMode                  string
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
		MaxConcurrentModelRequests:        getIntEnv("MAX_CONCURRENT_MODEL_REQUESTS", 8),
		ModelRequestsPerMinute:            getNonNegativeIntEnv("MODEL_REQUESTS_PER_MINUTE", 60),
		ModelTokensPerMinute:              getNonNegativeIntEnv("MODEL_TOKENS_PER_MINUTE", 120000),
		ModelRetryMaxAttempts:             getIntEnv("MODEL_RETRY_MAX_ATTEMPTS", 3),
		ModelRetryBaseDelay:               getDurationEnv("MODEL_RETRY_BASE_DELAY", 500*time.Millisecond),
		ModelRetryMaxDelay:                getDurationEnv("MODEL_RETRY_MAX_DELAY", 5*time.Second),
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
		ContextCompactionMode:             normalizeCompactionMode(getEnv("CONTEXT_COMPACTION_MODE", "auto")),
		ContextCompactionSoftThreshold:    getFloatEnv("CONTEXT_COMPACTION_SOFT_THRESHOLD", 0.70),
		ContextCompactionHardThreshold:    getFloatEnv("CONTEXT_COMPACTION_HARD_THRESHOLD", 0.85),
		ContextCompactionRecentTokens:     getIntEnv("CONTEXT_COMPACTION_RECENT_TOKENS", 16000),
		ContextCompactionSummaryMaxTokens: getIntEnv("CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS", 2000),
		ContextCompactionTimeout:          getDurationEnv("CONTEXT_COMPACTION_TIMEOUT", 45*time.Second),
		MemoryAdaptiveExtractionMode:      normalizeAdaptiveMemoryMode(getEnv("MEMORY_ADAPTIVE_EXTRACTION_MODE", "shadow")),
		MemoryAdaptiveMinConfidence:       getUnitFloatEnv("MEMORY_ADAPTIVE_MIN_CONFIDENCE", 0.85),
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
