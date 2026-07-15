package config

import (
	"bufio"
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
	ModelRetryMaxDelay            time.Duration
	RouterMode                    string
	AutonomousMaxIterations       int
	AutonomousMaxRuntime          time.Duration
	AutonomousMaxOutputCharacters int
	AutonomousMaxToolCalls        int
	RecoveryStaleRunTimeout       time.Duration
	StoreDriver                   string
	DatabaseURL                   string
	DataPath                      string
	ToolConfigPath                string
	AllowedOrigins                string
}

func Load() Config {
	loadDotEnv(".env")

	return Config{
		Port:                          getEnv("PORT", "8080"),
		OpenAIAPIKey:                  getEnv("OPENAI_API_KEY", ""),
		OpenAIBaseURL:                 getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIModel:                   getEnv("OPENAI_MODEL", "gpt-4o-mini"),
		EmbeddingBaseURL:              getEnv("EMBEDDING_BASE_URL", "http://localhost:11434/api/embed"),
		EmbeddingModel:                getEnv("EMBEDDING_MODEL", "embeddinggemma"),
		EmbeddingDimensions:           getIntEnv("EMBEDDING_DIMENSIONS", 1536),
		OpenAITimeout:                 getDurationEnv("OPENAI_REQUEST_TIMEOUT", 5*time.Minute),
		MaxConcurrentRuns:             getIntEnv("MAX_CONCURRENT_RUNS", 8),
		RunQueueSize:                  getNonNegativeIntEnv("RUN_QUEUE_SIZE", 32),
		RunQueueWaitTimeout:           getDurationEnv("RUN_QUEUE_WAIT_TIMEOUT", 30*time.Second),
		MaxConcurrentModelRequests:    getIntEnv("MAX_CONCURRENT_MODEL_REQUESTS", 8),
		ModelRequestsPerMinute:        getNonNegativeIntEnv("MODEL_REQUESTS_PER_MINUTE", 60),
		ModelTokensPerMinute:          getNonNegativeIntEnv("MODEL_TOKENS_PER_MINUTE", 120000),
		ModelRetryMaxAttempts:         getIntEnv("MODEL_RETRY_MAX_ATTEMPTS", 3),
		ModelRetryBaseDelay:           getDurationEnv("MODEL_RETRY_BASE_DELAY", 500*time.Millisecond),
		ModelRetryMaxDelay:            getDurationEnv("MODEL_RETRY_MAX_DELAY", 5*time.Second),
		RouterMode:                    normalizeRouterMode(getEnv("ROUTER_MODE", "auto")),
		AutonomousMaxIterations:       getIntEnv("AUTONOMOUS_MAX_ITERATIONS", 5),
		AutonomousMaxRuntime:          getAutonomousRuntime(),
		AutonomousMaxOutputCharacters: getIntEnv("AUTONOMOUS_MAX_OUTPUT_CHARS", 60000),
		AutonomousMaxToolCalls:        getIntEnv("AUTONOMOUS_MAX_TOOL_CALLS", 20),
		RecoveryStaleRunTimeout:       getDurationEnv("RECOVERY_STALE_RUN_TIMEOUT", 60*time.Second),
		StoreDriver:                   normalizeStoreDriver(getEnv("STORE_DRIVER", "file")),
		DatabaseURL:                   getEnv("DATABASE_URL", ""),
		DataPath:                      getEnv("DATA_PATH", ".data/agentflow.json"),
		ToolConfigPath:                getEnv("TOOL_CONFIG_PATH", ".data/tools.json"),
		AllowedOrigins:                getEnv("ALLOWED_ORIGINS", "http://localhost:3000"),
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
