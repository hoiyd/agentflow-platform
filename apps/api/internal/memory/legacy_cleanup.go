package memory

import (
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

type LegacyCleanupStore interface {
	ListLegacyMessageMemories() ([]domain.Memory, error)
	DeleteLegacyMessageMemories(ids []string) (int, error)
}

type LegacyCleanupReport struct {
	Mode      string   `json:"mode"`
	Found     int      `json:"found"`
	Deleted   int      `json:"deleted"`
	MemoryIDs []string `json:"memory_ids"`
}

// CleanupLegacyMessageMemories is dry-run by default. It only targets records
// created by the former message-to-memory sync protocol.
func CleanupLegacyMessageMemories(store LegacyCleanupStore, apply bool) (LegacyCleanupReport, error) {
	items, err := store.ListLegacyMessageMemories()
	if err != nil {
		return LegacyCleanupReport{}, err
	}
	report := LegacyCleanupReport{Mode: "dry-run", Found: len(items), MemoryIDs: make([]string, 0, len(items))}
	if apply {
		report.Mode = "apply"
	}
	for _, item := range items {
		report.MemoryIDs = append(report.MemoryIDs, item.ID)
	}
	sort.Strings(report.MemoryIDs)
	if !apply || len(report.MemoryIDs) == 0 {
		return report, nil
	}
	report.Deleted, err = store.DeleteLegacyMessageMemories(report.MemoryIDs)
	return report, err
}
