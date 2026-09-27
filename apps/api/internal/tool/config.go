package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentflow-platform/apps/api/internal/tool/policy"
)

type Config struct {
	EnabledTools   []string      `json:"enabled_tools"`
	SecurityPolicy policy.Policy `json:"security_policy"`
}

func DefaultConfig() Config {
	securityPolicy := policy.DefaultPolicy()
	enabled := []string{}
	for _, builtin := range builtinTools() {
		if builtin.defaultEnabled {
			enabled = append(enabled, builtin.binding.Descriptor.Name)
		}
		if builtin.defaultRuleID != "" {
			securityPolicy.Rules = append(securityPolicy.Rules, policy.Rule{
				ID: builtin.defaultRuleID, Tool: builtin.binding.Descriptor.Name,
				Action: policy.ActionAllow, Capability: builtin.binding.Descriptor.Security,
			})
		}
	}
	return Config{
		EnabledTools:   enabled,
		SecurityPolicy: securityPolicy,
	}
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read tools config: %w", err)
	}

	cfg := DefaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse tools config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Config{}, fmt.Errorf("parse tools config: unexpected trailing data")
	}
	if cfg.EnabledTools == nil {
		cfg.EnabledTools = DefaultConfig().EnabledTools
	}
	cfg.SecurityPolicy = policy.NormalizePolicy(cfg.SecurityPolicy)
	if err := policy.ValidatePolicy(cfg.SecurityPolicy); err != nil {
		return Config{}, fmt.Errorf("validate Tool security policy: %w", err)
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tools config: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create tools config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tools-*.json")
	if err != nil {
		return fmt.Errorf("create temporary tools config: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
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
