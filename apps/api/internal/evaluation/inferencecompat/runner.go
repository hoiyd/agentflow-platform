package inferencecompat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/inference/openai"
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
