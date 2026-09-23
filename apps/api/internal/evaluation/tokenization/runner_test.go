package tokenization

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
)

func TestCaptureUsesProductionContextAndRequestSerialization(t *testing.T) {
	options := testOptions("http://127.0.0.1:1/v1")
	config := contextassembly.DefaultConfig()
	config.ContextWindowTokens, config.OutputReserveTokens, config.SafetyMarginTokens = 4096, 256, 256
	for _, item := range corpus(3584) {
		observation, err := captureRequest(t.Context(), options, config, item)
		if item.rejectPreflight {
			if err == nil || len(observation.Payload) != 0 {
				t.Fatalf("%s was not rejected before transport: %v", item.id, err)
			}
			continue
		}
		if err != nil || len(observation.Payload) == 0 || observation.ContextManifestID == "" {
			t.Fatalf("%s request was not captured: observation=%#v err=%v", item.id, observation, err)
		}
		if observation.SourceTokenBreakdown[contextassembly.SourceCurrentInput] <= 0 {
			t.Fatalf("%s omitted current input estimate: %#v", item.id, observation.SourceTokenBreakdown)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(observation.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		switch item.id {
		case "tool_schema":
			if len(payload["tools"]) == 0 || observation.SourceTokenBreakdown[contextassembly.SourceToolDefinition] <= 0 {
				t.Fatal("Tool definitions missing from captured request or estimate")
			}
		case "history":
			if observation.SourceTokenBreakdown[contextassembly.SourceHistory] <= 0 {
				t.Fatal("history was not assembled")
			}
		case "rag":
			if observation.SourceTokenBreakdown[contextassembly.SourceKnowledge] <= 0 || !strings.Contains(string(payload["messages"]), "untrusted_knowledge_context") {
				t.Fatal("retrieved knowledge was not assembled")
			}
		}
	}
}

func TestRunCalibrationAndIdentity(t *testing.T) {
	server := calibrationServer(t, false, false, "fixture-template", false)
	defer server.Close()
	options := testOptions(server.URL + "/v1")
	report, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || len(report.Samples) != len(corpus(3584)) {
		t.Fatalf("unexpected gate or corpus: %#v", report.Gate)
	}
	for _, sample := range report.Samples {
		if sample.Status != "passed" {
			t.Fatalf("sample %s failed: %s", sample.ID, sample.Failure)
		}
		if sample.PreflightRejected {
			if sample.RequestSHA256 != "" || sample.BackendInputTokens != 0 || sample.CounterfactualTokens <= 0 {
				t.Fatal("rejected input was incorrectly treated as an admitted request")
			}
			continue
		}
		if sample.ProviderPromptTokens == nil || *sample.ProviderPromptTokens != sample.BackendInputTokens || sample.UsageEstimated {
			t.Fatalf("sample %s lacks exact usage: %#v", sample.ID, sample)
		}
		if sample.ID == "tool_schema" && sample.ToolTemplateOverhead <= 0 {
			t.Fatal("Tool schema had no measured template overhead")
		}
	}
	if report.Target.ChatTemplateSHA256 == "" || report.Target.GGUFSHA256 != options.GGUFSHA256 || report.Identity.DatasetHash == "" {
		t.Fatalf("target identity is incomplete: %#v", report.Target)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "Release checklist") || strings.Contains(string(encoded), "Reply OK without calling") {
		t.Fatal("report retained raw prompt content")
	}
}

func TestRunFailsWhenProviderUsageIsMissing(t *testing.T) {
	server := calibrationServer(t, true, false, "fixture-template", false)
	defer server.Close()
	report, err := Run(t.Context(), testOptions(server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Gate.Passed || report.Gate.BlockingFailures == 0 {
		t.Fatalf("missing provider usage passed the gate: %#v", report.Gate)
	}
	for _, sample := range report.Samples {
		if sample.PreflightRejected {
			continue
		}
		if !sample.UsageEstimated || sample.ProviderPromptTokens != nil || sample.UsageStatus != "missing; estimate only" {
			t.Fatalf("missing usage was misreported as exact: %#v", sample)
		}
	}
}

func TestRunFailsOnTemplateCountMismatch(t *testing.T) {
	server := calibrationServer(t, false, true, "fixture-template", false)
	defer server.Close()
	report, err := Run(t.Context(), testOptions(server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Gate.Passed || !strings.Contains(strings.Join(report.Gate.Reasons, " "), "template tokenization disagrees") {
		t.Fatalf("template drift passed the gate: %#v", report.Gate)
	}
}

func TestRunRejectsBackendWithoutTemplateIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"build_info":"fixture-build"}`)
	}))
	defer server.Close()
	if _, err := Run(t.Context(), testOptions(server.URL+"/v1")); err == nil || !strings.Contains(err.Error(), "chat template identity") {
		t.Fatalf("missing template identity accepted: %v", err)
	}
	options := testOptions(server.URL + "/v1")
	options.GGUFSHA256 = ""
	if _, err := Run(t.Context(), options); err == nil {
		t.Fatal("invalid target identity accepted")
	}
}

func testOptions(baseURL string) Options {
	return Options{BaseURL: baseURL, Model: "fixture-model", ModelArtifact: "fixture-model@commit/model.gguf",
		GGUFSHA256: strings.Repeat("a", 64), ContextWindowTokens: 4096, OutputReserveTokens: 256,
		SafetyMarginTokens: 256, APIKey: "local", RequestTimeout: 5 * time.Second}
}

func calibrationServer(t *testing.T, missingUsage, mismatchedTemplate bool, template string, falseReject bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			_ = json.NewEncoder(w).Encode(backendProps{BuildInfo: "fixture-build", ChatTemplate: template, BOSToken: "<bos>", EOSToken: "<eos>"})
			return
		}
		var payload json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/apply-template":
			_ = json.NewEncoder(w).Encode(map[string]any{"prompt": fixturePrompt(payload)})
		case "/tokenize":
			var input struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(payload, &input)
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, fixtureTokenCount(input.Content))})
		case "/v1/chat/completions/input_tokens":
			count := fixtureTokenCount(fixturePrompt(payload))
			if falseReject && bytes.Contains(payload, []byte("overflow overflow")) {
				count = 3500
			}
			if mismatchedTemplate {
				count++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": count})
		case "/v1/chat/completions":
			response := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "OK"}}}}
			if !missingUsage {
				count := fixtureTokenCount(fixturePrompt(payload))
				if mismatchedTemplate {
					count++
				}
				response["usage"] = map[string]any{"prompt_tokens": count}
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func fixturePrompt(payload []byte) string {
	content, _ := requestContent(payload)
	var request map[string]json.RawMessage
	_ = json.Unmarshal(payload, &request)
	return fmt.Sprintf("<bos>%s<eos>%s", content, request["tools"])
}

func fixtureTokenCount(content string) int { return max(1, (len(content)+3)/4) }

func TestValidateRejectsUnpinnedTokenizer(t *testing.T) {
	options := testOptions("http://localhost:8081/v1")
	options.GGUFSHA256 = ""
	if err := validate(options); err == nil {
		t.Fatal("missing GGUF hash accepted")
	}
	options.GGUFSHA256 = strings.Repeat("a", 64)
	options.OutputReserveTokens = options.ContextWindowTokens
	if err := validate(options); err == nil {
		t.Fatal("invalid context limits accepted")
	}
}

func TestCalibrationIdentityChangesWithTokenizerOrTemplate(t *testing.T) {
	firstServer := calibrationServer(t, false, false, "template-one", false)
	defer firstServer.Close()
	first, err := Run(t.Context(), testOptions(firstServer.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	secondServer := calibrationServer(t, false, false, "template-two", false)
	defer secondServer.Close()
	second, err := Run(t.Context(), testOptions(secondServer.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity.DatasetHash == second.Identity.DatasetHash {
		t.Fatal("chat template change did not alter calibration identity")
	}
	options := testOptions(firstServer.URL + "/v1")
	options.GGUFSHA256 = strings.Repeat("b", 64)
	third, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity.DatasetHash == third.Identity.DatasetHash {
		t.Fatal("GGUF/tokenizer change did not alter calibration identity")
	}
}

func TestRunReportsCounterfactualFalseRejectionWithoutWeakeningSafetyGate(t *testing.T) {
	server := calibrationServer(t, false, false, "fixture-template", true)
	defer server.Close()
	report, err := Run(t.Context(), testOptions(server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || report.Summary.FalseRejections != 1 {
		t.Fatalf("counterfactual rejection was not distinguished from a safety failure: %#v", report)
	}
	last := report.Samples[len(report.Samples)-1]
	if !last.PreflightRejected || !last.WouldFitIfAdmitted || last.CounterfactualTokens != 3500 {
		t.Fatalf("incorrect counterfactual evidence: %#v", last)
	}
}

func TestNativeBackendRejectsIncompleteAndFailedResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		call func(context.Context, nativeBackend) error
	}{
		{"missing input count", `{}`, func(ctx context.Context, backend nativeBackend) error {
			_, err := backend.inputTokens(ctx, []byte(`{}`))
			return err
		}},
		{"empty template", `{}`, func(ctx context.Context, backend nativeBackend) error {
			_, err := backend.applyTemplate(ctx, []byte(`{}`))
			return err
		}},
		{"missing token list", `{}`, func(ctx context.Context, backend nativeBackend) error {
			_, err := backend.tokenize(ctx, "hello", true)
			return err
		}},
		{"missing choices", `{}`, func(ctx context.Context, backend nativeBackend) error {
			_, err := backend.completionUsage(ctx, []byte(`{}`))
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			backend := nativeBackend{baseURL: server.URL, client: server.Client()}
			if err := test.call(t.Context(), backend); err == nil {
				t.Fatal("incomplete backend response accepted")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	backend := nativeBackend{baseURL: server.URL, client: server.Client()}
	if err := backend.request(t.Context(), http.MethodGet, "/props", nil, &backendProps{}); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("backend HTTP failure was hidden: %v", err)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "not JSON") }))
	defer broken.Close()
	backend.baseURL = broken.URL
	if err := backend.request(t.Context(), http.MethodGet, "/props", nil, &backendProps{}); err == nil {
		t.Fatal("malformed backend response accepted")
	}
	backend.baseURL = "://bad"
	if err := backend.request(t.Context(), http.MethodGet, "/props", nil, &backendProps{}); err == nil {
		t.Fatal("invalid backend URL accepted")
	}
}

func TestCapturedRequestHelpersRejectMalformedData(t *testing.T) {
	if _, err := requestContent([]byte(`{`)); err == nil {
		t.Fatal("malformed request content accepted")
	}
	if _, err := requestContent([]byte(`{}`)); err == nil {
		t.Fatal("empty request content accepted")
	}
	if _, err := removeTools([]byte(`{`)); err == nil {
		t.Fatal("malformed Tool request accepted")
	}
	if _, err := removeTools([]byte(`{"messages":[]}`)); err == nil {
		t.Fatal("Tool-free request accepted as Tool baseline")
	}
}

func TestCalibrationFailsClosedOnBackendFaults(t *testing.T) {
	tests := []struct {
		name, failPath, fault, want       string
		tool, mismatchUsage, overCapacity bool
	}{
		{name: "input count", failPath: "/v1/chat/completions/input_tokens", want: "backend input-token count failed"},
		{name: "template", failPath: "/apply-template", want: "chat template application failed"},
		{name: "tokenize", failPath: "/tokenize", want: "template tokenization failed"},
		{name: "special-token comparison", fault: "special_comparison", want: "special-token comparison failed"},
		{name: "content-only count", fault: "content_only", want: "content-only tokenization failed"},
		{name: "completion", failPath: "/v1/chat/completions", want: "completion usage failed"},
		{name: "Tool overhead", tool: true, want: "Tool definitions did not increase"},
		{name: "Tool-free baseline", tool: true, fault: "tool_baseline", want: "Tool-free baseline count failed"},
		{name: "usage mismatch", mismatchUsage: true, want: "provider prompt usage disagrees"},
		{name: "capacity overflow", overCapacity: true, want: "preflight admitted input plus output reserve"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			count := 10
			if test.overCapacity {
				count = 3841
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var tokenInput struct {
					Content string `json:"content"`
				}
				_ = json.Unmarshal(body, &tokenInput)
				if r.URL.Path == test.failPath {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if (test.fault == "special_comparison" && r.URL.Path == "/tokenize" && bytes.Contains(body, []byte(`"add_special":false`))) ||
					(test.fault == "content_only" && r.URL.Path == "/tokenize" && tokenInput.Content != "<bos>test<eos>") ||
					(test.fault == "tool_baseline" && r.URL.Path == "/v1/chat/completions/input_tokens" && !bytes.Contains(body, []byte(`"tools"`))) {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				switch r.URL.Path {
				case "/v1/chat/completions/input_tokens":
					_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": count})
				case "/apply-template":
					_ = json.NewEncoder(w).Encode(map[string]any{"prompt": "<bos>test<eos>"})
				case "/tokenize":
					_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, count)})
				case "/v1/chat/completions":
					usage := count
					if test.mismatchUsage {
						usage++
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"choices": []any{map[string]any{"message": map[string]any{"content": "OK"}}},
						"usage":   map[string]any{"prompt_tokens": usage},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			options := testOptions(server.URL + "/v1")
			config := contextassembly.DefaultConfig()
			config.ContextWindowTokens, config.OutputReserveTokens, config.SafetyMarginTokens = 4096, 256, 256
			item := corpus(3584)[0]
			item.withTool = test.tool
			backend := nativeBackend{baseURL: server.URL, client: server.Client()}
			sample := calibrateSample(t.Context(), options, backend, backendProps{BOSToken: "<bos>", EOSToken: "<eos>"}, config, item)
			if sample.Status != "failed" || !strings.Contains(sample.Failure, test.want) {
				t.Fatalf("fault was not reported: %#v", sample)
			}
		})
	}
}
