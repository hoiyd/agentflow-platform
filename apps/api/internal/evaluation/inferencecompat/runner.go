package inferencecompat

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/inference/routing"
)

const reportFormat = "agentflow-llama-cpp-compatibility-v1"

type Target struct {
	BackendVersion    string               `json:"backend_version"`
	BaseURL           string               `json:"base_url"`
	Model             string               `json:"model"`
	ModelArtifact     string               `json:"model_artifact"`
	Quantization      string               `json:"quantization"`
	ContextWindow     int                  `json:"context_window_tokens"`
	MaxOutputTokens   int                  `json:"max_output_tokens"`
	Hardware          string               `json:"hardware"`
	Capabilities      routing.Capabilities `json:"capabilities"`
	CancellationProof string               `json:"cancellation_proof"`
}

type Options struct {
	Target           Target
	APIKey           string
	RouteID          string
	CredentialEnv    string
	RequestTimeout   time.Duration
	AgentFlowBaseURL string
	WorkspaceID      string
	AgentID          string
	Revision         string
	HTTPClient       *http.Client
}

type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail,omitempty"`
}

type ModeEvidence struct {
	Mode          string           `json:"mode"`
	Status        string           `json:"status"`
	RunID         string           `json:"run_id,omitempty"`
	TerminalState domain.RunStatus `json:"terminal_state,omitempty"`
	SelectedRoute string           `json:"selected_route,omitempty"`
	TotalTokens   int              `json:"total_tokens,omitempty"`
	Detail        string           `json:"detail,omitempty"`
}

type Report struct {
	SchemaVersion string              `json:"schema_version"`
	Identity      evalreport.Identity `json:"identity"`
	Target        Target              `json:"target"`
	Checks        []Check             `json:"checks"`
	Modes         []ModeEvidence      `json:"modes"`
	Gate          evalreport.Gate     `json:"gate"`
}

func Run(ctx context.Context, options Options) (Report, error) {
	if err := validateOptions(&options); err != nil {
		return Report{}, err
	}
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		apiKey = "local"
	}
	modelClient := openai.NewClientWithTimeout(apiKey, options.Target.BaseURL, options.Target.Model, options.RequestTimeout)
	modelClient.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	options.Target.BaseURL = modelClient.RuntimeIdentity().BaseURL
	started := time.Now().UTC()
	report := Report{
		SchemaVersion: reportFormat,
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "llama_cpp_compatibility", DatasetID: options.Target.ModelArtifact,
			DatasetVersion: options.Target.BackendVersion, DatasetHash: targetHash(options.Target), GitRevision: options.Revision, StartedAt: started},
		Target: options.Target,
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	runCheck := func(name string, fn func(context.Context) error) {
		begin := time.Now()
		checkCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
		err := fn(checkCtx)
		cancel()
		item := Check{Name: name, Status: "passed", DurationMS: time.Since(begin).Milliseconds()}
		if err != nil {
			item.Status, item.Detail = "failed", err.Error()
		}
		report.Checks = append(report.Checks, item)
	}
	runCheck("route_contract", func(context.Context) error { return checkRouteContract(modelClient, options) })
	runCheck("completion_usage", func(checkCtx context.Context) error { return checkCompletionUsage(checkCtx, modelClient) })
	runCheck("streaming_usage", func(checkCtx context.Context) error { return checkStreaming(checkCtx, client, options, apiKey) })
	runCheck("context_length_error", func(checkCtx context.Context) error {
		return checkContextLimit(checkCtx, client, options, apiKey)
	})
	runCheck("request_timeout", func(checkCtx context.Context) error { return checkTimeout(checkCtx, client, options, apiKey) })
	runCheck("cancel_permit_wait_and_probe", func(checkCtx context.Context) error { return checkPermitCancellation(checkCtx, modelClient) })
	runCheck("cancel_before_first_token_and_probe", func(checkCtx context.Context) error {
		return checkBeforeFirstTokenCancellation(checkCtx, client, options, apiKey)
	})
	runCheck("cancel_mid_generation_and_probe", func(checkCtx context.Context) error {
		return checkMidGenerationCancellation(checkCtx, client, options, apiKey)
	})
	if options.Target.Capabilities.ToolCalling {
		runCheck("tool_calling", func(checkCtx context.Context) error { return checkToolCalling(checkCtx, client, options, apiKey) })
	} else {
		report.Checks = append(report.Checks, Check{Name: "tool_calling", Status: "skipped", Detail: "route capability explicitly false"})
	}
	if options.Target.Capabilities.StructuredOutput {
		runCheck("structured_output", func(checkCtx context.Context) error { return checkStructuredOutput(checkCtx, client, options, apiKey) })
	} else {
		report.Checks = append(report.Checks, Check{Name: "structured_output", Status: "skipped", Detail: "route capability explicitly false"})
	}
	if options.AgentFlowBaseURL != "" {
		for _, mode := range []string{"single", "multi_agent", "autonomous"} {
			modeCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
			report.Modes = append(report.Modes, checkMode(modeCtx, client, options, mode))
			cancel()
		}
		runCheck("run_cancel_terminal_and_probe", func(checkCtx context.Context) error {
			return checkRunCancellation(checkCtx, client, options, apiKey)
		})
	}
	report.Target.CancellationProof = "unverified"
	if cancellationChecksPassed(report.Checks) {
		report.Target.CancellationProof = "transport_and_capacity_probe"
		for _, check := range report.Checks {
			if check.Name == "run_cancel_terminal_and_probe" && check.Status == "passed" {
				report.Target.CancellationProof = "transport_run_terminal_and_capacity_probe"
			}
		}
	}
	report.Identity.CompletedAt = time.Now().UTC()
	report.Gate = gate(report)
	return report, nil
}

func validateOptions(options *Options) error {
	options.Target.BaseURL = strings.TrimRight(strings.TrimSpace(options.Target.BaseURL), "/")
	options.AgentFlowBaseURL = strings.TrimRight(strings.TrimSpace(options.AgentFlowBaseURL), "/")
	options.RouteID = strings.TrimSpace(options.RouteID)
	if options.RouteID == "" {
		options.RouteID = "llama-cpp"
	}
	if options.CredentialEnv == "" {
		options.CredentialEnv = "LLAMA_CPP_API_KEY"
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = 30 * time.Second
	}
	missing := make([]string, 0, 6)
	for _, field := range []struct{ name, value string }{{"base URL", options.Target.BaseURL}, {"model", options.Target.Model},
		{"backend version", options.Target.BackendVersion}, {"model artifact", options.Target.ModelArtifact},
		{"quantization", options.Target.Quantization}, {"hardware", options.Target.Hardware}} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 || options.Target.ContextWindow <= 0 || options.Target.MaxOutputTokens <= 0 {
		return fmt.Errorf("fixed target identity is incomplete: missing=%s context_window=%d max_output=%d", strings.Join(missing, ","), options.Target.ContextWindow, options.Target.MaxOutputTokens)
	}
	if options.AgentFlowBaseURL != "" && (strings.TrimSpace(options.WorkspaceID) == "" || strings.TrimSpace(options.AgentID) == "") {
		return errors.New("AgentFlow mode evidence requires --workspace-id and --agent-id")
	}
	for name, value := range map[string]string{"llama.cpp base URL": options.Target.BaseURL, "AgentFlow base URL": options.AgentFlowBaseURL} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials, query or fragment", name)
		}
	}
	return nil
}

func cancellationChecksPassed(checks []Check) bool {
	count := 0
	for _, check := range checks {
		if strings.HasPrefix(check.Name, "cancel_") {
			if check.Status != "passed" {
				return false
			}
			count++
		}
	}
	return count == 3
}

func targetHash(target Target) string {
	target.CancellationProof = ""
	encoded, _ := json.Marshal(target)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func checkRouteContract(client *openai.Client, options Options) error {
	identity := client.RuntimeIdentity()
	descriptor := routing.Descriptor{ID: options.RouteID, Provider: identity.Provider, Model: options.Target.Model, Endpoint: identity.BaseURL,
		Capabilities: options.Target.Capabilities, ContextWindowTokens: options.Target.ContextWindow, MaxOutputTokens: options.Target.MaxOutputTokens,
		Priority: 100, CredentialEnvironment: options.CredentialEnv, Pricing: routing.Pricing{Source: "local_compatibility_evidence"}}
	catalog, err := routing.NewCatalog(routing.Binding{Descriptor: descriptor, Client: client})
	if err != nil {
		return err
	}
	_, err = catalog.SelectRoute(options.RouteID, routing.Requirements{Purpose: "compatibility_evidence", Streaming: true, ToolCalling: options.Target.Capabilities.ToolCalling,
		StructuredOutput: options.Target.Capabilities.StructuredOutput, EstimatedInputTokens: 16, MaxOutputTokens: min(64, options.Target.MaxOutputTokens)})
	if err != nil {
		return err
	}
	if !options.Target.Capabilities.ToolCalling {
		_, err = catalog.SelectRoute(options.RouteID, routing.Requirements{Purpose: "unsupported_capability_check", ToolCalling: true, EstimatedInputTokens: 16, MaxOutputTokens: 16})
		if !errors.Is(err, routing.ErrNoCompatibleRoute) {
			return errors.New("Tool Calling was not rejected for a route that declares it unsupported")
		}
	}
	if !options.Target.Capabilities.StructuredOutput {
		_, err = catalog.SelectRoute(options.RouteID, routing.Requirements{Purpose: "unsupported_capability_check", StructuredOutput: true, EstimatedInputTokens: 16, MaxOutputTokens: 16})
		if !errors.Is(err, routing.ErrNoCompatibleRoute) {
			return errors.New("structured output was not rejected for a route that declares it unsupported")
		}
	}
	return nil
}

func checkCompletionUsage(ctx context.Context, client *openai.Client) error {
	result, err := client.CompleteTextDetailed(ctx, "Reply briefly.", "Reply with exactly: compatibility ok")
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Text) == "" || !result.Usage.Valid() {
		return errors.New("completion or usage is empty")
	}
	if result.Usage.Estimated {
		return errors.New("backend omitted exact usage; adapter used an estimate")
	}
	return nil
}

func checkStreaming(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Count from one to three."}},
		"stream": true, "stream_options": map[string]bool{"include_usage": true}, "max_tokens": min(32, options.Target.MaxOutputTokens),
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return responseError(response)
	}
	deltas, usage, done, err := readStream(response.Body, nil)
	if err != nil {
		return err
	}
	if deltas == 0 || !done || !usage.Valid() {
		return fmt.Errorf("incomplete stream evidence: deltas=%d done=%t usage=%d", deltas, done, usage.TotalTokens)
	}
	return nil
}

func checkContextLimit(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": strings.Repeat("boundary ", options.Target.ContextWindow+256)}},
		"stream": false, "max_tokens": 16,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 == 2 {
		return errors.New("backend accepted input beyond the declared context window")
	}
	var body struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body); err != nil {
		return fmt.Errorf("backend context error is not JSON: %w", err)
	}
	if body.Error.Code != "context_length_exceeded" && !strings.Contains(strings.ToLower(body.Error.Message), "context") {
		return fmt.Errorf("expected context length error, got status=%d code=%v", response.StatusCode, body.Error.Code)
	}
	return nil
}

func checkTimeout(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	written := make(chan struct{}, 1)
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { written <- struct{}{} }}
	timeoutCtx, cancel := context.WithTimeout(httptrace.WithClientTrace(ctx, trace), 50*time.Millisecond)
	defer cancel()
	response, err := chatRequest(timeoutCtx, client, options.Target.BaseURL, apiKey, basicPayload(options, 128))
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode/100 != 2 {
			return responseError(response)
		}
		_, _, done, readErr := readStream(response.Body, nil)
		if done {
			return errors.New("stream completed before the timeout")
		}
		err = readErr
	}
	if !errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("expected request timeout while waiting for the stream, got %v", err)
	}
	select {
	case <-written:
	default:
		return errors.New("timeout elapsed before the request was written")
	}
	return probe(ctx, client, options, apiKey)
}

type blockingLimiter struct{ calls atomic.Int32 }

func (l *blockingLimiter) AcquireRequest(ctx context.Context, _ string, _ int) (func(), error) {
	if l.calls.Add(1) == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return func() {}, nil
}

var _ requestcontrol.Limiter = (*blockingLimiter)(nil)

func checkPermitCancellation(ctx context.Context, client *openai.Client) error {
	limiter := &blockingLimiter{}
	client.SetRequestLimiter(limiter)
	defer client.SetRequestLimiter(nil)
	cancelCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	_, err := client.CompleteTextDetailed(cancelCtx, "Reply briefly.", "permit cancellation")
	modelErr, ok := openai.AsModelError(err)
	if !ok || modelErr.Kind != openai.ErrorCanceled {
		return fmt.Errorf("expected bounded permit cancellation, got %v", err)
	}
	_, err = client.CompleteTextDetailed(ctx, "Reply briefly.", "Reply with exactly: probe ok")
	return err
}

func checkBeforeFirstTokenCancellation(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	written := make(chan struct{}, 1)
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { written <- struct{}{} }}
	cancelCtx = httptrace.WithClientTrace(cancelCtx, trace)
	result := make(chan error, 1)
	go func() {
		response, err := chatRequest(cancelCtx, client, options.Target.BaseURL, apiKey, basicPayload(options, options.Target.MaxOutputTokens))
		if response != nil {
			response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-written:
		cancel()
	case <-time.After(options.RequestTimeout):
		cancel()
		return errors.New("request was not written before cancellation deadline")
	}
	select {
	case err := <-result:
		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "canceled")) {
			return fmt.Errorf("expected pre-token cancellation, got %v", err)
		}
	case <-time.After(options.RequestTimeout):
		return errors.New("pre-token cancellation did not finish within the request timeout")
	}
	return probe(ctx, client, options, apiKey)
}

func checkMidGenerationCancellation(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	response, err := chatRequest(cancelCtx, client, options.Target.BaseURL, apiKey, basicPayload(options, options.Target.MaxOutputTokens))
	if err != nil {
		cancel()
		return err
	}
	if response.StatusCode/100 != 2 {
		cancel()
		defer response.Body.Close()
		return responseError(response)
	}
	seen := make(chan struct{}, 1)
	type outcome struct {
		done bool
		err  error
	}
	result := make(chan outcome, 1)
	go func() {
		_, _, done, readErr := readStream(response.Body, func() { seen <- struct{}{}; cancel() })
		response.Body.Close()
		result <- outcome{done: done, err: readErr}
	}()
	select {
	case <-seen:
	case <-time.After(options.RequestTimeout):
		cancel()
		return errors.New("stream produced no token before cancellation deadline")
	}
	select {
	case observed := <-result:
		if observed.done || observed.err == nil {
			return fmt.Errorf("stream completed instead of stopping mid-generation: done=%t err=%v", observed.done, observed.err)
		}
	case <-time.After(options.RequestTimeout):
		return errors.New("mid-generation cancellation did not finish within the request timeout")
	}
	return probe(ctx, client, options, apiKey)
}

func checkToolCalling(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Call get_current_time."}},
		"tools":       []map[string]any{{"type": "function", "function": map[string]any{"name": "get_current_time", "description": "Get time", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "get_current_time"}}, "stream": false,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return responseError(response)
	}
	var payload struct {
		Choices []struct {
			Message provider.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Choices) == 0 || len(payload.Choices[0].Message.ToolCalls) == 0 || payload.Choices[0].Message.ToolCalls[0].Function.Name != "get_current_time" {
		return errors.New("route declares Tool Calling but backend returned no valid tool call")
	}
	return nil
}

func checkStructuredOutput(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	response, err := chatRequest(ctx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Finish."}},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "decision", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{
				"done": map[string]any{"type": "boolean"}}, "required": []string{"done"}, "additionalProperties": false},
		}}, "max_tokens": 64,
	})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return responseError(response)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Choices) == 0 {
		return errors.New("route declares structured output but backend returned no choices")
	}
	var decision struct {
		Done *bool `json:"done"`
	}
	if json.Unmarshal([]byte(payload.Choices[0].Message.Content), &decision) != nil || decision.Done == nil {
		return errors.New("route declares structured output but backend returned invalid schema content")
	}
	return nil
}

func probe(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	probeCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
	defer cancel()
	response, err := chatRequest(probeCtx, client, options.Target.BaseURL, apiKey, map[string]any{
		"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "stream": false, "max_tokens": 8,
	})
	if err != nil {
		return fmt.Errorf("capacity recovery probe failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("capacity recovery probe: %w", responseError(response))
	}
	var completion struct {
		Choices []struct {
			Message provider.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil || len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return errors.New("capacity recovery probe returned no completion")
	}
	return nil
}

func basicPayload(options Options, maxTokens int) map[string]any {
	return map[string]any{"model": options.Target.Model, "messages": []map[string]string{{"role": "user", "content": "Write a long numbered list."}},
		"stream": true, "stream_options": map[string]bool{"include_usage": true}, "max_tokens": maxTokens}
}

func chatRequest(ctx context.Context, client *http.Client, baseURL, apiKey string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Close = true
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return client.Do(request)
}

func readStream(body io.Reader, onFirstDelta func()) (int, provider.Usage, bool, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	deltas := 0
	var usage provider.Usage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return deltas, usage, true, nil
		}
		var chunk struct {
			Choices []struct {
				Delta provider.Message `json:"delta"`
			} `json:"choices"`
			Usage *provider.Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return deltas, usage, false, err
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				deltas++
				if deltas == 1 && onFirstDelta != nil {
					onFirstDelta()
				}
			}
		}
	}
	return deltas, usage, false, scanner.Err()
}

func responseError(response *http.Response) error {
	return fmt.Errorf("status=%d", response.StatusCode)
}

func gate(report Report) evalreport.Gate {
	result := evalreport.Gate{Passed: true}
	if len(report.Modes) != 3 {
		result.Passed = false
		result.BlockingFailures++
		result.Reasons = append(result.Reasons, "AgentFlow Single/Multi/Loop evidence is incomplete")
	}
	for _, check := range report.Checks {
		if check.Status == "failed" {
			result.Passed = false
			result.BlockingFailures++
			result.Reasons = append(result.Reasons, check.Name+": "+check.Detail)
		}
	}
	for _, mode := range report.Modes {
		if mode.Status != "passed" {
			result.Passed = false
			result.BlockingFailures++
			result.Reasons = append(result.Reasons, mode.Mode+": "+mode.Detail)
		}
	}
	result.BlockingSamples = result.BlockingFailures
	return result
}

func checkMode(ctx context.Context, client *http.Client, options Options, mode string) ModeEvidence {
	evidence := ModeEvidence{Mode: mode, Status: "failed"}
	runID, terminal, err := startMode(ctx, client, options, mode)
	if err != nil {
		evidence.Detail = err.Error()
		return evidence
	}
	if mode == "multi_agent" && terminal == domain.RunWaitingForUser {
		terminal, err = continueMulti(ctx, client, options, runID)
		if err != nil {
			evidence.Detail = err.Error()
			return evidence
		}
	}
	replay, err := fetchReplay(ctx, client, options, runID)
	if err != nil {
		evidence.Detail = err.Error()
		return evidence
	}
	evidence.RunID, evidence.TerminalState, evidence.TotalTokens = runID, replay.Run.Status, replay.UsageLedger.Totals.TotalTokens
	for _, event := range replay.RunEvents {
		if event.Type == domain.EventModelRouteDecided && event.Payload["outcome"] == "selected" {
			evidence.SelectedRoute, _ = event.Payload["selected_route_id"].(string)
			break
		}
	}
	if terminal != domain.RunCompleted || replay.Run.Status != domain.RunCompleted || evidence.SelectedRoute != options.RouteID || evidence.TotalTokens <= 0 {
		evidence.Detail = fmt.Sprintf("terminal=%s replay=%s route=%q tokens=%d", terminal, replay.Run.Status, evidence.SelectedRoute, evidence.TotalTokens)
		return evidence
	}
	if replay.RuntimeSnapshot == nil || !snapshotContainsRoute(replay.RuntimeSnapshot, options) {
		evidence.Detail = "Replay did not retain the expected route identity"
		return evidence
	}
	evidence.Status = "passed"
	return evidence
}

func checkRunCancellation(ctx context.Context, client *http.Client, options Options, apiKey string) error {
	payload, _ := json.Marshal(map[string]any{
		"workspace_id": options.WorkspaceID, "agent_id": options.AgentID, "mode": "autonomous",
		"message": "Write a detailed compatibility report with evidence and limitations.",
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, options.AgentFlowBaseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-ID", options.WorkspaceID)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	if response.StatusCode/100 != 2 {
		defer response.Body.Close()
		return responseError(response)
	}
	var runID string
	var runStarted bool
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event domain.RunEvent
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		if event.RunID != "" {
			runID = event.RunID
		}
		if event.Type == domain.EventRunStarted && runID != "" {
			runStarted = true
			break
		}
	}
	readErr := scanner.Err()
	_ = response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if runID == "" || !runStarted {
		return errors.New("AgentFlow stream omitted run.started before cancellation")
	}
	modelActive := false
	for ctx.Err() == nil {
		replay, err := fetchReplay(ctx, client, options, runID)
		if err != nil {
			return err
		}
		modelActive = false
		for _, event := range replay.RunEvents {
			switch event.Type {
			case domain.EventModelStarted:
				modelActive = true
			case domain.EventModelCompleted, domain.EventModelFailed:
				modelActive = false
			}
		}
		if modelActive {
			break
		}
		if replay.Run.Status != domain.RunRunning {
			return fmt.Errorf("Run reached %s before a model request could be canceled", replay.Run.Status)
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !modelActive {
		return fmt.Errorf("model request did not start before cancellation deadline: %w", ctx.Err())
	}
	cancelRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, options.AgentFlowBaseURL+"/api/runs/"+url.PathEscape(runID)+"/cancel", nil)
	if err != nil {
		return err
	}
	cancelRequest.Header.Set("X-Workspace-ID", options.WorkspaceID)
	cancelResponse, err := client.Do(cancelRequest)
	if err != nil {
		return err
	}
	_ = cancelResponse.Body.Close()
	if cancelResponse.StatusCode/100 != 2 {
		return responseError(cancelResponse)
	}
	var awaitingEvents bool
	for ctx.Err() == nil {
		replay, err := fetchReplay(ctx, client, options, runID)
		if err != nil {
			return err
		}
		if replay.Run.Status == domain.RunCanceled {
			var requested, canceled bool
			openStages := map[string]bool{}
			for _, event := range replay.RunEvents {
				requested = requested || event.Type == domain.EventRunCancelRequested
				canceled = canceled || event.Type == domain.EventRunCanceled
				switch event.Type {
				case domain.EventStageStarted:
					openStages[event.StageID] = true
				case domain.EventStageCompleted, domain.EventStageFailed:
					delete(openStages, event.StageID)
				}
			}
			if requested && canceled {
				if len(openStages) != 0 {
					return fmt.Errorf("canceled Run has %d stage.started events without terminal events", len(openStages))
				}
				return probe(ctx, client, options, apiKey)
			}
			awaitingEvents = true
		}
		if replay.Run.Status == domain.RunCompleted || replay.Run.Status == domain.RunFailed {
			return fmt.Errorf("Run ended as %s instead of canceled", replay.Run.Status)
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	if awaitingEvents {
		return errors.New("canceled Run Replay omitted cancellation lifecycle events")
	}
	return ctx.Err()
}

func startMode(ctx context.Context, client *http.Client, options Options, mode string) (string, domain.RunStatus, error) {
	message := "Reply briefly with compatibility evidence."
	if mode == "multi_agent" {
		message = "Research the compatibility evidence, compare sources, and identify evidence gaps."
	} else if mode == "autonomous" {
		message = "The local model returned a response and token counts. Summarize those two observed facts in one sentence. All facts required are in this request."
	}
	payload := map[string]any{"workspace_id": options.WorkspaceID, "agent_id": options.AgentID, "message": message, "mode": mode}
	return runSSERequest(ctx, client, options, http.MethodPost, "/api/chat", payload)
}

func continueMulti(ctx context.Context, client *http.Client, options Options, runID string) (domain.RunStatus, error) {
	_, status, err := runSSERequest(ctx, client, options, http.MethodPost, "/api/runs/"+url.PathEscape(runID)+"/continue", map[string]any{
		"plan":                 "Produce a brief compatibility response.",
		"routing_requirements": map[string]any{"prohibited_tools": []string{"calculator", "get_current_time"}},
	})
	return status, err
}

func runSSERequest(ctx context.Context, client *http.Client, options Options, method, path string, payload any) (string, domain.RunStatus, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, method, options.AgentFlowBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-ID", options.WorkspaceID)
	response, err := client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return "", "", responseError(response)
	}
	var runID string
	var status domain.RunStatus
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var chunk domain.ChatChunk
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &chunk) == nil {
			if chunk.RunID != "" {
				runID = chunk.RunID
			}
			if chunk.Status != "" {
				status = domain.RunStatus(chunk.Status)
			}
			if chunk.Type == "error" {
				return runID, status, fmt.Errorf("AgentFlow run failed: %s", chunk.Error)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return runID, status, err
	}
	if runID == "" || status == "" {
		return runID, status, errors.New("AgentFlow stream omitted run terminal evidence")
	}
	return runID, status, nil
}

func fetchReplay(ctx context.Context, client *http.Client, options Options, runID string) (domain.RunReplay, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, options.AgentFlowBaseURL+"/api/runs/"+url.PathEscape(runID)+"/replay", nil)
	if err != nil {
		return domain.RunReplay{}, err
	}
	request.Header.Set("X-Workspace-ID", options.WorkspaceID)
	response, err := client.Do(request)
	if err != nil {
		return domain.RunReplay{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return domain.RunReplay{}, responseError(response)
	}
	var replay domain.RunReplay
	err = json.NewDecoder(response.Body).Decode(&replay)
	return replay, err
}

func snapshotContainsRoute(snapshot *domain.RuntimeSnapshot, options Options) bool {
	for _, route := range snapshot.ModelRouting.Routes {
		if route.ID == options.RouteID && route.Model == options.Target.Model && route.Endpoint == options.Target.BaseURL {
			return true
		}
	}
	return false
}
