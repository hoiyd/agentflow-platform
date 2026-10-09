package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type recordingExporter struct {
	mu               sync.Mutex
	spans            []sdktrace.ReadOnlySpan
	entered, release chan struct{}
	gate             sync.Once
}

func (e *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.entered != nil {
		e.gate.Do(func() { e.entered <- struct{}{}; <-e.release })
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}
func (e *recordingExporter) Shutdown(context.Context) error { return nil }

func event(kind, stage, turn string, payload map[string]any) domain.RunEvent {
	return domain.RunEvent{Type: domain.RunEventType(kind), RunID: "run-1", ConversationID: "conv-1", StageID: stage, TurnID: turn, Sequence: 7, Timestamp: time.Now(), Payload: payload}
}
func closeObserver(t *testing.T, o *Observer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
func testObserver(exporter *recordingExporter, bounds limits) *Observer {
	return newObserver(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)), bounds)
}

func TestExecutionHierarchyAndContentBoundary(t *testing.T) {
	for _, mode := range []string{"single", "multi", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			exporter := &recordingExporter{}
			o := testObserver(exporter, defaultLimits())
			stage := ""
			secret := "PRIVATE_CONTENT_CANARY"
			o.Observe(event("run.started", "", "", map[string]any{"input": secret}))
			if mode != "single" {
				stage = "stage-1"
				o.Observe(event("stage.started", stage, "", map[string]any{"name": secret, "input": secret}))
			}
			o.Observe(event("turn.started", stage, "turn-1", nil))
			for attempt := 1; attempt <= 2; attempt++ {
				id := "record-" + string(rune('0'+attempt))
				o.Observe(event("model.request_prepared", stage, "turn-1", map[string]any{"record_id": id, "model_call_id": "call-1", "attempt": attempt, "provider": "fixture", "model": "fixture-model", "parameters": secret, "payload_hash": secret}))
				o.Observe(event("model.attempt_finished", stage, "turn-1", map[string]any{"record_id": id, "status": "completed", "duration_ms": 12, "total_tokens": json.Number("32"), "http_time_to_first_token_ms": float64(3), "usage_estimated": false, "error": secret}))
			}
			o.Observe(event("tool.started", stage, "turn-1", map[string]any{"tool_call_id": "tool-1", "tool_name": "get_current_time", "arguments": secret}))
			o.Observe(event("tool.progress", stage, "turn-1", map[string]any{"message": secret}))
			o.Observe(event("model.reasoning", stage, "turn-1", map[string]any{"content": secret}))
			o.Observe(event("tool.completed", stage, "turn-1", map[string]any{"tool_call_id": "tool-1", "result": secret}))
			o.Observe(event("turn.completed", stage, "turn-1", map[string]any{"output": secret}))
			if stage != "" {
				o.Observe(event("stage.completed", stage, "", nil))
			}
			o.Observe(event("run.completed", "", "", nil))
			closeObserver(t, o)
			want := 5
			if stage != "" {
				want++
			}
			if len(exporter.spans) != want {
				t.Fatalf("spans=%d want %d", len(exporter.spans), want)
			}
			var root, stageSpan, turn sdktrace.ReadOnlySpan
			for _, s := range exporter.spans {
				switch s.Name() {
				case "agentflow.run":
					root = s
				case "agentflow.stage":
					stageSpan = s
				case "agentflow.turn":
					turn = s
				}
				for _, a := range s.Attributes() {
					if strings.Contains(a.Value.Emit(), secret) {
						t.Fatalf("private value exported: %v", a)
					}
				}
			}
			if root.Parent().IsValid() {
				t.Fatal("root has parent")
			}
			parent := root
			if stageSpan != nil {
				parent = stageSpan
				if stageSpan.Parent().SpanID() != root.SpanContext().SpanID() {
					t.Fatal("stage parent")
				}
			}
			if turn.Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Fatal("turn parent")
			}
			for _, s := range exporter.spans {
				if s.Name() == "agentflow.model_attempt" || s.Name() == "agentflow.tool" {
					if s.Parent().SpanID() != turn.SpanContext().SpanID() {
						t.Fatal("attempt/tool parent")
					}
				}
			}
		})
	}
}

func TestResumeCancellationFailuresAndIncompleteBoundaries(t *testing.T) {
	e := &recordingExporter{}
	o := testObserver(e, defaultLimits())
	o.Observe(event("tool.completed", "", "", map[string]any{"tool_call_id": "missing"}))
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("turn.started", "", "turn-1", nil))
	o.Observe(event("model.request_prepared", "", "turn-1", map[string]any{"record_id": "bad-attempt"}))
	o.Observe(event("model.attempt_finished", "", "turn-1", map[string]any{"record_id": "bad-attempt", "status": "failed", "error_kind": "rate_limited", "http_status": 429}))
	o.Observe(event("tool.started", "", "turn-1", map[string]any{"tool_call_id": "bad-tool"}))
	o.Observe(event("tool.failed", "", "turn-1", map[string]any{"tool_call_id": "bad-tool"}))
	o.Observe(event("run.waiting_for_user", "", "", nil))
	o.Observe(event("run.resumed", "", "", nil))
	o.Observe(event("turn.started", "", "turn-2", nil))
	o.Observe(event("turn.failed", "", "turn-2", map[string]any{"stop_reason": "canceled", "error": "sensitive"}))
	o.Observe(event("run.canceled", "", "", nil))
	closeObserver(t, o)
	var roots []sdktrace.ReadOnlySpan
	for _, s := range e.spans {
		if s.Name() == "agentflow.run" {
			roots = append(roots, s)
		}
		if s.Name() == "agentflow.model_attempt" || s.Name() == "agentflow.tool" {
			if s.Status().Code != codes.Error {
				t.Fatal("failure not marked")
			}
		}
		if s.Name() == "agentflow.turn" && s.Status().Description == "canceled" {
			t.Fatal("cancellation incorrectly treated as error")
		}
	}
	if len(roots) != 2 || roots[0].SpanContext().TraceID() == roots[1].SpanContext().TraceID() {
		t.Fatal("resume must create a new execution segment")
	}
}

func TestBoundedQueueAndShutdownDoNotBlockProducers(t *testing.T) {
	e := &recordingExporter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	b := defaultLimits()
	b.queue = 2
	o := testObserver(e, b)
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("run.completed", "", "", nil))
	select {
	case <-e.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach exporter")
	}
	for i := 0; i < 20; i++ {
		o.Observe(event("run.started", "", "", nil))
	}
	if o.Dropped() == 0 {
		t.Fatal("overflow not counted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Shutdown(ctx); err == nil {
		t.Fatal("shutdown ignored expired context")
	}
	close(e.release)
	closeObserver(t, o)
	o.Observe(event("run.started", "", "", nil))
}

func TestActiveSpanCapAndStaleCleanup(t *testing.T) {
	e := &recordingExporter{}
	b := defaultLimits()
	b.active = 1
	b.ttl = time.Nanosecond
	b.sweep = time.Millisecond
	o := testObserver(e, b)
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("turn.started", "", "turn-1", nil))
	// Shutdown drains the queue and marks any still-open span incomplete.
	closeObserver(t, o)
	if o.Dropped() != 1 || len(e.spans) != 1 || e.spans[0].Status().Code != codes.Error {
		t.Fatalf("bounded cleanup: drops=%d spans=%v", o.Dropped(), e.spans)
	}
}

func TestOTLPHTTPExportAndDisabledConfiguration(t *testing.T) {
	received := make(chan *collectorpb.ExportTraceServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected path or credential")
		}
		data, _ := io.ReadAll(r.Body)
		request := &collectorpb.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(data, request); err != nil {
			t.Error(err)
		}
		received <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=secret-canary")
	o, err := New(Config{Exporter: "otlp", Endpoint: server.URL, ServiceName: "agentflow-test", SampleRatio: "1"})
	if err != nil {
		t.Fatal(err)
	}
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("run.completed", "", "", map[string]any{"output": "secret-canary"}))
	closeObserver(t, o)
	select {
	case request := <-received:
		data, _ := protojson.Marshal(request)
		if strings.Contains(string(data), "secret-canary") || !strings.Contains(string(data), "agentflow-test") {
			t.Fatalf("unexpected OTLP: %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("no OTLP export")
	}
	for _, cfg := range []Config{{}, {Exporter: "none", Endpoint: "invalid"}} {
		o, err = New(cfg)
		if err != nil || o != nil {
			t.Fatal("disabled should be no-op")
		}
		o.Observe(domain.RunEvent{})
		closeObserver(t, o)
	}
}

func TestInvalidConfigurationIsSecretSafe(t *testing.T) {
	for _, cfg := range []Config{
		{Exporter: "file"}, {Exporter: "otlp", Endpoint: "http://user:secret-canary@localhost:4318"},
		{Exporter: "otlp", Endpoint: "http://remote.example:4318"}, {Exporter: "otlp", Endpoint: "https://example.com/?secret-canary"},
		{Exporter: "otlp", Endpoint: "http://localhost:65536"}, {Exporter: "otlp", Endpoint: "http://localhost:0"},
		{Exporter: "otlp", SampleRatio: "NaN"}, {Exporter: "otlp", SampleRatio: "2"}, {Exporter: "otlp", SampleRatio: "bad"},
		{Exporter: "otlp", ServiceName: "secret-canary with spaces"},
	} {
		if _, err := New(cfg); err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatalf("invalid config accepted or leaked: %v", err)
		}
	}
}

func TestProjectionRejectsContentAndInvalidScalars(t *testing.T) {
	for _, kind := range []string{"stage.started", "stage.completed", "stage.failed", "stage.canceled", "turn.started", "turn.completed", "turn.failed", "turn.canceled", "run.failed", "run.canceled", "tool.failed", "tool.completed", "model.attempt_finished"} {
		b, ok := project(event(kind, "stage-1", "turn-1", map[string]any{"tool_call_id": "tool-1", "record_id": "record-1", "error": "PRIVATE_CANARY", "input": "PRIVATE_CANARY", "truncated": true, "replayed": false}))
		if !ok {
			t.Fatal(kind)
		}
		for _, a := range b.attrs {
			if strings.Contains(a.Value.Emit(), "PRIVATE_CANARY") {
				t.Fatal("raw content copied")
			}
		}
	}
	for _, item := range []domain.RunEvent{
		{}, event("stage.started", "", "", nil), event("turn.started", "", "", nil), event("model.request_prepared", "", "", nil), event("tool.started", "", "", nil), event("model.delta", "", "", nil),
	} {
		if _, ok := project(item); ok {
			t.Fatalf("invalid or unsupported boundary accepted: %v", item)
		}
	}
	item := event("run.started", "", "", map[string]any{"total_tokens": json.Number("bad"), "duration_ms": math.Inf(1), "prompt_tokens": -1, "http_status": "200", "model": "not an identifier"})
	item.Timestamp = time.Time{}
	b, ok := project(item)
	if !ok || b.at.IsZero() {
		t.Fatal("missing timestamp fallback")
	}
	for _, a := range b.attrs {
		if a.Key == "agentflow.model" || a.Key == "agentflow.total_tokens" || a.Key == "agentflow.duration_ms" || a.Key == "agentflow.prompt_tokens" || a.Key == "agentflow.http_status" {
			t.Fatal("invalid scalar exported")
		}
	}
	for _, value := range []any{int64(42), int(42), float64(42), json.Number("42")} {
		if n, ok := number(value); !ok || n != 42 {
			t.Fatal(value)
		}
	}
	for _, value := range []any{math.NaN(), nil, int64(-1), json.Number("bad")} {
		if _, ok := number(value); ok {
			t.Fatal(value)
		}
	}
	if identifier(strings.Repeat("a", 129)) || identifier("has spaces") {
		t.Fatal("identifier bounds")
	}
}

func TestStaleSpanIsReapedWithoutCompletion(t *testing.T) {
	e := &recordingExporter{}
	b := defaultLimits()
	b.ttl = time.Millisecond
	b.sweep = time.Millisecond
	o := testObserver(e, b)
	defer closeObserver(t, o)
	o.Observe(event("run.started", "", "", nil))
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		ready := len(e.spans) > 0
		if ready {
			if e.spans[0].Status().Code != codes.Error {
				t.Error("stale span fabricated success")
			}
		}
		e.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("stale span not expired")
		}
	}
}

func TestCollectorRejectionIsSafeAndSamplingZeroIsSilent(t *testing.T) {
	for _, ratio := range []string{"1", "0"} {
		t.Run(ratio, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(503)
				io.WriteString(w, "PRIVATE_CANARY")
			}))
			defer server.Close()
			o, err := New(Config{Exporter: "otlp", Endpoint: server.URL, SampleRatio: ratio})
			if err != nil {
				t.Fatal(err)
			}
			o.Observe(event("run.started", "", "", nil))
			o.Observe(event("run.completed", "", "", nil))
			closeObserver(t, o)
			if ratio == "1" && requests.Load() != 1 {
				t.Fatal("export retries must be disabled")
			}
			if ratio == "0" && requests.Load() != 0 {
				t.Fatal("zero sampling made network requests")
			}
		})
	}
}

type failedExporter struct{}

func (failedExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return errors.New("PRIVATE_CANARY")
}
func (failedExporter) Shutdown(context.Context) error { return errors.New("PRIVATE_CANARY") }
func TestExporterErrorRedaction(t *testing.T) {
	e := safeExporter{failedExporter{}}
	for _, err := range []error{e.ExportSpans(context.Background(), nil), e.Shutdown(context.Background())} {
		if err == nil || strings.Contains(err.Error(), "PRIVATE_CANARY") {
			t.Fatal(err)
		}
	}
}

func TestAttemptMetadataAndConcurrentShutdown(t *testing.T) {
	e := &recordingExporter{}
	o := testObserver(e, defaultLimits())
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("model.request_prepared", "", "", map[string]any{"record_id": "attempt-1", "attempt": 1, "operation": "chat.completion"}))
	o.Observe(event("model.attempt_finished", "", "", map[string]any{"record_id": "attempt-1", "status": "completed", "total_tokens": 60, "usage_estimated": true, "http_time_to_first_token_ms": 5}))
	closeObserver(t, o)
	found := false
	for _, s := range e.spans {
		if s.Name() == "agentflow.model_attempt" {
			for _, a := range s.Attributes() {
				if a.Key == attribute.Key("agentflow.total_tokens") && a.Value.AsFloat64() == 60 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("attempt token metadata missing")
	}
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Go(func() {
			for j := 0; j < 10; j++ {
				o.Observe(event("run.started", "", "", nil))
			}
			closeObserver(t, o)
		})
	}
	group.Wait()
}

func TestMissingTurnFallsBackToStageWithDiagnostic(t *testing.T) {
	e := &recordingExporter{}
	o := testObserver(e, defaultLimits())
	o.Observe(event("run.started", "", "", nil))
	o.Observe(event("stage.started", "stage-1", "", nil))
	o.Observe(event("tool.started", "stage-1", "missing-turn", map[string]any{"tool_call_id": "tool-1"}))
	o.Observe(event("tool.completed", "stage-1", "missing-turn", map[string]any{"tool_call_id": "tool-1"}))
	closeObserver(t, o)
	var stage, tool sdktrace.ReadOnlySpan
	for _, span := range e.spans {
		if span.Name() == "agentflow.stage" {
			stage = span
		}
		if span.Name() == "agentflow.tool" {
			tool = span
		}
	}
	if tool.Parent().SpanID() != stage.SpanContext().SpanID() {
		t.Fatal("missing Turn should retain its observed Stage parent")
	}
	marked := false
	for _, attr := range tool.Attributes() {
		if attr.Key == "agentflow.trace.parent_boundary_missing" && attr.Value.AsBool() {
			marked = true
		}
	}
	if !marked {
		t.Fatal("missing parent was hidden")
	}
}
