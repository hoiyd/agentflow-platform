package agent

import (
	"context"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Exercise the real completion -> Snapshot/model resolution -> compactor path,
// not a replacement completion callback. Shutdown must not change the answer Run.
func TestCompletionDoesNotWaitForOwnedSoftSummary(t *testing.T) {
	s := fixturestore.New()
	c, _ := s.CreateConversation("owned soft compaction")
	cfg := overflowCompactionConfig()
	cfg.ContextWindowTokens = 240
	cfg.OutputReserveTokens = 20
	cfg.SafetyMarginTokens = 20
	cfg.CompactionSoftThreshold = .25
	cfg.CompactionHardThreshold = .30
	client := &blockingSoftClient{entered: make(chan struct{})}
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = cfg
	setOverflowModelSnapshot(t, &snapshot, client, cfg)
	snapshot.ModelRouting.PinnedRouteID = snapshot.ModelRouting.Routes[0].ID
	run, err := s.CreateRunWithContract("agent_planner", c.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		s.AddMessage(c.ID, "user", strings.Repeat("retained context ", 20))
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	r := newRuntimeForTest(RuntimeOptions{Store: s, ContextAssembly: cfg}, client)
	if _, err := r.ModelClientForRun(run.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.CloseCompaction(context.Background()) })
	completed, err := r.CompleteRun(run.ID)
	if err != nil || completed.Status != domain.RunCompleted {
		t.Fatalf("%+v %v", completed, err)
	}
	<-client.entered
	r.scheduleSoftContextCompaction(completed) // Duplicate cannot queue another job.
	if err = r.CloseCompaction(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetRun(run.ID)
	if got.Status != domain.RunCompleted {
		t.Fatal(got)
	}
	if _, found, _ := s.GetLatestContextCompaction(c.ID); found {
		t.Fatal("canceled late success became active")
	}
	events, _ := s.ListRunEvents(run.ID)
	starts, failed := 0, 0
	for _, event := range events {
		if event.Type == domain.EventCompactionStarted {
			starts++
		}
		if event.Type == domain.EventCompactionFailed {
			failed++
		}
	}
	if starts != 1 || failed != 1 {
		t.Fatalf("starts=%d failed=%d", starts, failed)
	}
}

type blockingSoftClient struct {
	overflowRecoveryClient
	entered chan struct{}
}

func (c *blockingSoftClient) WithRuntimeIdentity(provider.RuntimeIdentity) provider.Client { return c }
func (c *blockingSoftClient) CompleteTextDetailed(ctx context.Context, _, _ string) (provider.TextCompletion, error) {
	close(c.entered)
	<-ctx.Done()
	return provider.TextCompletion{Text: "## Source Evidence\n[]\n\n## Established Facts\nA late success.", Model: "fixture"}, nil
}
