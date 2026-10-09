package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Exercise production composition, real HTTP/SSE, physical provider attempts,
// tools and Postgres commits; only the model and OTLP receiver are fixtures.
// TEST_OTLP_ENDPOINT optionally forwards the same protobuf to a real Collector.
func TestProductionTelemetryCommittedExecution(t *testing.T) {
	if os.Getenv("TEST_JAEGER_URL") != "" && os.Getenv("TEST_OTLP_ENDPOINT") == "" {
		t.Fatal("TEST_JAEGER_URL requires TEST_OTLP_ENDPOINT")
	}
	databaseURL := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	provider := newBrowserProvider()
	model := httptest.NewServer(http.HandlerFunc(provider.respond))
	defer model.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=PRIVATE_OTEL_CANARY")
	t.Setenv("OTEL_FIXTURE_KEY", "PRIVATE_OTEL_CANARY")
	t.Setenv("EMBEDDING_API_KEY", "PRIVATE_OTEL_CANARY")
	t.Setenv("TAVILY_API_KEY", "")
	var mu sync.Mutex
	var spans []*tracepb.Span
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		request := &collectorpb.ExportTraceServiceRequest{}
		if err = proto.Unmarshal(data, request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		text, _ := protojson.Marshal(request)
		if strings.Contains(string(text), "PRIVATE_OTEL_CANARY") || r.Header.Get("Authorization") != "" {
			t.Error("content/credential crossed telemetry boundary")
		}
		mu.Lock()
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				spans = append(spans, scope.Spans...)
			}
		}
		mu.Unlock()
		if endpoint := os.Getenv("TEST_OTLP_ENDPOINT"); endpoint != "" {
			forward, err := http.NewRequestWithContext(r.Context(), "POST", strings.TrimSuffix(endpoint, "/")+"/v1/traces", bytes.NewReader(data))
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			forward.Header.Set("Content-Type", "application/x-protobuf")
			response, err := (&http.Client{Timeout: 2 * time.Second}).Do(forward)
			if err != nil {
				t.Errorf("Collector forwarding failed: %v", err)
				w.WriteHeader(502)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				t.Errorf("Collector status: %d", response.StatusCode)
				w.WriteHeader(502)
				return
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	routes, _ := json.Marshal(map[string]any{"routes": []any{map[string]any{
		"id": "otel-fixture", "model": "fixture-model", "base_url": model.URL, "credential_environment": "OTEL_FIXTURE_KEY", "request_timeout_seconds": 20,
		"capabilities": map[string]bool{"tool_calling": true, "streaming": true, "structured_output": true}, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100,
		"pricing": map[string]string{"source": "fixture"},
	}}})
	route := filepath.Join(root, "routes.json")
	if err := os.WriteFile(route, routes, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.DatabaseURL, cfg.ModelRouteConfigPath = databaseURL, route
	cfg.ToolConfigPath, cfg.TrustedSkillDirectories = filepath.Join(root, "tools.json"), root
	cfg.AuthMode, cfg.SandboxEnabled = "local", false
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = model.URL, "fixture-embedding", 1536
	cfg.ContextCompactionMode, cfg.MemoryAdaptiveExtractionMode, cfg.RerankerMode = "off", "off", "heuristic"
	cfg.ModelRequestCaptureMode, cfg.ModelRetryMaxAttempts = "full", 1
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute, cfg.RunMaxModelCalls = 0, 0, 50
	cfg.OTelTracesExporter, cfg.OTelEndpoint, cfg.OTelServiceName, cfg.OTelSampleRatio = "otlp", receiver.URL, "agentflow-otel-fixture", "1"
	application, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(application.server.Handler)
	defer server.Close()
	client := &http.Client{Timeout: 30 * time.Second}
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		data, _ := json.Marshal(map[string]string{"message": "owner-tools: PRIVATE_OTEL_CANARY check time zones", "mode": mode})
		response, err := client.Post(server.URL+"/api/chat", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !bytes.Contains(body, []byte("event: done")) {
			t.Fatalf("%s chat failed: status=%d error=%v %s", mode, response.StatusCode, err, body)
		}
		if mode == "multi_agent" {
			runs, err := application.store.ListRuns()
			if err != nil {
				t.Fatal(err)
			}
			waiting := ""
			for _, run := range runs {
				if run.Status == domain.RunWaitingForUser {
					waiting = run.ID
				}
			}
			if waiting == "" {
				t.Fatal("Multi must preserve its real plan-approval boundary")
			}
			response, err = client.Post(server.URL+"/api/runs/"+waiting+"/continue", "application/json", strings.NewReader(`{"plan":"owner-tools: check the requested time zones, then review the result"}`))
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || !bytes.Contains(body, []byte("event: done")) {
				t.Fatalf("Multi Resume failed: %v %s", err, body)
			}
		}
	}
	runs, err := application.store.ListRuns()
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs: %d %v", len(runs), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := application.telemetry.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	byRun := map[string][]*tracepb.Span{}
	for _, span := range spans {
		for _, attr := range span.Attributes {
			if attr.Key == "agentflow.run.id" {
				byRun[attr.Value.GetStringValue()] = append(byRun[attr.Value.GetStringValue()], span)
			}
		}
	}
	for _, run := range runs {
		if run.Status != domain.RunCompleted {
			t.Fatalf("run %s: %s %s", run.ID, run.Status, run.Error)
		}
		replay, found, err := application.store.GetRunReplay(run.ID)
		if err != nil || !found {
			t.Fatalf("replay: %v %t", err, found)
		}
		counts := map[string]int{}
		for _, event := range replay.RunEvents {
			switch event.Type {
			case domain.EventRunStarted, domain.EventRunResumed:
				counts["agentflow.run"]++
			case domain.EventStageStarted:
				counts["agentflow.stage"]++
			case domain.EventTurnStarted:
				counts["agentflow.turn"]++
			case domain.EventModelRequestPrepared:
				counts["agentflow.model_attempt"]++
			case domain.EventToolStarted:
				counts["agentflow.tool"]++
			}
		}
		observed := map[string]int{}
		ids := map[string]bool{}
		for _, span := range byRun[run.ID] {
			observed[span.Name]++
			ids[hex.EncodeToString(span.SpanId)] = true
		}
		for name, count := range counts {
			if observed[name] != count {
				t.Fatalf("run %s %s exported=%d committed=%d", run.ID, name, observed[name], count)
			}
		}
		if observed["agentflow.model_attempt"] == 0 || observed["agentflow.tool"] == 0 {
			t.Fatalf("real attempt/tool path untested: %v", observed)
		}
		if run.RuntimeSnapshot.Mode == "single" && observed["agentflow.stage"] != 0 {
			t.Fatal("invented stage for Single")
		}
		for _, span := range byRun[run.ID] {
			if span.Name != "agentflow.run" && !ids[hex.EncodeToString(span.ParentSpanId)] {
				t.Fatalf("orphan span: %s", span.Name)
			}
		}
		var traceIDs []string
		for _, span := range byRun[run.ID] {
			if span.Name == "agentflow.run" {
				traceIDs = append(traceIDs, hex.EncodeToString(span.TraceId))
			}
		}
		if run.RuntimeSnapshot.Mode == "multi_agent" && len(traceIDs) != 2 {
			t.Fatal("plan approval/Resume must produce two execution segments")
		}
		if endpoint := os.Getenv("TEST_JAEGER_URL"); endpoint != "" {
			verifyJaegerTrace(t, endpoint, run.ID, traceIDs, counts)
		}
		evidence, _ := json.Marshal(map[string]any{"run_id": run.ID, "mode": run.RuntimeSnapshot.Mode, "status": run.Status, "trace_ids": traceIDs, "committed_events": len(replay.RunEvents), "exported_spans": observed, "provider": "deterministic fixture", "capture_mode": "full", "content_exported": false, "collector_forwarded": os.Getenv("TEST_OTLP_ENDPOINT") != "", "jaeger_verified": os.Getenv("TEST_JAEGER_URL") != ""})
		t.Logf("OTEL_EVIDENCE %s", evidence)
	}
}

// Jaeger v3 serves OTLP JSON. Wait for the storage condition, not an arbitrary
// startup sleep, and compare every segment with the committed source boundaries.
func verifyJaegerTrace(t *testing.T, endpoint, runID string, traceIDs []string, expected map[string]int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		counts := map[string]int{}
		for _, id := range traceIDs {
			request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(endpoint, "/")+"/api/v3/traces/"+id, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				continue
			}
			var envelope struct {
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(data, &envelope) != nil {
				t.Fatal("Jaeger v3 returned invalid JSON")
			}
			traces := &collectorpb.ExportTraceServiceRequest{}
			if err = protojson.Unmarshal(envelope.Result, traces); err != nil {
				t.Fatalf("Jaeger OTLP decode: %v", err)
			}
			if bytes.Contains(data, []byte("PRIVATE_OTEL_CANARY")) {
				t.Fatal("content reached Jaeger")
			}
			for _, resource := range traces.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						matched := false
						for _, attr := range span.Attributes {
							if attr.Key == "agentflow.run.id" && attr.Value.GetStringValue() == runID {
								matched = true
							}
						}
						if matched {
							counts[span.Name]++
						}
					}
				}
			}
		}
		ready := true
		for name, count := range expected {
			if counts[name] != count {
				ready = false
			}
		}
		if ready {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("Jaeger spans differ from commits for %s: got=%v want=%v", runID, counts, expected)
		}
	}
}
