package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

type attemptRecorderStub struct {
	observations []requestcontrol.Observation
	refs         []requestcontrol.AttemptRef
	outcomes     []requestcontrol.AttemptOutcome
	finishErr    error
}

func (r *attemptRecorderStub) Record(ctx context.Context, observation requestcontrol.Observation) error {
	_, err := r.Begin(ctx, observation)
	return err
}

func (r *attemptRecorderStub) Begin(_ context.Context, observation requestcontrol.Observation) (requestcontrol.AttemptRef, error) {
	ref := requestcontrol.AttemptRef{RecordID: "record-" + strconv.Itoa(len(r.refs)+1), ModelCallID: observation.ModelCallID, Attempt: len(r.refs) + 1}
	r.observations = append(r.observations, observation)
	r.refs = append(r.refs, ref)
	return ref, nil
}

func (r *attemptRecorderStub) Finish(_ context.Context, _ requestcontrol.AttemptRef, outcome requestcontrol.AttemptOutcome) error {
	r.outcomes = append(r.outcomes, outcome)
	return r.finishErr
}

func TestCompletionTelemetryRecordsEachPhysicalRetry(t *testing.T) {
	client := retryTestClient()
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(503, `{"error":{"message":"private provider detail"}}`), nil
		}
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`), nil
	})}
	completion, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
	if err != nil || completion.Text != "ok" || len(recorder.outcomes) != 2 || recorder.refs[0].ModelCallID != recorder.refs[1].ModelCallID {
		t.Fatalf("unexpected retry telemetry: completion=%#v refs=%#v outcomes=%#v err=%v", completion, recorder.refs, recorder.outcomes, err)
	}
	failed, passed := recorder.outcomes[0], recorder.outcomes[1]
	if failed.Status != "failed" || failed.ErrorKind != string(ErrorProviderUnavailable) || failed.HTTPStatus != 503 || failed.UsageAvailable ||
		failed.HTTPDurationMS == nil || passed.HTTPDurationMS == nil || passed.Status != "completed" ||
		passed.PromptTokens != 3 || passed.CompletionTokens != 2 || !passed.UsageAvailable || passed.UsageEstimated {
		t.Fatalf("physical attempts were conflated: failed=%#v passed=%#v", failed, passed)
	}
}

func TestStreamTelemetryMeasuresFirstTokenAndEstimatedUsageFallback(t *testing.T) {
	client := retryTestClient()
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return modelHTTPResponse(400, `{"error":{"message":"stream_options.include_usage is not supported","code":"invalid_request_error"}}`), nil
		}
		return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"), nil
	})}
	events := make(chan StreamEvent, 2)
	emitted, output, _, err := client.streamMessages(context.Background(), []Message{{Role: "user", Content: "hello"}}, events)
	if err != nil || !emitted || output != "hello" || len(recorder.outcomes) != 2 {
		t.Fatalf("unexpected stream fallback: output=%q outcomes=%#v err=%v", output, recorder.outcomes, err)
	}
	if recorder.outcomes[0].Status != "failed" || recorder.outcomes[0].HTTPStatus != 400 || recorder.outcomes[0].TimeToFirstTokenMS != nil ||
		recorder.outcomes[1].Status != "completed" || recorder.outcomes[1].TimeToFirstTokenMS == nil ||
		recorder.outcomes[1].HTTPTimeToFirstTokenMS == nil || !recorder.outcomes[1].UsageEstimated {
		t.Fatalf("stream attempt telemetry incorrect: %#v", recorder.outcomes)
	}
}

func TestPartialStreamAndTelemetryWriteFailureDoNotTriggerRetry(t *testing.T) {
	client := retryTestClient()
	recorder := &attemptRecorderStub{finishErr: errors.New("telemetry store unavailable")}
	client.SetRequestRecorder(recorder)
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), nil
	})}
	events := make(chan StreamEvent, 2)
	emitted, output, _, err := client.streamMessagesWithUsageOption(context.Background(), []Message{{Role: "user", Content: "hello"}}, events, false)
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorInvalidResponse || !emitted || output != "partial" || attempts != 1 || len(recorder.outcomes) != 1 ||
		recorder.outcomes[0].Status != "failed" || recorder.outcomes[0].TimeToFirstTokenMS == nil {
		t.Fatalf("unexpected partial stream telemetry: attempts=%d outcomes=%#v err=%v", attempts, recorder.outcomes, err)
	}

	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
	})}
	if completion, err := client.CompleteTextDetailed(context.Background(), "system", "hello"); err != nil || completion.Text != "ok" {
		t.Fatalf("telemetry failure changed successful completion: completion=%#v err=%v", completion, err)
	}
}

func TestCanceledStreamFinishesAttemptWithoutAnotherRetry(t *testing.T) {
	client := retryTestClient()
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	attempts := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return modelHTTPResponse(200, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	emitted, _, _, err := client.streamMessagesWithUsageOption(ctx, []Message{{Role: "user", Content: "hello"}}, make(chan StreamEvent), false)
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorCanceled || !emitted || attempts != 1 || len(recorder.outcomes) != 1 ||
		recorder.outcomes[0].Status != "failed" || recorder.outcomes[0].ErrorKind != string(ErrorCanceled) {
		t.Fatalf("canceled stream telemetry: attempts=%d outcomes=%#v err=%v", attempts, recorder.outcomes, err)
	}
}

func TestOutputRateRequiresExactUsageAndMeasuredGenerationInterval(t *testing.T) {
	client := retryTestClient()
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	started := time.Now().Add(-200 * time.Millisecond)
	firstToken := started.Add(50 * time.Millisecond)
	client.finishModelAttempt(context.Background(), requestcontrol.AttemptRef{RecordID: "exact", Attempt: 1}, started, firstToken,
		Usage{PromptTokens: 4, CompletionTokens: 12, TotalTokens: 16}, "stop", nil)
	client.finishModelAttempt(context.Background(), requestcontrol.AttemptRef{RecordID: "estimated", Attempt: 2}, started, firstToken,
		Usage{PromptTokens: 4, CompletionTokens: 12, TotalTokens: 16, Estimated: true}, "stop", nil)
	if len(recorder.outcomes) != 2 || recorder.outcomes[0].OutputTokensPerSecond <= 0 || recorder.outcomes[1].OutputTokensPerSecond != 0 {
		t.Fatalf("output rate must use exact usage only: %#v", recorder.outcomes)
	}
}

func TestPermitTimeoutRecordsLocalWaitAndRecovers(t *testing.T) {
	client := NewClientWithTimeout("test-key", "https://provider.example/v1", "test-model", 80*time.Millisecond)
	client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{MaxConcurrent: 1}))
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	requests := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	first, err := client.doRequest(context.Background(), []byte(`{"model":"test-model"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	_, err = client.CompleteTextDetailed(context.Background(), "system", "same prompt")
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorLocalAdmissionTimeout || modelErr.Retryable || requests != 1 || len(recorder.outcomes) != 1 {
		t.Fatalf("permit wait did not stop at route deadline: requests=%d outcomes=%#v err=%v", requests, recorder.outcomes, err)
	}
	if wait := recorder.outcomes[0].ModelPermitWaitMS; wait == nil || *wait < 50 || recorder.outcomes[0].HTTPDurationMS != nil {
		t.Fatalf("local wait was conflated with HTTP time: %#v", recorder.outcomes[0])
	}
	if err := first.Body.Close(); err != nil {
		t.Fatal(err)
	}
	completion, err := client.CompleteTextDetailed(context.Background(), "system", "same prompt")
	if err != nil || completion.Text != "ok" || requests != 2 || len(recorder.outcomes) != 2 || recorder.outcomes[1].HTTPDurationMS == nil {
		t.Fatalf("released permit did not recover: completion=%#v outcomes=%#v err=%v", completion, recorder.outcomes, err)
	}
}

func TestRateWaitTimeoutIsLocalAndDoesNotRetry(t *testing.T) {
	client := NewClientWithTimeout("test-key", "https://provider.example/v1", "test-model", 50*time.Millisecond)
	client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{
		MaxConcurrent: 1, RequestsPerPeriod: 1, RatePeriod: 200 * time.Millisecond,
	}))
	recorder := &attemptRecorderStub{}
	client.SetRequestRecorder(recorder)
	requests := 0
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return modelHTTPResponse(200, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`), nil
	})}
	if _, err := client.CompleteTextDetailed(context.Background(), "system", "same prompt"); err != nil {
		t.Fatal(err)
	}
	_, err := client.CompleteTextDetailed(context.Background(), "system", "same prompt")
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorLocalAdmissionTimeout || modelErr.FailureInfo().Source != "model_request_limiter" ||
		requests != 1 || len(recorder.outcomes) != 2 || recorder.outcomes[1].RateLimitWaitMS == nil ||
		*recorder.outcomes[1].RateLimitWaitMS < 30 || recorder.outcomes[1].HTTPDurationMS != nil {
		t.Fatalf("rate wait was retried or mislabeled: requests=%d outcomes=%#v err=%v", requests, recorder.outcomes, err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := client.CompleteTextDetailed(context.Background(), "system", "same prompt"); err != nil || requests != 2 {
		t.Fatalf("rate bucket did not recover: requests=%d err=%v", requests, err)
	}
}

func TestTimedOutStreamBodyKeepsPartialOutputAndReleasesPermit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	client := NewClientWithTimeout("test-key", server.URL, "test-model", 200*time.Millisecond)
	client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{MaxConcurrent: 1}))
	events := make(chan StreamEvent, 2)
	emitted, output, _, err := client.streamMessagesWithUsageOption(context.Background(), []Message{{Role: "user", Content: "hello"}}, events, false)
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorTimeout || !emitted || output != "partial" || requests.Load() != 1 {
		t.Fatalf("stream timeout was misclassified or retried: output=%q requests=%d err=%v", output, requests.Load(), err)
	}
	if completion, err := client.CompleteTextDetailed(context.Background(), "system", "hello"); err != nil || completion.Text != "recovered" || requests.Load() != 2 {
		t.Fatalf("stream timeout retained permit: completion=%#v requests=%d err=%v", completion, requests.Load(), err)
	}
}

func TestTimedOutCompletionBodyIsNotAnInvalidResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"choices":[`))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	client := NewClientWithTimeout("test-key", server.URL, "test-model", 200*time.Millisecond)
	client.SetRetryPolicy(RetryPolicy{MaxAttempts: 1})
	client.SetRequestLimiter(concurrency.NewModelRequestLimiter(concurrency.ModelRequestLimits{MaxConcurrent: 1}))
	_, err := client.CompleteTextDetailed(context.Background(), "system", "hello")
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorTimeout || requests.Load() != 1 {
		t.Fatalf("completion body timeout was misclassified: requests=%d err=%v", requests.Load(), err)
	}
	if completion, err := client.CompleteTextDetailed(context.Background(), "system", "hello"); err != nil || completion.Text != "recovered" || requests.Load() != 2 {
		t.Fatalf("completion timeout retained permit: completion=%#v requests=%d err=%v", completion, requests.Load(), err)
	}
}
