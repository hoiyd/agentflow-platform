package openai

import (
	"context"
	"net/http"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

// Missing/rejected ownership must never be classified as a retryable transport
// failure. Provider retries reacquire owner capacity and leave no reservations.
func TestOwnerOverloadIsNotProviderRetry(t *testing.T) {
	for _, code := range []string{"model_owner_required", "owner_model_queue_full", "owner_model_queue_timeout", "model_limiter_closed", "model_owner_unavailable"} {
		t.Run(code, func(t *testing.T) {
			client := retryTestClient()
			client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{
				MaxConcurrent: 2, OwnerMaxConcurrent: 1, OwnerResolver: func(context.Context) (string, error) { return "", &requestcontrol.OwnerAdmissionError{Code: code} },
			}))
			recorder := &attemptRecorderStub{}
			client.SetRequestRecorder(recorder)
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("rejected request reached provider")
				return nil, nil
			})}
			_, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
			if failure.Describe(err).Code != code || len(recorder.outcomes) != 1 {
				t.Fatalf("wrong classification/retry: %v %#v", err, recorder.outcomes)
			}
			if recorder.outcomes[0].OwnerCapacityWaitMS == nil || recorder.outcomes[0].HTTPDurationMS != nil {
				t.Fatal("missing local admission diagnostic")
			}
		})
	}
}

func TestOwnerRetriesReleasePhysicalPermit(t *testing.T) {
	l := concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{MaxConcurrent: 2, OwnerMaxConcurrent: 1})
	client := retryTestClient()
	client.SetRequestLimiter(l)
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(503, `{}`), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`), nil
	})}
	ctx, cancel := context.WithTimeout(requestcontrol.WithOwner(context.Background(), "owner"), time.Second)
	defer cancel()
	if _, err := client.CompleteTextDetailed(ctx, "system", "hello"); err != nil || attempts != 2 {
		t.Fatalf("retry leaked permit: %v %d", err, attempts)
	}
	for _, outcome := range recorder.outcomes {
		if outcome.OwnerCapacityWaitMS == nil {
			t.Fatal("retry bypassed owner admission")
		}
	}
	release, err := l.AcquireRequest(ctx, "key", 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestRouteTimeoutDuringOwnerWait(t *testing.T) {
	l := concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{MaxConcurrent: 2, OwnerMaxConcurrent: 1, OwnerQueueSize: 1, OwnerWaitTimeout: time.Minute})
	ctx := requestcontrol.WithOwner(context.Background(), "owner")
	first, err := l.AcquireRequest(ctx, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	client := retryTestClient()
	client.timeout = 10 * time.Millisecond
	client.SetRequestLimiter(l)
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	_, err = client.CompleteTextDetailed(ctx, "system", "hello")
	if failure.Describe(err).Code != "owner_model_queue_timeout" || len(recorder.outcomes) != 1 || recorder.outcomes[0].OwnerCapacityWaitMS == nil {
		t.Fatalf("route expiry in owner wait: %v %#v", err, recorder.outcomes)
	}
	first()
	release, err := l.AcquireRequest(ctx, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
