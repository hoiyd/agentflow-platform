package memory

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

type cleanupStoreStub struct {
	items   []domain.Memory
	deleted []string
}

func (s *cleanupStoreStub) ListLegacyMessageMemories() ([]domain.Memory, error) {
	return append([]domain.Memory(nil), s.items...), nil
}

func (s *cleanupStoreStub) DeleteLegacyMessageMemories(ids []string) (int, error) {
	s.deleted = append([]string(nil), ids...)
	return len(ids), nil
}

func TestCleanupLegacyMessageMemoriesDefaultsToDryRun(t *testing.T) {
	store := &cleanupStoreStub{items: []domain.Memory{{ID: "mem_b"}, {ID: "mem_a"}}}
	report, err := CleanupLegacyMessageMemories(store, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "dry-run" || report.Found != 2 || report.Deleted != 0 || len(store.deleted) != 0 {
		t.Fatalf("unexpected dry-run report: %#v deleted=%#v", report, store.deleted)
	}
	if report.MemoryIDs[0] != "mem_a" || report.MemoryIDs[1] != "mem_b" {
		t.Fatalf("memory ids are not stable: %#v", report.MemoryIDs)
	}
}

func TestCleanupLegacyMessageMemoriesApplyIsExplicit(t *testing.T) {
	store := &cleanupStoreStub{items: []domain.Memory{{ID: "mem_a"}}}
	report, err := CleanupLegacyMessageMemories(store, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "apply" || report.Found != 1 || report.Deleted != 1 || len(store.deleted) != 1 {
		t.Fatalf("unexpected apply report: %#v deleted=%#v", report, store.deleted)
	}
}

func TestCleanupLegacyMessageMemoriesEmptyApplyStillReportsApplyMode(t *testing.T) {
	report, err := CleanupLegacyMessageMemories(&cleanupStoreStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "apply" || report.Found != 0 || report.Deleted != 0 {
		t.Fatalf("unexpected empty apply report: %#v", report)
	}
}
