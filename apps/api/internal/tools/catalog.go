package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Catalog struct {
	mu       sync.RWMutex
	bindings map[string]Binding
	enabled  map[string]bool
}

func NewCatalog(bindings ...Binding) (*Catalog, error) {
	catalog := &Catalog{
		bindings: map[string]Binding{},
		enabled:  map[string]bool{},
	}
	for _, binding := range bindings {
		if err := catalog.Register(binding); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func DefaultCatalog() *Catalog {
	catalog, err := NewCatalog(
		CalculatorTool(),
		CurrentTimeTool(),
	)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c *Catalog) Register(binding Binding) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	name := strings.TrimSpace(binding.Descriptor.Name)
	if name == "" {
		return errors.New("tool name is required")
	}
	if binding.Handler == nil {
		return fmt.Errorf("tool %q handler is required", name)
	}
	if binding.Descriptor.Parameters == nil {
		return fmt.Errorf("tool %q parameters are required", name)
	}
	if _, err := json.Marshal(binding.Descriptor.Parameters); err != nil {
		return fmt.Errorf("tool %q parameters must be JSON-compatible: %w", name, err)
	}
	concurrency := binding.Descriptor.Concurrency
	switch concurrency.Mode {
	case "", ConcurrencySerial, ConcurrencyReadOnly:
		if strings.TrimSpace(concurrency.KeyArgument) != "" {
			return fmt.Errorf("tool %q concurrency key argument requires keyed mode", name)
		}
	case ConcurrencyKeyed:
		if strings.TrimSpace(concurrency.KeyArgument) == "" {
			return fmt.Errorf("tool %q keyed concurrency requires a key argument", name)
		}
		binding.Descriptor.Concurrency.KeyArgument = strings.TrimSpace(concurrency.KeyArgument)
	default:
		return fmt.Errorf("tool %q has unsupported concurrency mode %q", name, concurrency.Mode)
	}
	if _, exists := c.bindings[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	binding.Descriptor.Name = name
	c.bindings[name] = binding
	c.enabled[name] = true
	return nil
}

func (c *Catalog) Resolve(name string) (Binding, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.enabled[name] {
		return Binding{}, false
	}
	binding, ok := c.bindings[name]
	return binding, ok
}

func (c *Catalog) Installed(name string) (Binding, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	binding, ok := c.bindings[name]
	return binding, ok
}

func (c *Catalog) SetEnabled(name string, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.bindings[name]; !ok {
		return fmt.Errorf("tool %q not found", name)
	}
	c.enabled[name] = enabled
	return nil
}

func (c *Catalog) List() []ToolInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := c.sortedNamesLocked(false)
	items := make([]ToolInfo, 0, len(names))
	for _, name := range names {
		descriptor := c.bindings[name].Descriptor
		items = append(items, ToolInfo{
			Name: descriptor.Name, Description: descriptor.Description,
			Parameters: descriptor.Parameters, Enabled: c.enabled[name],
		})
	}
	return items
}

func (c *Catalog) EnabledNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sortedNamesLocked(true)
}

// CloneWith creates an isolated catalog while preserving installed bindings
// and enablement. Runtime-owned harness tools can be added without mutating the
// user-configurable Manager catalog.
func (c *Catalog) CloneWith(bindings ...Binding) (*Catalog, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cloned, err := NewCatalog()
	if err != nil {
		return nil, err
	}
	for _, name := range c.sortedNamesLocked(false) {
		if err := cloned.Register(c.bindings[name]); err != nil {
			return nil, err
		}
		if err := cloned.SetEnabled(name, c.enabled[name]); err != nil {
			return nil, err
		}
	}
	for _, binding := range bindings {
		if err := cloned.Register(binding); err != nil {
			return nil, err
		}
	}
	return cloned, nil
}

func (c *Catalog) Definitions() []map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := c.sortedNamesLocked(true)
	definitions := make([]map[string]any, 0, len(names))
	for _, name := range names {
		descriptor := c.bindings[name].Descriptor
		definitions = append(definitions, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": descriptor.Name, "description": descriptor.Description,
				"parameters": descriptor.Parameters,
			},
		})
	}
	return definitions
}

func (c *Catalog) sortedNamesLocked(enabledOnly bool) []string {
	names := make([]string, 0, len(c.bindings))
	for name := range c.bindings {
		if !enabledOnly || c.enabled[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
