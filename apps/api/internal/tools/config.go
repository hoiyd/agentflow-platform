package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	EnabledTools []string          `json:"enabled_tools"`
	MCPServers   []MCPServerConfig `json:"mcp_servers"`
}

type MCPServerConfig struct {
	ID         string            `json:"id"`
	Enabled    bool              `json:"enabled"`
	Transport  string            `json:"transport"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	WorkingDir string            `json:"cwd,omitempty"`
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

func SaveConfig(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	bytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tools config: %w", err)
	}
	bytes = append(bytes, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create tools config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tools-*.json")
	if err != nil {
		return fmt.Errorf("create temporary tools config: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temporary tools config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary tools config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace tools config: %w", err)
	}
	return nil
}
