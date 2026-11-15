package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

type gatedEmbedder struct {
	release chan struct{}
}

func (e *gatedEmbedder) EmbedText(context.Context, string) (provider.Embedding, error) {
	<-e.release
	return provider.Embedding{Provider: "load-fixture", Model: "load-fixture", Vector: []float64{1}}, nil
}

type harness struct {
	config         suiteConfig
	controller     *concurrency.RunController
	modelLimiter   *concurrency.ModelRequestLimiter
	client         *openai.Client
	provider       *providerFixture
	store          *fixturestore.Store
	memoryProvider *memory.BuiltinProvider
	memoryEmbedder *gatedEmbedder
	toolExecutor   *tool.Executor
	toolActive     atomic.Int64
	toolPeak       atomic.Int64
	memoryAccepted atomic.Int64
	memoryRejected atomic.Int64
	closeOnce      sync.Once
	closeReport    resourceReport
}

func newHarness(t *testing.T, config suiteConfig) *harness {
	t.Helper()
	provider := newProviderFixture(config.ProviderDelay, config.ProviderLongTailEvery)
	modelLimiter := concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{MaxConcurrent: config.MaxModelRequests})
	client := openai.NewClientWithTimeout("load-fixture-key", provider.server.URL, "load-fixture-model", config.RequestTimeout)
	client.SetRequestLimiter(modelLimiter)
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	store := fixturestore.New()
	embedder := &gatedEmbedder{release: make(chan struct{})}
	memoryProvider := memory.NewBuiltinProvider(store, embedder, memory.ProviderOptions{
		QueueSize: config.MemoryQueueSize, JobTimeout: config.MemoryJobTimeout, MaxAttempts: 1,
	})
	if err := memoryProvider.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	harness := &harness{
		config: config, controller: concurrency.NewRunController(concurrency.RunOptions{
			MaxConcurrent: config.MaxConcurrentRuns, QueueSize: config.RunQueueSize, WaitTimeout: config.RunQueueWait,
		}),
		modelLimiter: modelLimiter, client: client, provider: provider, store: store,
		memoryProvider: memoryProvider, memoryEmbedder: embedder,
	}
	t.Cleanup(func() { harness.close(t) })
	catalog, err := tool.NewCatalog(
		harness.loadTool("load_probe", config.ToolDelay, tool.ExecutionPolicy{}),
		harness.loadTool("slow_load_probe", config.ToolDelay*10, tool.ExecutionPolicy{Timeout: config.ToolDelay}),
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.toolExecutor = tool.NewExecutor(catalog, tool.ExecutorOptions{})
	return harness
}

func (h *harness) loadTool(name string, delay time.Duration, policy tool.ExecutionPolicy) tool.Binding {
	return tool.Binding{
		Descriptor: tool.Descriptor{Name: name, Parameters: tool.ObjectSchema(nil, nil)}, Policy: policy,
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			active := h.toolActive.Add(1)
			updatePeak(&h.toolPeak, active)
			defer h.toolActive.Add(-1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				return map[string]any{"ok": true}, nil
			}
		},
	}
}

func (h *harness) newRun(maxTotalTokens int, phase string, task int) (domain.Run, domain.Message, error) {
	conversation, err := h.store.CreateConversation("load " + phase)
	if err != nil {
		return domain.Run{}, domain.Message{}, err
	}
	limits := domain.RuntimeRunBudget{
		MaxModelCalls: 1, MaxToolCalls: 1, MaxTotalTokens: maxTotalTokens,
		MaxRuntimeMS: h.config.RequestTimeout.Milliseconds(),
	}
	run, err := h.store.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &limits,
	}, nil)
	message := domain.Message{
		ID: fmt.Sprintf("load-%s-%d", phase, task), WorkspaceID: conversation.WorkspaceID,
		ConversationID: conversation.ID, Role: "user", Content: fmt.Sprintf("Remember that load sample %s-%d uses bounded resources.", phase, task),
	}
	return run, message, err
}

func (h *harness) probe(ctx context.Context, prompt string, maxTotalTokens int) error {
	reservation, err := h.controller.Reserve()
	if err != nil {
		return err
	}
	release, err := reservation.Start(ctx, "probe-"+prompt)
	if err != nil {
		return err
	}
	defer release()
	run, _, err := h.newRun(maxTotalTokens, "probe", int(time.Now().UnixNano()))
	if err != nil {
		return err
	}
	ctx = budget.WithController(ctx, budget.NewTracker(h.store, nil, run))
	_, err = h.client.CompleteTextDetailed(ctx, "Return ok.", prompt)
	return err
}

func (h *harness) close(t *testing.T) resourceReport {
	t.Helper()
	h.closeOnce.Do(func() {
		close(h.memoryEmbedder.release)
		memoryCtx, memoryCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := h.memoryProvider.Close(memoryCtx); err != nil {
			t.Errorf("close memory provider: %v", err)
		}
		memoryCancel()
		drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
		if err := h.controller.CloseAndWait(drainCtx); err != nil {
			t.Errorf("drain run controller: %v", err)
		}
		drainCancel()
		h.provider.close()
		h.closeReport = resourceReport{
			ConnectionsOpened: h.provider.opened.Load(), ConnectionsAfterCleanup: h.provider.connections.Load(),
		}
	})
	return h.closeReport
}

func waitForGoroutines(limit int) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		current := runtime.NumGoroutine()
		if current <= limit || time.Now().After(deadline) {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
}
