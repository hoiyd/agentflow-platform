package tools

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"agentflow-platform/apps/api/internal/toolpolicy"
)

type Catalog struct {
	mu             sync.RWMutex
	bindings       map[string]Binding
	enabled        map[string]bool
	securityPolicy toolpolicy.Policy
}

func NewCatalog(bindings ...Binding) (*Catalog, error) {
	return NewCatalogWithPolicy(toolpolicy.DefaultPolicy(), bindings...)
}

func NewCatalogWithPolicy(policy toolpolicy.Policy, bindings ...Binding) (*Catalog, error) {
	policy = toolpolicy.NormalizePolicy(policy)
	if err := toolpolicy.ValidatePolicy(policy); err != nil {
		return nil, fmt.Errorf("invalid Tool security policy: %w", err)
	}
	catalog := &Catalog{
		bindings:       map[string]Binding{},
		enabled:        map[string]bool{},
		securityPolicy: policy,
	}
	for _, binding := range bindings {
		if err := catalog.Register(binding); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func DefaultCatalog() *Catalog {
	catalog, err := NewCatalogWithPolicy(toolpolicy.DefaultPolicy(),
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
	binding.Descriptor.Name = name
	binding.Descriptor.Security = toolpolicy.NormalizeCapability(binding.Descriptor.Security)
	if err := toolpolicy.ValidateCapability(binding.Descriptor.Security); err != nil {
		return fmt.Errorf("tool %q has invalid security capability: %w", name, err)
	}
	if binding.Descriptor.SideEffect.Mode == SideEffectExternal && binding.Descriptor.Security.SideEffect == toolpolicy.SideEffectNone {
		return fmt.Errorf("tool %q external side effect requires an explicit security side-effect class", name)
	}
	if binding.Descriptor.SideEffect.Mode != SideEffectExternal && (binding.Descriptor.SideEffect.RetryWithSameKey || binding.Descriptor.SideEffect.Compensate || binding.Reconciliation.RetryWithSameKey != nil || binding.Reconciliation.Compensate != nil) {
		return fmt.Errorf("tool %q reconciliation requires external side-effect mode", name)
	}
	if binding.Descriptor.SideEffect.RetryWithSameKey != (binding.Reconciliation.RetryWithSameKey != nil) {
		return fmt.Errorf("tool %q retry capability and callback must match", name)
	}
	if binding.Descriptor.SideEffect.Compensate != (binding.Reconciliation.Compensate != nil) {
		return fmt.Errorf("tool %q compensation capability and callback must match", name)
	}
	if binding.Descriptor.SideEffect.Compensate && binding.Descriptor.Security.Reversibility == toolpolicy.Irreversible {
		return fmt.Errorf("tool %q irreversible side effect cannot declare compensation", name)
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
	contract, err := compileArgumentContract(&binding.Descriptor)
	if err != nil {
		return fmt.Errorf("tool %q has invalid parameters: %w", name, err)
	}
	if _, exists := c.bindings[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	binding.contract = contract
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

func (c *Catalog) SecurityPolicy() toolpolicy.Policy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return toolpolicy.NormalizePolicy(c.securityPolicy)
}

// CloneWith creates an isolated catalog while preserving installed bindings
// and enablement. Runtime-owned harness tools can be added without mutating the
// user-configurable Manager catalog.
func (c *Catalog) CloneWith(bindings ...Binding) (*Catalog, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cloned, err := NewCatalogWithPolicy(c.securityPolicy)
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
