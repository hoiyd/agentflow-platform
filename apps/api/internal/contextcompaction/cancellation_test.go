package contextcompaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
)

func TestCanceledSummaryCannotReplaceContext(t *testing.T) {
	s, c, run := newCompactionTestStore(t)
	for i := 0; i < 8; i++ {
		s.AddMessage(c.ID, "user", strings.Repeat("retained fact ", 20))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := NewCompactor(s).CompactIfNeeded(ctx, Request{RunID: run.ID, ConversationID: c.ID, Trigger: contextassembly.CompactionTriggerSoft, Config: compactionTestConfig(), Summarizer: SummarizerFunc(func(context.Context, SummaryRequest) (SummaryResult, error) {
		cancel() // Simulate a provider returning a late success after shutdown.
		return SummaryResult{Text: "## Source Evidence\n[]\n\n## Established Facts\nRetained fact.", Model: "fixture"}, nil
	})})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("late success committed: %+v %v", result, err)
	}
	if _, found, _ := s.GetLatestContextCompaction(c.ID); found {
		t.Fatal("canceled summary became active")
	}
	events, _ := s.ListRunEvents(run.ID)
	if !hasEvent(events, domain.EventCompactionFailed) {
		t.Fatal("no terminal compaction evidence")
	}
}
