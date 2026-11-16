package loadtest

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
