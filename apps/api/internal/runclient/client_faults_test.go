package runclient

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMain(m *testing.M) {
	// Fault servers must never read or receive an operator's CLI session.
	for _, key := range []string{"AGENTFLOW_API_URL", "AGENTFLOW_WEB_URL", "AGENTFLOW_WORKSPACE_ID", "AGENTFLOW_SESSION_FILE"} {
		os.Unsetenv(key)
	}
	os.Exit(m.Run())
}

// Failure inventory: docs/runtime/headless-run-client.md. These controlled wire
// faults complement the CLI -> production application -> Postgres gate in app.
func TestSubmissionNeverRetriesUnknownOrRejectedOutcome(t *testing.T) {
	for _, scenario := range []struct {
		name, reply, code string
		status, exit      int
	}{
		{"lost", "", "submission_unknown", 200, 7},
		{"partial", "event: run.started\ndata: {\"run_id\":\"run_partial\"}", "submission_unknown", 200, 7},
		{"conversation acknowledged", "event: conversation\ndata: {\"conversation_id\":\"conv_known\"}\n\n", "submission_unknown", 200, 7},
		{"unauthorized", `{}`, "http_error", 401, 4},
		{"overload", `{}`, "http_error", 429, 8},
		{"server failure", `{}`, "submission_unknown", 500, 7},
		{"reflected retry header", `{}`, "http_error", 429, 8},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Retry-After", "3")
				if scenario.name == "reflected retry header" {
					w.Header().Set("Retry-After", "SECRET_HEADER_CANARY")
				}
				w.WriteHeader(scenario.status)
				fmt.Fprint(w, scenario.reply)
			}))
			defer server.Close()
			request := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(request, []byte(`{"message":"synthetic report","mode":"single"}`), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostic bytes.Buffer
			exit := Execute(context.Background(), []string{"start", "--api-url", server.URL, "--workspace", "1", "--request", request}, &out, &diagnostic)
			if exit != scenario.exit || requests.Load() != 1 || !strings.Contains(diagnostic.String(), scenario.code) {
				t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", exit, requests.Load(), &out, &diagnostic)
			}
			if scenario.name == "conversation acknowledged" && (!strings.Contains(diagnostic.String(), "conv_known") || !strings.Contains(diagnostic.String(), "request_sha256")) {
				t.Fatalf("lost submission identity: %s", &diagnostic)
			}
			if strings.Contains(diagnostic.String(), "SECRET_HEADER_CANARY") {
				t.Fatal("arbitrary Retry-After content escaped into diagnostics")
			}
		})
	}
}

func TestObservationReconnectsWithoutLosingOrDuplicatingDurableEvents(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			fmt.Fprint(w, "id: 1\nevent: tool.started\ndata: {\"run_id\":\"run_fixture\",\"sequence\":1,\"type\":\"tool.started\"}\n\n")
			fmt.Fprint(w, "id: 2\nevent: tool.completed\ndata: {")
		case 2:
			if r.Header.Get("Last-Event-ID") != "1" {
				t.Errorf("cursor=%s", r.Header.Get("Last-Event-ID"))
			}
			fmt.Fprint(w, "id: 1\nevent: tool.started\ndata: {\"run_id\":\"run_fixture\",\"sequence\":1,\"type\":\"tool.started\"}\n\n")
			fmt.Fprint(w, "id: 2\nevent: tool.completed\ndata: {\"run_id\":\"run_fixture\",\"sequence\":2,\"type\":\"tool.completed\"}\n\n")
			fmt.Fprint(w, "id: 2\nevent: run.snapshot\ndata: {\"as_of_sequence\":2,\"run\":{\"run_id\":\"run_fixture\",\"status\":\"completed\"}}\n\n")
		default:
			t.Error("unnecessary reconnect")
		}
	}))
	defer server.Close()
	client, err := New(Options{APIURL: server.URL, Workspace: "1"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cursor, err := client.Observe(context.Background(), "run_fixture", 0, &out)
	if err != nil || cursor != 2 || requests.Load() != 2 || strings.Count(out.String(), `"type":"tool.started"`) != 1 || strings.Count(out.String(), `"type":"tool.completed"`) != 1 {
		t.Fatalf("cursor=%d requests=%d err=%v output=%s", cursor, requests.Load(), err, &out)
	}
}

func TestClientRejectsUnsafeCredentialsAndRedirects(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:password@example.com", "https://example.com?token=x", "https://example.com/#x"} {
		if _, err := New(Options{APIURL: raw, Workspace: "1"}); err == nil {
			t.Errorf("accepted unsafe endpoint")
		}
	}
	file := filepath.Join(t.TempDir(), "session")
	if err := os.WriteFile(file, []byte("agentflow_session="+strings.Repeat("a", 43)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{APIURL: "https://example.com", Workspace: "1", SessionFile: file}); err == nil {
		t.Fatal("accepted exposed session file")
	}
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	code := Execute(context.Background(), []string{"result", "--api-url", server.URL, "--workspace", "1", "--session-file", file, "--run", "run_fixture"}, &out, &diagnostic)
	if code == 0 || reached.Load() != 0 || strings.Contains(out.String()+diagnostic.String(), strings.Repeat("a", 43)) {
		t.Fatalf("redirect/credential boundary: %d %s %s", code, &out, &diagnostic)
	}
}
