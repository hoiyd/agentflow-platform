package tooleval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/toolartifact"
)

// The local provider selects tools and produces answers from actual returned
// snippets. It validates wiring, not real-model competence.
func fixtureProvider(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	lifetime, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "timeout" {
			// HTTP/1 only watches for disconnects after the request body reaches
			// EOF. Waiting first can strand this Handler and Server.Close forever.
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
			case <-lifetime.Done():
			}
			return
		}
		if mode == "provider_error" {
			http.Error(w, "unavailable", 503)
			return
		}
		var request struct {
			Stream   bool             `json:"stream"`
			Tools    []any            `json:"tools"`
			Messages []openai.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var input string
		for _, message := range request.Messages {
			if message.Role == "user" {
				input = message.Content
			}
		}
		lines := strings.Split(input, "\n")
		if len(lines) < 2 {
			t.Errorf("missing task input: %s", input)
			http.Error(w, "bad input", 400)
			return
		}
		ids := strings.Split(strings.TrimPrefix(lines[0], "Requested IDs: "), ", ")
		artifactID := strings.TrimPrefix(lines[1], "Artifact: ")
		providerUsage := usage()
		if mode == "estimated" {
			providerUsage = nil
		}
		if !request.Stream && len(request.Tools) > 0 && mode != "no_evidence" {
			calls := []any{}
			for i, id := range ids {
				args, _ := json.Marshal(map[string]any{"artifact_id": artifactID, "query": id})
				if mode == "invalid_args" {
					args = []byte(`{"artifact_id":"x"}`)
				}
				calls = append(calls, map[string]any{"id": fmt.Sprintf("call-%d", i), "type": "function", "function": map[string]any{"name": toolartifact.SearchToolName, "arguments": string(args)}})
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": calls}}}, "usage": providerUsage})
			return
		}
		facts := []Fact{}
		missing := []string{}
		for _, id := range ids {
			found := false
			for _, message := range request.Messages {
				if message.Role != "tool" {
					continue
				}
				var envelope struct {
					Result domain.ToolArtifactSearchResult `json:"result"`
				}
				if json.Unmarshal([]byte(message.Content), &envelope) != nil {
					continue
				}
				for _, match := range envelope.Result.Matches {
					for _, line := range strings.Split(match.Preview, "\n") {
						if strings.HasPrefix(line, id+" | ") {
							parts := strings.Split(line, " | ")
							facts = append(facts, Fact{id, strings.TrimPrefix(parts[1], "settled_amount_usd="), line})
							found = true
						}
					}
				}
			}
			if !found {
				missing = append(missing, id)
			}
		}
		answer, _ := json.Marshal(map[string]any{"facts": facts, "missing": missing})
		if mode == "wrong_answer" {
			answer = []byte(`{"facts":[],"missing":[]}`)
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": string(answer)}}}, "usage": providerUsage})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		} else {
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(answer)}}}, "usage": providerUsage})
		}
	}))
	t.Cleanup(func() {
		// Release deliberate stalls before waiting for active Handlers to exit.
		cancel()
		server.Close()
	})
	return server
}

func TestTimeoutFixtureClosesAfterCanceledPOST(t *testing.T) {
	server := fixtureProvider(t, "timeout")
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	response, err := client.Post(server.URL, "application/json", strings.NewReader(`{"messages":[]}`))
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected client timeout, got %v", err)
	}
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		// fixture cleanup cancels its own lifetime even if disconnect detection
		// regresses, so this assertion itself does not leave a stuck Handler.
		t.Fatal("timeout fixture did not close after the POST client disconnected")
	}
}

func usage() map[string]int {
	return map[string]int{"prompt_tokens": 1000, "completion_tokens": 80, "total_tokens": 1080}
}
func options() Options {
	return Options{Trials: 2, MaxModelCalls: 30, MaxTotalTokens: 100000, Timeout: time.Second, Revision: "fixture"}
}

func TestTaskEvaluationProductionPathAndReport(t *testing.T) {
	server := fixtureProvider(t, "success")
	report, err := Run(context.Background(), openai.NewClient("fixture-key", server.URL, "fixture-model"), options())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed() || len(report.Samples) != 12 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Summary["without_tools"].Verified != 2 || report.Summary["with_tools"].Verified != 6 {
		t.Fatalf("wrong ablation: %+v", report.Summary)
	}
	for _, sample := range report.Samples {
		if sample.Usage.TotalTokens == 0 || sample.UsageEstimated || sample.Usage.OpenReservations != 0 {
			t.Fatalf("usage lost: %+v", sample)
		}
		if sample.Arm == "with_tools" && (len(sample.Evidence) == 0 || sample.Usage.ToolCalls == 0) {
			t.Fatalf("no execution evidence: %+v", sample)
		}
	}
	report.EvaluationKind = "offline_protocol_fixture"
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fixture-key") || strings.Contains(string(encoded), server.URL) {
		t.Fatal("credentials/endpoint leaked")
	}
	// CI can retain this deterministic wiring report separately from live evals.
	if path := os.Getenv("TOOL_TASK_REPORT_PATH"); path != "" {
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskEvaluationFailuresStayInDenominator(t *testing.T) {
	for _, mode := range []string{"wrong_answer", "invalid_args", "provider_error", "no_evidence", "timeout", "budget", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			server := fixtureProvider(t, mode)
			opts := options()
			opts.Trials = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "budget" {
				opts.MaxModelCalls = 1
			}
			if mode == "timeout" {
				opts.Timeout = 20 * time.Millisecond
			}
			if mode == "cancel" {
				cancel()
			}
			report, err := Run(ctx, openai.NewClient("fixture-key", server.URL, "fixture"), opts)
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed() || len(report.Samples) != 6 || report.Summary["with_tools"].Samples != 3 {
				t.Fatalf("failure hidden: %+v", report)
			}
			if mode == "invalid_args" && (len(report.Samples[1].ToolFailures) != 1 || report.Samples[1].ToolFailures[0].Code != "invalid_arguments") {
				t.Fatalf("Tool failure evidence missing: %+v", report.Samples[1])
			}
			if mode == "provider_error" || mode == "timeout" {
				if report.Samples[0].Status != "failed" || report.Samples[1].Status != "not_evaluated" {
					t.Fatalf("unknown usage did not stop spending: %+v", report.Samples)
				}
			}
		})
	}
}

func TestEvaluationValidationAndDatasetIdentity(t *testing.T) {
	if _, err := Run(context.Background(), nil, options()); err == nil {
		t.Fatal("nil client accepted")
	}
	if _, err := Run(context.Background(), openai.NewClient("", "", ""), options()); err == nil {
		t.Fatal("fallback accepted")
	}
	for _, bad := range []Options{{}, {Trials: 21}, {Trials: 1, MaxModelCalls: 1, MaxTotalTokens: 1, Timeout: 6 * time.Minute}} {
		if _, err := Run(context.Background(), openai.NewClient("key", "", "x"), bad); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	data, err := loadDataset()
	if err != nil || len(data.Content) < 64000 || data.Hash == "" {
		t.Fatalf("dataset: %s %v", data.Hash, err)
	}
	again, _ := loadDataset()
	if data.Hash != again.Hash {
		t.Fatal("unstable dataset hash")
	}
	for _, fact := range data.Records {
		if strings.Contains(data.Content[:256], fact.ID) || !strings.Contains(data.Content, fact.Quote) {
			t.Fatal("fixture leaks answer or lost source")
		}
	}
	if (Report{}).Passed() {
		t.Fatal("empty report passed")
	}
}

func TestEvidenceSensorRejectsPlausibleWrongAnswers(t *testing.T) {
	data, _ := loadDataset()
	fact := data.Records[0]
	good, _ := json.Marshal(map[string]any{"facts": []Fact{fact}, "missing": []string{}})
	duplicate, _ := json.Marshal(map[string]any{"facts": []Fact{fact, fact}, "missing": []string{}})
	read, _ := json.Marshal(domain.ToolArtifactRead{Artifact: domain.ToolArtifact{ID: "a"}, Content: fact.Quote})
	evidence := []Evidence{{Tool: toolartifact.ReadToolName, Result: read}}
	if got := verify(data, data.Cases[0], string(good), "a", evidence, true); len(got) != 0 {
		t.Fatal(got)
	}
	for _, test := range []struct {
		output   string
		artifact string
		evidence []Evidence
	}{
		{"not json", "a", evidence}, {string(good) + " {}", "a", evidence}, {`{"unknown":true}`, "a", evidence},
		{string(duplicate), "a", evidence},
		{string(good), "other-run", evidence}, {string(good), "a", nil},
		{`{"facts":[{"id":"INV-7041","value":"wrong","quote":"wrong"}],"missing":[]}`, "a", evidence},
		{`{"facts":[],"missing":["INV-7041","INV-7041","extra"]}`, "a", evidence},
		{`{"facts":[],"missing":[]}`, "a", evidence},
	} {
		if got := verify(data, data.Cases[0], test.output, test.artifact, test.evidence, true); len(got) == 0 {
			t.Fatalf("bad answer accepted: %+v", test)
		}
	}
	search, _ := json.Marshal(domain.ToolArtifactSearchResult{Artifact: domain.ToolArtifact{ID: "a"}, Query: "INV-0000", ScannedBytes: 70000})
	if !observedEvidence([]Evidence{{Tool: toolartifact.SearchToolName, Result: search}}, "a", "INV-0000", "", true) {
		t.Fatal("absence not recognized")
	}
	if observedEvidence([]Evidence{{Tool: toolartifact.SearchToolName, Result: search}}, "a", "different", "", true) {
		t.Fatal("wrong query proves absence")
	}
	if observedEvidence([]Evidence{{Tool: toolartifact.SearchToolName, Result: json.RawMessage(`{`)}}, "a", "x", "quote", false) {
		t.Fatal("invalid evidence accepted")
	}
	if len(collectEvidence([]domain.RunEvent{{Type: domain.EventToolFailed}, {Type: domain.EventToolCompleted}})) != 0 {
		t.Fatal("invalid trace included")
	}
}

func TestMissingProviderUsageRemainsExplicitlyEstimated(t *testing.T) {
	server := fixtureProvider(t, "estimated")
	opts := options()
	opts.Trials = 1
	report, err := Run(context.Background(), openai.NewClient("fixture", server.URL, "fixture"), opts)
	if err != nil || !report.Passed() {
		t.Fatalf("estimated evaluation: %+v %v", report, err)
	}
	for _, sample := range report.Samples {
		if !sample.UsageEstimated || sample.Usage.TotalTokens <= 0 {
			t.Fatalf("missing usage reported as free: %+v", sample)
		}
	}
}

func TestUnavailableTemporaryStorageFailsBeforeModelCall(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/missing-parent")
	if _, err := Run(context.Background(), openai.NewClient("fixture", "http://127.0.0.1:1", "fixture"), options()); err == nil {
		t.Fatal("unavailable temporary storage accepted")
	}
}
