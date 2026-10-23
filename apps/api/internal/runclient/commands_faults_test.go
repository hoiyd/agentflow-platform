package runclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCommandValidationDoesNotSubmit(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"start", "--token", "DO_NOT_ECHO"}, {"start", "--deadline", "DO_NOT_ECHO"}, {"observe", "--run", "../foreign"}, {"observe", "--run", "run_valid", "--after", "-1"}, {"start", "--request", "missing", "--detach", "--output", "evidence"}, {"result", "--run", "run_valid", "positional"}, {"cancel", "--run", "run_valid", "--deadline", "0s"}} {
		var out, diagnostic bytes.Buffer
		if code := Execute(context.Background(), args, &out, &diagnostic); code != 2 || strings.Contains(diagnostic.String(), "DO_NOT_ECHO") {
			t.Fatalf("options: %d %s", code, &diagnostic)
		}
	}
	for _, args := range [][]string{nil, {"--help"}, {"start", "--help"}} {
		var out bytes.Buffer
		if Execute(context.Background(), args, &out, io.Discard) != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal("missing help")
		}
	}
	for _, workspace := range []string{"", "0", "-1", " 1", "a", "12345678901234567890"} {
		if _, err := New(Options{APIURL: "https://example.com", Workspace: workspace}); err == nil {
			t.Errorf("invalid Workspace %q", workspace)
		}
	}
	for _, content := range []string{`{"message":"x","unknown":true}`, `{"message":"x","mode":"invalid"}`, `{"message":"x","workspace_id":"2"}`, `{}`, `{"message":"x"} {}`} {
		path := filepath.Join(t.TempDir(), "request.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if code := Execute(context.Background(), []string{"start", "--workspace", "1", "--request", path}, io.Discard, io.Discard); code != 2 {
			t.Fatalf("invalid request %s returned %d", content, code)
		}
	}
}

func TestObservationRejectsMalformedOrForeignProtocol(t *testing.T) {
	for _, item := range []struct{ name, frame, code string }{
		{"foreign", `event: tool.started` + "\ndata: " + `{"run_id":"foreign","type":"tool.started"}` + "\n\n", "protocol_error"},
		{"negative", "event: tool.started\ndata: {\"run_id\":\"run_fixture\",\"sequence\":-1,\"type\":\"tool.started\"}\n\n", "protocol_error"},
		{"cursor mismatch", "id: 2\nevent: tool.started\ndata: {\"run_id\":\"run_fixture\",\"sequence\":1,\"type\":\"tool.started\"}\n\n", "protocol_error"},
		{"snapshot mismatch", "event: run.snapshot\ndata: {\"as_of_sequence\":9,\"run\":{\"run_id\":\"run_fixture\",\"status\":\"completed\"}}\n\n", "protocol_error"},
		{"error", "event: error\ndata: {\"code\":\"permission_denied\"}\n\n", "stream_rejected"},
		{"invalid error", "event: error\ndata: {\n\n", "protocol_error"},
		{"oversize", "data: " + strings.Repeat("x", maxFrameBytes) + "\n\n", "observation_lost"},
	} {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, item.frame)
			}))
			defer server.Close()
			client, err := New(Options{APIURL: server.URL, Workspace: "1"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.http.CloseIdleConnections()
			after, err := client.Observe(context.Background(), "run_fixture", 0, io.Discard)
			var detail *Error
			if after != 0 || !errors.As(err, &detail) || detail.Code != item.code {
				t.Fatalf("after=%d err=%v", after, err)
			}
		})
	}
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, errors.New("broken consumer") }

func TestObservationHonorsConsumerFailureAndClientCancellation(t *testing.T) {
	var canceledRuns atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			canceledRuns.Add(1)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: run.snapshot\ndata: {\"as_of_sequence\":0,\"run\":{\"run_id\":\"run_fixture\",\"status\":\"running\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := New(Options{APIURL: server.URL, Workspace: "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	if after, err := client.Observe(context.Background(), "run_fixture", 0, failingOutput{}); after != 0 || err == nil {
		t.Fatalf("broken output: %d %v", after, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	writer := cancelOutput{cancel: cancel}
	var diagnostic bytes.Buffer
	code := Execute(ctx, []string{"observe", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture"}, writer, &diagnostic)
	if code != 130 || canceledRuns.Load() != 0 || !strings.Contains(diagnostic.String(), "client_interrupted") {
		t.Fatalf("client cancellation: %d %s", code, &diagnostic)
	}
	if code := Execute(context.Background(), []string{"observe", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture", "--deadline", "1ns"}, io.Discard, io.Discard); code != 124 {
		t.Fatalf("deadline: %d", code)
	}
}

type cancelOutput struct{ cancel context.CancelFunc }

func (w cancelOutput) Write(data []byte) (int, error) { w.cancel(); return len(data), nil }

func TestResultExportsOnlyAcceptedObjectsAndRetainsIdentity(t *testing.T) {
	for _, scenario := range []struct {
		status, verification string
		value                any
		exit                 int
	}{{"completed", "passed", map[string]any{"report": "ok"}, 0}, {"failed", "failed", nil, 3}, {"waiting_for_user", "pending", nil, 5}, {"canceled", "pending", nil, 6}, {"running", "pending", nil, 5}} {
		t.Run(scenario.status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/result") {
					if scenario.value == nil {
						w.WriteHeader(409)
					}
					json.NewEncoder(w).Encode(map[string]any{"run_id": "run_fixture", "status": scenario.status, "verification_status": scenario.verification, "value": scenario.value})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "run_fixture", "workspace_id": "1", "status": scenario.status, "verification_status": scenario.verification, "completion_contract": map[string]any{"output": map[string]any{"mode": "local", "verifier_id": "report"}}}, "structured_result": map[string]any{"run_id": "run_fixture", "status": scenario.status, "verification_status": scenario.verification, "value": scenario.value}})
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "result.json")
			var out, diagnostic bytes.Buffer
			exit := Execute(context.Background(), []string{"result", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture", "--output", file}, &out, &diagnostic)
			if exit != scenario.exit {
				t.Fatalf("result %d %s %s", exit, &out, &diagnostic)
			}
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("evidence must be private")
			}
			if code := Execute(context.Background(), []string{"result", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture", "--output", file}, io.Discard, io.Discard); code != 1 {
				t.Fatal("overwrote existing evidence")
			}
		})
	}
}

func TestResultAndCancelRejectCorruptOrForeignServerData(t *testing.T) {
	for _, reply := range []string{`{`, `{} {}`, strings.Repeat(" ", maxJSONBytes+1), `{"run":{"id":"foreign","workspace_id":"1"}}`, `{"run":{"id":"run_fixture","workspace_id":"2"}}`, `{"run":{"id":"run_fixture","workspace_id":"1","status":"failed","verification_status":"failed"},"structured_result":{"run_id":"run_fixture","status":"failed","verification_status":"failed","value":{"not":"accepted"}}}`, `{"run":{"id":"run_fixture","workspace_id":"1","status":"completed","verification_status":"passed"},"structured_result":{"run_id":"foreign"}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, reply) }))
		code := Execute(context.Background(), []string{"result", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture"}, io.Discard, io.Discard)
		server.Close()
		if code != 1 {
			t.Fatalf("corrupt response accepted: %d", code)
		}
	}
	for _, status := range []int{200, 500} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
			fmt.Fprint(w, `{"id":"other","workspace_id":"1"}`)
		}))
		code := Execute(context.Background(), []string{"cancel", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture"}, io.Discard, io.Discard)
		server.Close()
		if code != 1 || calls.Load() != 1 {
			t.Fatalf("cancel fault: code=%d calls=%d", code, calls.Load())
		}
	}
}

func TestObserverRecoversLagAndServerRestartWithoutResubmission(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("observation must not create a Run")
		}
		switch attempts.Add(1) {
		case 1:
			w.WriteHeader(503)
		case 2:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: error\ndata: {\"code\":\"event_subscription_lost\"}\n\n")
		case 3:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": keepalive\n\nid: 0\nevent: run.snapshot\ndata: {\"as_of_sequence\":0,\"run\":{\"run_id\":\"run_fixture\",\"status\":\"failed_recoverable\"}}\n\n")
		default:
			t.Error("unnecessary reconnect")
		}
	}))
	defer server.Close()
	client, err := New(Options{APIURL: server.URL, Workspace: "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	if cursor, err := client.Observe(context.Background(), "run_fixture", 0, io.Discard); cursor != 0 || err != nil || attempts.Load() != 3 {
		t.Fatalf("restart: %d %v attempts=%d", cursor, err, attempts.Load())
	}
}

func TestCredentialFileAndEvidenceOutputFailures(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "session")
	for _, content := range []string{"", "Bearer not-a-session", "agentflow_session=bad", strings.Repeat("x", 4097), "__Host-agentflow_session=" + strings.Repeat("x", 43)} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(Options{APIURL: "http://localhost:8080", Workspace: "1", SessionFile: file}); err == nil {
			t.Fatal("accepted invalid/insecure cookie")
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{APIURL: "https://example.com", Workspace: "1", SessionFile: link}); err == nil {
		t.Fatal("accepted session symlink")
	}
	t.Setenv("AGENTFLOW_API_URL", "http://localhost:8080")
	t.Setenv("AGENTFLOW_WORKSPACE_ID", "1")
	if code := Execute(context.Background(), []string{"start", "--request", filepath.Join(dir, "missing")}, io.Discard, io.Discard); code != 2 {
		t.Fatal("missing request file")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"run":{"id":"run_fixture","workspace_id":"1","status":"completed","verification_status":"not_required"}}`)
	}))
	defer server.Close()
	args := []string{"result", "--api-url", server.URL, "--workspace", "1", "--run", "run_fixture"}
	if code := Execute(context.Background(), args, failingOutput{}, io.Discard); code != 1 {
		t.Fatal("broken stdout accepted")
	}
	if code := Execute(context.Background(), append(args, "--output", filepath.Join(dir, "missing", "result.json")), io.Discard, io.Discard); code != 1 {
		t.Fatal("missing output directory accepted")
	}
}
