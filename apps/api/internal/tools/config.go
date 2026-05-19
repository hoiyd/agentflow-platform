package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	EnabledTools []string          `json:"enabled_tools"`
	MCPServers   []MCPServerConfig `json:"mcp_servers"`
}

type MCPServerConfig struct {
	ID        string            `json:"id"`
	Enabled   bool              `json:"enabled"`
	Transport string            `json:"transport"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
}

func DefaultConfig() Config {
	return Config{
		EnabledTools: []string{"calculator", "get_current_time", "mock_web_search"},
	}
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultConfig(), nil
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read tools config: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse tools config: %w", err)
	}
	if cfg.EnabledTools == nil {
		cfg.EnabledTools = DefaultConfig().EnabledTools
	}
	return cfg, nil
}
