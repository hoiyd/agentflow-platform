package tools

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu         sync.RWMutex
	configPath string
	config     Config
	registry   *Registry
	mcpClient  MCPClient
	modTime    time.Time
}

func NewManager(ctx context.Context, configPath string, mcpClient MCPClient) (*Manager, error) {
	cfg, modTime, err := loadConfigWithModTime(configPath)
	if err != nil {
		return nil, err
	}
	registry, err := BuildRegistry(ctx, BuildOptions{Config: cfg, MCPClient: mcpClient})
	if err != nil {
		return nil, err
	}
	return &Manager{
		configPath: strings.TrimSpace(configPath),
		config:     cfg,
		registry:   registry,
		mcpClient:  mcpClient,
		modTime:    modTime,
	}, nil
}

func (m *Manager) Registry(ctx context.Context) (*Registry, error) {
	if err := m.ReloadIfChanged(ctx); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry, nil
}

func (m *Manager) List(ctx context.Context) ([]ToolInfo, error) {
	if err := m.ReloadIfChanged(ctx); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry.List(), nil
}

func (m *Manager) SetEnabled(ctx context.Context, name string, enabled bool) ([]ToolInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("tool name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.reloadIfChangedLocked(ctx); err != nil {
		return nil, err
	}

	nextConfig := m.config
	nextConfig.EnabledTools = nextEnabledNames(m.registry.List(), name, enabled)
	if nextConfig.EnabledTools == nil {
		return nil, errors.New("tool " + name + " not found")
	}
	if err := SaveConfig(m.configPath, nextConfig); err != nil {
		return nil, err
	}
	if err := m.registry.SetEnabled(name, enabled); err != nil {
		return nil, err
	}
	m.config = nextConfig
	m.modTime = configModTime(m.configPath)
	return m.registry.List(), nil
}

func (m *Manager) ReloadIfChanged(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadIfChangedLocked(ctx)
}

func (m *Manager) reloadIfChangedLocked(ctx context.Context) error {
	modTime := configModTime(m.configPath)
	if modTime.Equal(m.modTime) {
		return nil
	}

	cfg, loadedModTime, err := loadConfigWithModTime(m.configPath)
	if err != nil {
		return err
	}
	registry, err := BuildRegistry(ctx, BuildOptions{Config: cfg, MCPClient: m.mcpClient})
	if err != nil {
		return err
	}
	m.config = cfg
	m.registry = registry
	m.modTime = loadedModTime
	return nil
}

func loadConfigWithModTime(path string) (Config, time.Time, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return Config{}, time.Time{}, err
	}
	return cfg, configModTime(path), nil
}

func configModTime(path string) time.Time {
	if strings.TrimSpace(path) == "" {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func nextEnabledNames(items []ToolInfo, target string, enabled bool) []string {
	found := false
	names := make([]string, 0, len(items))
	for _, item := range items {
		itemEnabled := item.Enabled
		if item.Name == target {
			found = true
			itemEnabled = enabled
		}
		if itemEnabled {
			names = append(names, item.Name)
		}
	}
	if !found {
		return nil
	}
	sort.Strings(names)
	return names
}
