package config

import (
	"bufio"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port           string
	OpenAIAPIKey   string
	OpenAIBaseURL  string
	OpenAIModel    string
	OpenAITimeout  time.Duration
	DataPath       string
	ToolConfigPath string
	AllowedOrigins string
}

func Load() Config {
	loadDotEnv(".env")

	return Config{
		Port:           getEnv("PORT", "8080"),
		OpenAIAPIKey:   getEnv("OPENAI_API_KEY", ""),
		OpenAIBaseURL:  getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIModel:    getEnv("OPENAI_MODEL", "gpt-4o-mini"),
		OpenAITimeout:  getDurationEnv("OPENAI_REQUEST_TIMEOUT", 5*time.Minute),
		DataPath:       getEnv("DATA_PATH", ".data/agentflow.json"),
		ToolConfigPath: getEnv("TOOL_CONFIG_PATH", ".data/tools.json"),
		AllowedOrigins: getEnv("ALLOWED_ORIGINS", "http://localhost:3000"),
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
