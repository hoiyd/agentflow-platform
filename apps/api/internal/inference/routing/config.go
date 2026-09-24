package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type RouteFile struct {
	Routes []RouteConfig `json:"routes"`
}

// RouteConfig is secret-free. CredentialEnvironment names the environment
// variable resolved only when the provider client is constructed.
type RouteConfig struct {
	ID                    string                   `json:"id"`
	BaseURL               string                   `json:"base_url"`
	Model                 string                   `json:"model"`
	CredentialEnvironment string                   `json:"credential_environment"`
	RequestTimeoutSeconds int                      `json:"request_timeout_seconds"`
	Capabilities          Capabilities             `json:"capabilities"`
	GenerationPolicy      *domain.GenerationPolicy `json:"generation_policy,omitempty"`
	ContextWindowTokens   int                      `json:"context_window_tokens"`
	MaxOutputTokens       int                      `json:"max_output_tokens"`
	Priority              int                      `json:"priority"`
	Pricing               Pricing                  `json:"pricing"`
}

func LoadRouteFile(path string) (RouteFile, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return RouteFile{}, fmt.Errorf("MODEL_ROUTE_CONFIG_PATH is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RouteFile{}, fmt.Errorf("read model route config: %w", err)
	}
	var config RouteFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return RouteFile{}, fmt.Errorf("parse model route config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return RouteFile{}, fmt.Errorf("parse model route config: unexpected trailing data")
	}
	if len(config.Routes) == 0 {
		return RouteFile{}, fmt.Errorf("parse model route config: at least one route is required")
	}
	for _, route := range config.Routes {
		if route.RequestTimeoutSeconds <= 0 {
			return RouteFile{}, fmt.Errorf("parse model route config: route %q request_timeout_seconds must be positive", route.ID)
		}
	}
	return config, nil
}

func (c RouteConfig) Descriptor(provider string) Descriptor {
	return Descriptor{
		ID: c.ID, Provider: provider, Model: c.Model, Endpoint: c.BaseURL,
		Capabilities: c.Capabilities, ContextWindowTokens: c.ContextWindowTokens,
		GenerationPolicy: c.GenerationPolicy,
		MaxOutputTokens:  c.MaxOutputTokens, Priority: c.Priority, Pricing: c.Pricing,
		CredentialEnvironment: c.CredentialEnvironment,
	}
}
