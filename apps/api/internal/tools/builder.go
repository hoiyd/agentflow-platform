package tools

import (
	"fmt"
	"strings"
)

func BuildCatalog(config Config) (*Catalog, error) {
	catalog := DefaultCatalog()
	enabled := make(map[string]bool, len(config.EnabledTools))
	for _, name := range config.EnabledTools {
		if name = strings.TrimSpace(name); name != "" {
			enabled[name] = true
		}
	}
	installed := make(map[string]bool)
	for _, item := range catalog.List() {
		installed[item.Name] = true
		if err := catalog.SetEnabled(item.Name, enabled[item.Name]); err != nil {
			return nil, err
		}
	}
	for name := range enabled {
		if !installed[name] {
			return nil, fmt.Errorf("enabled tool %q is not installed", name)
		}
	}
	return catalog, nil
}
