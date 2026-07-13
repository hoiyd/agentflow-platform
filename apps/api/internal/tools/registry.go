package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Handler     Handler
	Source      string
	SourceID    string
}

type Handler func(ctx context.Context, args json.RawMessage) (any, error)

type Registry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	enabled map[string]bool
}

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Source      string         `json:"source"`
	SourceID    string         `json:"source_id,omitempty"`
	Enabled     bool           `json:"enabled"`
}

type ExecutionResult struct {
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	Result     any             `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	DurationMS int64           `json:"duration_ms"`
}

func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{
		tools:   map[string]Tool{},
		enabled: map[string]bool{},
	}
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func DefaultRegistry() *Registry {
	registry, err := NewRegistry(
		CalculatorTool(),
		CurrentTimeTool(),
		MockWebSearchTool(),
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tool.Name == "" {
		return errors.New("tool name is required")
	}
	if tool.Handler == nil {
		return fmt.Errorf("tool %q handler is required", tool.Name)
	}
	if tool.Source == "" {
		tool.Source = "builtin"
	}
	if _, exists := r.tools[tool.Name]; exists {
		return fmt.Errorf("tool %q already registered", tool.Name)
	}
	r.tools[tool.Name] = tool
	r.enabled[tool.Name] = true
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.enabled[name] {
		return Tool{}, false
	}
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Installed(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) SetEnabled(name string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tools[name]; !ok {
		return fmt.Errorf("tool %q not found", name)
	}
	r.enabled[name] = enabled
	return nil
}

func (r *Registry) List() []ToolInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]ToolInfo, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		items = append(items, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Source:      tool.Source,
			SourceID:    tool.SourceID,
			Enabled:     r.enabled[name],
		})
	}
	return items
}

func (r *Registry) EnabledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if r.enabled[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *Registry) EnabledSubset(names []string) (*Registry, error) {
	subset, err := NewRegistry()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		tool, ok := r.Get(name)
		if !ok {
			continue
		}
		if err := subset.Register(tool); err != nil {
			return nil, err
		}
	}
	return subset, nil
}

func (r *Registry) Definitions() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if r.enabled[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	definitions := make([]map[string]any, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		definitions = append(definitions, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	return definitions
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) ExecutionResult {
	started := time.Now()
	result := ExecutionResult{
		Tool:      name,
		Arguments: append(json.RawMessage(nil), args...),
	}

	tool, ok := r.Get(name)
	if !ok {
		result.Error = fmt.Sprintf("tool %q not found", name)
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}

	value, err := tool.Handler(ctx, args)
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Result = value
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result
}

func ObjectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}
