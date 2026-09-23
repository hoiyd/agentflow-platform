package openai

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

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
		passed.Status != "completed" || passed.PromptTokens != 3 || passed.CompletionTokens != 2 || !passed.UsageAvailable || passed.UsageEstimated {
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
		recorder.outcomes[1].Status != "completed" || recorder.outcomes[1].TimeToFirstTokenMS == nil || !recorder.outcomes[1].UsageEstimated {
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
		Usage{PromptTokens: 4, CompletionTokens: 12, TotalTokens: 16}, nil)
	client.finishModelAttempt(context.Background(), requestcontrol.AttemptRef{RecordID: "estimated", Attempt: 2}, started, firstToken,
		Usage{PromptTokens: 4, CompletionTokens: 12, TotalTokens: 16, Estimated: true}, nil)
	if len(recorder.outcomes) != 2 || recorder.outcomes[0].OutputTokensPerSecond <= 0 || recorder.outcomes[1].OutputTokensPerSecond != 0 {
		t.Fatalf("output rate must use exact usage only: %#v", recorder.outcomes)
	}
}
