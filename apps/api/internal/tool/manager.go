package tool

import (
	"os"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu         sync.RWMutex
	configPath string
	config     Config
	catalog    *Catalog
	bindings   []Binding
	modTime    time.Time
}

func NewManager(configPath string, bindings ...Binding) (*Manager, error) {
	cfg, modTime, err := loadConfigWithModTime(configPath)
	if err != nil {
		return nil, err
	}
	catalog, err := BuildCatalog(cfg, bindings...)
	if err != nil {
		return nil, err
	}
	return &Manager{
		configPath: strings.TrimSpace(configPath), config: cfg,
		catalog: catalog, bindings: append([]Binding(nil), bindings...), modTime: modTime,
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

// ReplaceBindings publishes a newly discovered catalog atomically. Runs that
// already captured the previous catalog retain their frozen definitions.
func (m *Manager) ReplaceBindings(bindings []Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reloadIfChangedLocked(); err != nil {
		return err
	}
	catalog, err := BuildCatalog(m.config, bindings...)
	if err != nil {
		return err
	}
	m.bindings = append([]Binding(nil), bindings...)
	m.catalog = catalog
	return nil
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
	catalog, err := BuildCatalog(cfg, m.bindings...)
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
