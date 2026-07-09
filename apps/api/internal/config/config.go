package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                          string
	OpenAIAPIKey                  string
	OpenAIBaseURL                 string
	OpenAIModel                   string
	EmbeddingBaseURL              string
	EmbeddingModel                string
	EmbeddingDimensions           int
	OpenAITimeout                 time.Duration
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
