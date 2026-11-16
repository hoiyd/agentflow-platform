package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/tool"
)

func exerciseFailureBoundaries(t *testing.T, harness *harness) controlReport {
	t.Helper()
	result := controlReport{}

	if err := harness.probe(context.Background(), "provider-429", 512); err == nil {
		t.Fatal("provider 429 was accepted")
	} else {
		result.Provider429Code = failure.Describe(err).Code
	}
	if err := harness.probe(context.Background(), "provider-503", 512); failure.Describe(err).Code != "provider_unavailable" {
		t.Fatalf("provider 503 was not classified: %v", err)
	}
	if err := harness.probe(context.Background(), "post-provider-overload", 512); err != nil {
		t.Fatalf("provider overload did not recover: %v", err)
	}
	toolResult := harness.toolExecutor.Execute(context.Background(), tool.ExecutionRequest{
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
	var capacityErr *requestcontrol.TokenBucketCapacityError
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
