package tools

import (
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
	catalog    *Catalog
	modTime    time.Time
}

func NewManager(configPath string) (*Manager, error) {
	cfg, modTime, err := loadConfigWithModTime(configPath)
	if err != nil {
		return nil, err
	}
	catalog, err := BuildCatalog(cfg)
	if err != nil {
		return nil, err
	}
	return &Manager{
		configPath: strings.TrimSpace(configPath), config: cfg,
		catalog: catalog, modTime: modTime,
	}, nil
}

func (m *Manager) Catalog() (*Catalog, error) {
	if err := m.ReloadIfChanged(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalog, nil
}

func (m *Manager) List() ([]ToolInfo, error) {
	if err := m.ReloadIfChanged(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalog.List(), nil
}

func (m *Manager) SetEnabled(name string, enabled bool) ([]ToolInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("tool name is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reloadIfChangedLocked(); err != nil {
		return nil, err
	}
	nextConfig := m.config
	nextConfig.EnabledTools = nextEnabledNames(m.catalog.List(), name, enabled)
	if nextConfig.EnabledTools == nil {
		return nil, errors.New("tool " + name + " not found")
	}
	if err := SaveConfig(m.configPath, nextConfig); err != nil {
		return nil, err
	}
	if err := m.catalog.SetEnabled(name, enabled); err != nil {
		return nil, err
	}
	m.config = nextConfig
	m.modTime = configModTime(m.configPath)
	return m.catalog.List(), nil
}

func (m *Manager) ReloadIfChanged() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadIfChangedLocked()
}

func (m *Manager) reloadIfChangedLocked() error {
	modTime := configModTime(m.configPath)
	if modTime.Equal(m.modTime) {
		return nil
	}
	cfg, loadedModTime, err := loadConfigWithModTime(m.configPath)
	if err != nil {
		return err
	}
	catalog, err := BuildCatalog(cfg)
	if err != nil {
		return err
	}
	m.config, m.catalog, m.modTime = cfg, catalog, loadedModTime
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
			found, itemEnabled = true, enabled
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
