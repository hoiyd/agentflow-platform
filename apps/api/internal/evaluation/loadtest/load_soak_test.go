package loadtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/modelrequest"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tools"
)

const reportSchema = "agentflow-bounded-load-report-v1"

type suiteConfig struct {
	MaxConcurrentRuns     int           `json:"max_concurrent_runs"`
	RunQueueSize          int           `json:"run_queue_size"`
	RunQueueWait          time.Duration `json:"run_queue_wait_ns"`
	MaxModelRequests      int           `json:"max_model_requests"`
	ProviderDelay         time.Duration `json:"provider_delay_ns"`
	ProviderLongTailEvery int64         `json:"provider_long_tail_every"`
	ToolDelay             time.Duration `json:"tool_delay_ns"`
	RequestTimeout        time.Duration `json:"request_timeout_ns"`
	MemoryQueueSize       int           `json:"memory_queue_size"`
	MemoryJobTimeout      time.Duration `json:"memory_job_timeout_ns"`
	SoakDuration          time.Duration `json:"soak_duration_ns"`
	SoakArrivalInterval   time.Duration `json:"soak_arrival_interval_ns"`
}

type phaseReport struct {
	Name                 string         `json:"name"`
	Offered              int            `json:"offered"`
	Accepted             int            `json:"accepted"`
	Rejected             int            `json:"rejected"`
	RejectionRate        float64        `json:"rejection_rate"`
	Completed            int            `json:"completed"`
	Failed               int            `json:"failed"`
	ArrivalIntervalMS    int64          `json:"arrival_interval_ms"`
	DurationMS           int64          `json:"duration_ms"`
	QueueWaitP50MS       int64          `json:"queue_wait_p50_ms"`
	QueueWaitP95MS       int64          `json:"queue_wait_p95_ms"`
	ServiceP50MS         int64          `json:"service_p50_ms"`
	ServiceP95MS         int64          `json:"service_p95_ms"`
	PeakActiveRuns       int            `json:"peak_active_runs"`
	PeakSameConversation int            `json:"peak_same_conversation"`
	FailureCodes         map[string]int `json:"failure_codes"`
}

type controlReport struct {
	PeakProviderRequests    int64  `json:"peak_provider_requests"`
	PeakToolRequests        int64  `json:"peak_tool_requests"`
	Provider429Code         string `json:"provider_429_code"`
	ToolTimeoutCode         string `json:"tool_timeout_code"`
	TimeoutReleasedPermit   bool   `json:"timeout_released_permit"`
	CanceledRequestRecovers bool   `json:"canceled_request_recovers"`
	IgnoredCancelRecovered  bool   `json:"ignored_cancel_recovered"`
	RunBudgetCode           string `json:"run_budget_code"`
	RPMWaitCanceled         bool   `json:"rpm_wait_canceled"`
	RPMRecovered            bool   `json:"rpm_recovered"`
	TPMCapacityCode         string `json:"tpm_capacity_code"`
	MemorySyncAccepted      int64  `json:"memory_sync_accepted"`
	MemorySyncRejected      int64  `json:"memory_sync_rejected"`
}

type resourceReport struct {
	GoroutinesBefore        int    `json:"goroutines_before"`
	GoroutinesAfter         int    `json:"goroutines_after"`
	GoroutineDelta          int    `json:"goroutine_delta"`
	HeapAllocBeforeBytes    uint64 `json:"heap_alloc_before_bytes"`
	HeapAllocAfterBytes     uint64 `json:"heap_alloc_after_bytes"`
	HeapAllocDeltaBytes     int64  `json:"heap_alloc_delta_bytes"`
	ConnectionsOpened       int64  `json:"connections_opened"`
	ConnectionsAfterCleanup int64  `json:"connections_after_cleanup"`
}

type loadReport struct {
	SchemaVersion string         `json:"schema_version"`
	GitRevision   string         `json:"git_revision"`
	ConfigHash    string         `json:"config_hash"`
	GoVersion     string         `json:"go_version"`
	GOOS          string         `json:"goos"`
	GOARCH        string         `json:"goarch"`
	CPUCount      int            `json:"cpu_count"`
	StartedAt     time.Time      `json:"started_at"`
	CompletedAt   time.Time      `json:"completed_at"`
	Config        suiteConfig    `json:"config"`
	Phases        []phaseReport  `json:"phases"`
	Controls      controlReport  `json:"controls"`
	Resources     resourceReport `json:"resources"`
	Limitations   []string       `json:"limitations"`
}

type phaseSpec struct {
	name               string
	count              int
	arrival            time.Duration
	sharedConversation bool
}

type phaseRecorder struct {
	mu                  sync.Mutex
	report              phaseReport
	queueWaits          []time.Duration
	serviceTimes        []time.Duration
	active              int
	activeConversations map[string]int
}

func newPhaseRecorder(spec phaseSpec) *phaseRecorder {
	return &phaseRecorder{report: phaseReport{
		Name: spec.name, ArrivalIntervalMS: spec.arrival.Milliseconds(), FailureCodes: map[string]int{},
	}, activeConversations: map[string]int{}}
}

func (r *phaseRecorder) offer() {
	r.mu.Lock()
	r.report.Offered++
	r.mu.Unlock()
}

func (r *phaseRecorder) accept() {
	r.mu.Lock()
	r.report.Accepted++
	r.mu.Unlock()
}

func (r *phaseRecorder) reject(err error) {
	r.mu.Lock()
	r.report.Rejected++
	r.report.FailureCodes[failure.Describe(err).Code]++
	r.mu.Unlock()
}

func (r *phaseRecorder) start(conversationID string, queueWait time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueWaits = append(r.queueWaits, queueWait)
	r.active++
	r.report.PeakActiveRuns = max(r.report.PeakActiveRuns, r.active)
	r.activeConversations[conversationID]++
	r.report.PeakSameConversation = max(r.report.PeakSameConversation, r.activeConversations[conversationID])
}

func (r *phaseRecorder) finish(conversationID string, serviceTime time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.serviceTimes = append(r.serviceTimes, serviceTime)
	r.active--
	r.activeConversations[conversationID]--
	if err == nil {
		r.report.Completed++
		return
	}
	r.report.Failed++
	r.report.FailureCodes[failure.Describe(err).Code]++
}

func (r *phaseRecorder) startFailed(queueWait time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueWaits = append(r.queueWaits, queueWait)
	r.report.Failed++
	r.report.FailureCodes[failure.Describe(err).Code]++
}

func (r *phaseRecorder) snapshot(duration time.Duration) phaseReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.report.DurationMS = duration.Milliseconds()
	if r.report.Offered > 0 {
		r.report.RejectionRate = float64(r.report.Rejected) / float64(r.report.Offered)
	}
	r.report.QueueWaitP50MS = percentileMS(r.queueWaits, 50)
	r.report.QueueWaitP95MS = percentileMS(r.queueWaits, 95)
	r.report.ServiceP50MS = percentileMS(r.serviceTimes, 50)
	r.report.ServiceP95MS = percentileMS(r.serviceTimes, 95)
	return r.report
}

type providerFixture struct {
	server        *httptest.Server
	delay         time.Duration
	longTailEvery int64
	requests      atomic.Int64
	active        atomic.Int64
	peak          atomic.Int64
	connections   atomic.Int64
	opened        atomic.Int64
}

func newProviderFixture(delay time.Duration, longTailEvery int64) *providerFixture {
	fixture := &providerFixture{delay: delay, longTailEvery: longTailEvery}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveHTTP))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			fixture.opened.Add(1)
			fixture.connections.Add(1)
		case http.StateClosed, http.StateHijacked:
			fixture.connections.Add(-1)
		}
	}
	server.Start()
	fixture.server = server
	return fixture
}

func (p *providerFixture) serveHTTP(w http.ResponseWriter, request *http.Request) {
	active := p.active.Add(1)
	updatePeak(&p.peak, active)
	defer p.active.Add(-1)

	body, _ := io.ReadAll(request.Body)
	if bytes.Contains(body, []byte("provider-429")) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture capacity","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
		return
	}

	delay := p.delay
	requestNumber := p.requests.Add(1)
	if bytes.Contains(body, []byte("ignore-cancel")) {
		time.Sleep(p.delay * 4)
	} else {
		if p.longTailEvery > 0 && requestNumber%p.longTailEvery == 0 {
			delay *= 4
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(delay):
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
}

func (p *providerFixture) close() {
	p.server.CloseClientConnections()
	p.server.Close()
}

type gatedEmbedder struct {
	release chan struct{}
}

func (e *gatedEmbedder) EmbedText(context.Context, string) (modelprovider.Embedding, error) {
	<-e.release
	return modelprovider.Embedding{Provider: "load-fixture", Model: "load-fixture", Vector: []float64{1}}, nil
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
	toolExecutor   *tools.Executor
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
	catalog, err := tools.NewCatalog(
		harness.loadTool("load_probe", config.ToolDelay, tools.ExecutionPolicy{}),
		harness.loadTool("slow_load_probe", config.ToolDelay*10, tools.ExecutionPolicy{Timeout: config.ToolDelay}),
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.toolExecutor = tools.NewExecutor(catalog, tools.ExecutorOptions{})
	return harness
}

func (h *harness) loadTool(name string, delay time.Duration, policy tools.ExecutionPolicy) tools.Binding {
	return tools.Binding{
		Descriptor: tools.Descriptor{Name: name, Parameters: tools.ObjectSchema(nil, nil)}, Policy: policy,
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

func (h *harness) runPhase(spec phaseSpec) phaseReport {
	recorder := newPhaseRecorder(spec)
	started := time.Now()
	var workers sync.WaitGroup
	for index := 0; index < spec.count; index++ {
		recorder.offer()
		reservation, err := h.controller.Reserve()
		if err != nil {
			recorder.reject(err)
		} else {
			recorder.accept()
			conversationID := fmt.Sprintf("%s-%d", spec.name, index)
			if spec.sharedConversation {
				conversationID = spec.name + "-shared"
			}
			workers.Add(1)
			go func(task int, writerID string) {
				defer workers.Done()
				h.runReservation(spec.name, task, writerID, reservation, recorder)
			}(index, conversationID)
		}
		if spec.arrival > 0 {
			time.Sleep(spec.arrival)
		}
	}
	workers.Wait()
	return recorder.snapshot(time.Since(started))
}

func (h *harness) runReservation(phase string, task int, writerID string, reservation *concurrency.Reservation, recorder *phaseRecorder) {
	ctx, cancel := context.WithTimeout(context.Background(), h.config.RequestTimeout)
	defer cancel()
	waitStarted := time.Now()
	release, err := reservation.Start(ctx, writerID)
	queueWait := time.Since(waitStarted)
	if err != nil {
		recorder.startFailed(queueWait, err)
		return
	}
	defer release()
	recorder.start(writerID, queueWait)

	serviceStarted := time.Now()
	run, message, err := h.newRun(512, phase, task)
	if err == nil {
		ctx = budget.WithController(ctx, budget.NewTracker(h.store, nil, run))
		_, err = h.client.CompleteTextDetailed(ctx, "Return ok.", fmt.Sprintf("phase=%s task=%d", phase, task))
		if err == nil {
			result := h.toolExecutor.Execute(ctx, tools.ExecutionRequest{
				CallID: fmt.Sprintf("tool-%s-%d", phase, task), RunID: run.ID,
				Tool: "load_probe", Arguments: json.RawMessage(`{}`),
			})
			if result.Error != nil {
				err = result.Error
			}
		}
	}
	serviceTime := time.Since(serviceStarted)
	recorder.finish(writerID, serviceTime, err)
	if err != nil {
		return
	}
	if syncErr := h.memoryProvider.SyncTurn(memory.TurnSyncRequest{RunID: run.ID, Message: message}); syncErr != nil {
		h.memoryRejected.Add(1)
	} else {
		h.memoryAccepted.Add(1)
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

func TestBoundedLoadAndSoak(t *testing.T) {
	soakDuration := configuredSoakDuration(t)
	config := suiteConfig{
		MaxConcurrentRuns: 2, RunQueueSize: 2, RunQueueWait: 250 * time.Millisecond,
		MaxModelRequests: 1, ProviderDelay: 20 * time.Millisecond, ProviderLongTailEvery: 5,
		ToolDelay:      5 * time.Millisecond,
		RequestTimeout: 500 * time.Millisecond, MemoryQueueSize: 2, MemoryJobTimeout: 5 * time.Second,
		SoakDuration: soakDuration, SoakArrivalInterval: 5 * time.Millisecond,
	}
	startedAt := time.Now().UTC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineGoroutines := runtime.NumGoroutine()
	harness := newHarness(t, config)

	phases := []phaseReport{
		harness.runPhase(phaseSpec{name: "underloaded", count: 4, arrival: 35 * time.Millisecond}),
		harness.runPhase(phaseSpec{name: "saturated", count: 4, sharedConversation: true}),
		harness.runPhase(phaseSpec{name: "overloaded", count: 20}),
		harness.runPhase(phaseSpec{name: "recovery", count: 4, arrival: 35 * time.Millisecond}),
	}
	soakCount := max(1, int(soakDuration/config.SoakArrivalInterval))
	phases = append(phases, harness.runPhase(phaseSpec{name: "soak", count: soakCount, arrival: config.SoakArrivalInterval}))

	controls := exerciseFailureBoundaries(t, harness)
	controls.PeakProviderRequests = harness.provider.peak.Load()
	controls.PeakToolRequests = harness.toolPeak.Load()
	controls.MemorySyncAccepted = harness.memoryAccepted.Load()
	controls.MemorySyncRejected = harness.memoryRejected.Load()
	resources := harness.close(t)
	resources.GoroutinesBefore = baselineGoroutines
	resources.GoroutinesAfter = waitForGoroutines(baselineGoroutines + 6)
	resources.GoroutineDelta = resources.GoroutinesAfter - baselineGoroutines
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	resources.HeapAllocBeforeBytes = before.HeapAlloc
	resources.HeapAllocAfterBytes = after.HeapAlloc
	resources.HeapAllocDeltaBytes = int64(after.HeapAlloc) - int64(before.HeapAlloc)

	assertLoadBoundaries(t, config, phases, controls, resources)
	report := loadReport{
		SchemaVersion: reportSchema, GitRevision: gitRevision(), ConfigHash: hashConfig(config),
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUCount: runtime.NumCPU(),
		StartedAt: startedAt, CompletedAt: time.Now().UTC(), Config: config, Phases: phases,
		Controls: controls, Resources: resources,
		Limitations: []string{
			"fixture throughput proves local scheduling boundaries, not real model QPS or SLO",
			"heap deltas are observational because the Go runtime retains reusable allocations",
			"the default soak is intentionally short for CI; use SOAK_DURATION for a longer bounded run",
		},
	}
	writeReport(t, report)
}

func exerciseFailureBoundaries(t *testing.T, harness *harness) controlReport {
	t.Helper()
	result := controlReport{}

	if err := harness.probe(context.Background(), "provider-429", 512); err == nil {
		t.Fatal("provider 429 was accepted")
	} else {
		result.Provider429Code = failure.Describe(err).Code
	}
	toolResult := harness.toolExecutor.Execute(context.Background(), tools.ExecutionRequest{
		Tool: "slow_load_probe", Arguments: json.RawMessage(`{}`),
	})
	if toolResult.Error == nil {
		t.Fatal("slow Tool did not time out")
	}
	result.ToolTimeoutCode = string(toolResult.Error.Code)

	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	err := harness.probe(timeoutCtx, "timeout", 512)
	timeoutCancel()
	if err == nil {
		t.Fatal("slow provider completed before caller timeout")
	}
	if !waitForProviderIdle(harness.provider, harness.config.RequestTimeout) {
		t.Fatal("timed-out provider request did not finish")
	}
	timeoutRecoveryCtx, timeoutRecoveryCancel := context.WithTimeout(context.Background(), harness.config.RequestTimeout)
	result.TimeoutReleasedPermit = harness.probe(timeoutRecoveryCtx, "post-timeout-recovery", 512) == nil
	timeoutRecoveryCancel()

	requestStarted := harness.provider.requests.Load()
	canceledCtx, cancelRequest := context.WithCancel(context.Background())
	canceledDone := make(chan error, 1)
	go func() { canceledDone <- harness.probe(canceledCtx, "explicit-cancel", 512) }()
	deadline := time.Now().Add(100 * time.Millisecond)
	for harness.provider.requests.Load() == requestStarted && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if harness.provider.requests.Load() == requestStarted {
		cancelRequest()
		t.Fatal("canceled request did not reach provider")
	}
	cancelRequest()
	select {
	case err = <-canceledDone:
		if err == nil {
			t.Fatal("canceled provider request completed")
		}
	case <-time.After(harness.config.RequestTimeout):
		t.Fatal("canceled provider request did not return")
	}
	if !waitForProviderIdle(harness.provider, harness.config.RequestTimeout) {
		t.Fatal("canceled provider request did not finish")
	}
	cancelRecoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), harness.config.RequestTimeout)
	result.CanceledRequestRecovers = harness.probe(cancelRecoveryCtx, "post-explicit-cancel", 512) == nil
	cancelRecovery()

	ignoredCtx, ignoredCancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	err = harness.probe(ignoredCtx, "ignore-cancel", 512)
	ignoredCancel()
	if err == nil {
		t.Fatal("ignored-cancel provider completed before caller timeout")
	}
	if !waitForProviderIdle(harness.provider, harness.config.RequestTimeout) {
		t.Fatal("ignored-cancel provider request did not finish")
	}
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), harness.config.RequestTimeout)
	result.IgnoredCancelRecovered = harness.probe(recoveryCtx, "post-cancel-recovery", 512) == nil
	recoveryCancel()

	if err := harness.probe(context.Background(), "budget-probe", 1); err == nil {
		t.Fatal("run budget did not reject oversized request")
	} else {
		result.RunBudgetCode = failure.Describe(err).Code
	}

	rateLimiter := concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{
		MaxConcurrent: 1, RequestsPerPeriod: 1, TokensPerPeriod: 10, RatePeriod: 30 * time.Millisecond,
	})
	release, err := rateLimiter.AcquireRequest(context.Background(), "rate-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
	rateCtx, rateCancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	_, err = rateLimiter.AcquireRequest(rateCtx, "rate-key", 1)
	rateCancel()
	result.RPMWaitCanceled = errors.Is(err, context.DeadlineExceeded)
	time.Sleep(30 * time.Millisecond)
	release, err = rateLimiter.AcquireRequest(context.Background(), "rate-key", 1)
	if err == nil {
		result.RPMRecovered = true
		release()
	}
	_, err = rateLimiter.AcquireRequest(context.Background(), "token-key", 11)
	var capacityErr *modelrequest.TokenBucketCapacityError
	if errors.As(err, &capacityErr) {
		result.TPMCapacityCode = failure.Describe(err).Code
	}
	return result
}

func assertLoadBoundaries(t *testing.T, config suiteConfig, phases []phaseReport, controls controlReport, resources resourceReport) {
	t.Helper()
	byName := map[string]phaseReport{}
	for _, phase := range phases {
		byName[phase.Name] = phase
		if phase.Offered != phase.Accepted+phase.Rejected || phase.Accepted != phase.Completed+phase.Failed {
			t.Fatalf("phase accounting mismatch: %#v", phase)
		}
		if phase.PeakActiveRuns > config.MaxConcurrentRuns || phase.PeakSameConversation > 1 {
			t.Fatalf("phase exceeded concurrency boundary: %#v", phase)
		}
	}
	if underloaded := byName["underloaded"]; underloaded.Rejected != 0 || underloaded.Completed != underloaded.Offered {
		t.Fatalf("underloaded phase failed: %#v", underloaded)
	}
	if overloaded := byName["overloaded"]; overloaded.Rejected == 0 {
		t.Fatalf("overloaded phase did not apply backpressure: %#v", overloaded)
	}
	if recovery := byName["recovery"]; recovery.Rejected != 0 || recovery.Completed != recovery.Offered {
		t.Fatalf("controller did not recover after overload: %#v", recovery)
	}
	if controls.PeakProviderRequests > int64(config.MaxModelRequests) || controls.PeakToolRequests > int64(config.MaxConcurrentRuns) ||
		controls.Provider429Code != "rate_limited" || controls.ToolTimeoutCode != "execution_timeout" ||
		!controls.TimeoutReleasedPermit || !controls.CanceledRequestRecovers || !controls.IgnoredCancelRecovered || controls.RunBudgetCode != "budget_exceeded" ||
		!controls.RPMWaitCanceled || !controls.RPMRecovered || controls.TPMCapacityCode != "request_token_capacity_exceeded" ||
		controls.MemorySyncAccepted == 0 || controls.MemorySyncRejected == 0 {
		t.Fatalf("control boundary mismatch: %#v", controls)
	}
	if resources.ConnectionsAfterCleanup != 0 || resources.GoroutineDelta > 6 {
		t.Fatalf("resources did not return to bounded baseline: %#v", resources)
	}
}

func configuredSoakDuration(t *testing.T) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("LOAD_SOAK_DURATION"))
	if value == "" {
		return 2 * time.Second
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 250*time.Millisecond || duration > 5*time.Minute {
		t.Fatalf("LOAD_SOAK_DURATION must be between 250ms and 5m, got %q", value)
	}
	return duration
}

func percentileMS(values []time.Duration, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*percentile+99)/100 - 1
	return ordered[max(0, min(index, len(ordered)-1))].Milliseconds()
}

func updatePeak(target *atomic.Int64, value int64) {
	for current := target.Load(); value > current && !target.CompareAndSwap(current, value); current = target.Load() {
	}
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

func waitForProviderIdle(provider *providerFixture, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for provider.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return provider.active.Load() == 0
}

func hashConfig(config suiteConfig) string {
	content, _ := json.Marshal(config)
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func gitRevision() string {
	content, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(content))
}

func writeReport(t *testing.T, report loadReport) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("EVALUATION_REPORT_DIR"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bounded-load.json"), content, 0600); err != nil {
		t.Fatal(err)
	}
	var table strings.Builder
	table.WriteString("# Bounded Load / Soak Evidence\n\n")
	table.WriteString("| Phase | Offered | Accepted | Rejected | Rejection rate | Completed | Failed | Queue p50/p95 ms | Service p50/p95 ms | Peak runs |\n")
	table.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, phase := range report.Phases {
		fmt.Fprintf(&table, "| %s | %d | %d | %d | %.1f%% | %d | %d | %d/%d | %d/%d | %d |\n",
			phase.Name, phase.Offered, phase.Accepted, phase.Rejected, phase.RejectionRate*100, phase.Completed, phase.Failed,
			phase.QueueWaitP50MS, phase.QueueWaitP95MS, phase.ServiceP50MS, phase.ServiceP95MS, phase.PeakActiveRuns)
	}
	table.WriteString("\nFixture throughput validates local scheduling boundaries only; it is not real model QPS or an SLO.\n")
	if err := os.WriteFile(filepath.Join(dir, "bounded-load.md"), []byte(table.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
