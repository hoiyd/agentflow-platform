package tokenization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

const reportFormat = "agentflow-tokenization-calibration-v1"

var errCaptured = errors.New("calibration request captured")

type Options struct {
	BaseURL             string
	Model               string
	ModelArtifact       string
	GGUFSHA256          string
	ContextWindowTokens int
	OutputReserveTokens int
	SafetyMarginTokens  int
	APIKey              string
	RequestTimeout      time.Duration
	Revision            string
	HTTPClient          *http.Client
}

type Target struct {
	BackendBuild        string `json:"backend_build"`
	Model               string `json:"model"`
	ModelArtifact       string `json:"model_artifact"`
	GGUFSHA256          string `json:"gguf_sha256"`
	ChatTemplateSHA256  string `json:"chat_template_sha256"`
	BOSToken            string `json:"bos_token"`
	EOSToken            string `json:"eos_token"`
	ContextWindowTokens int    `json:"context_window_tokens"`
	OutputReserveTokens int    `json:"output_reserve_tokens"`
	SafetyMarginTokens  int    `json:"safety_margin_tokens"`
	CorpusVersion       string `json:"corpus_version"`
}

type Sample struct {
	ID                    string         `json:"id"`
	Status                string         `json:"status"`
	RequestSHA256         string         `json:"request_sha256,omitempty"`
	SourceTokenBreakdown  map[string]int `json:"source_token_breakdown,omitempty"`
	EstimatedInputTokens  int            `json:"estimated_input_tokens"`
	BackendInputTokens    int            `json:"backend_input_tokens,omitempty"`
	TemplateTokens        int            `json:"template_tokens,omitempty"`
	ContentOnlyTokens     int            `json:"content_only_tokens,omitempty"`
	SpecialInsertionDelta int            `json:"special_insertion_delta"`
	BOSMarkerInPrompt     bool           `json:"bos_marker_in_prompt"`
	EOSMarkersInPrompt    int            `json:"eos_markers_in_prompt"`
	ToolTemplateOverhead  int            `json:"tool_template_overhead_tokens,omitempty"`
	ProviderPromptTokens  *int           `json:"provider_prompt_tokens,omitempty"`
	UsageEstimated        bool           `json:"usage_estimated"`
	UsageStatus           string         `json:"usage_status"`
	AbsoluteErrorTokens   int            `json:"absolute_error_tokens,omitempty"`
	RelativeErrorPercent  float64        `json:"relative_error_percent,omitempty"`
	InputBudgetTokens     int            `json:"input_budget_tokens"`
	FitsContextWindow     bool           `json:"fits_context_window"`
	PreflightRejected     bool           `json:"preflight_rejected,omitempty"`
	CounterfactualTokens  int            `json:"counterfactual_backend_input_tokens,omitempty"`
	WouldFitIfAdmitted    bool           `json:"would_fit_if_admitted,omitempty"`
	Failure               string         `json:"failure,omitempty"`
}

type Summary struct {
	WorstAbsoluteSample  string  `json:"worst_absolute_sample"`
	WorstAbsoluteTokens  int     `json:"worst_absolute_tokens"`
	WorstRelativeSample  string  `json:"worst_relative_sample"`
	WorstRelativePercent float64 `json:"worst_relative_percent"`
	MaxUnderestimate     int     `json:"max_underestimate_tokens"`
	FalseRejections      int     `json:"counterfactual_false_rejections"`
}

type Report struct {
	SchemaVersion string              `json:"schema_version"`
	Identity      evalreport.Identity `json:"identity"`
	Target        Target              `json:"target"`
	Samples       []Sample            `json:"samples"`
	Summary       Summary             `json:"summary"`
	Gate          evalreport.Gate     `json:"gate"`
}

type captureRecorder struct{ observation requestcontrol.Observation }

func (r *captureRecorder) Record(_ context.Context, observation requestcontrol.Observation) error {
	r.observation = observation
	return errCaptured
}

func Run(ctx context.Context, options Options) (Report, error) {
	if err := validate(options); err != nil {
		return Report{}, err
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = time.Minute
	}
	if options.APIKey == "" {
		options.APIKey = "local"
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: options.RequestTimeout}
	}
	backend := nativeBackend{baseURL: strings.TrimSuffix(strings.TrimRight(options.BaseURL, "/"), "/v1"), apiKey: options.APIKey, client: options.HTTPClient}
	props, err := backend.props(ctx)
	if err != nil {
		return Report{}, err
	}
	if props.BuildInfo == "" || props.ChatTemplate == "" {
		return Report{}, errors.New("llama.cpp props omitted build or chat template identity")
	}
	started := time.Now().UTC()
	target := Target{
		BackendBuild: props.BuildInfo, Model: options.Model, ModelArtifact: options.ModelArtifact,
		GGUFSHA256: strings.ToLower(options.GGUFSHA256), ChatTemplateSHA256: digest([]byte(props.ChatTemplate)),
		BOSToken: props.BOSToken, EOSToken: props.EOSToken,
		ContextWindowTokens: options.ContextWindowTokens, OutputReserveTokens: options.OutputReserveTokens,
		SafetyMarginTokens: options.SafetyMarginTokens, CorpusVersion: corpusVersion,
	}
	identityBytes, _ := json.Marshal(target)
	report := Report{
		SchemaVersion: reportFormat, Target: target,
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "tokenization_calibration",
			DatasetID: "agentflow-tokenization", DatasetVersion: corpusVersion, DatasetHash: digest(identityBytes),
			GitRevision: options.Revision, StartedAt: started},
	}
	config := contextassembly.DefaultConfig()
	config.ContextWindowTokens = options.ContextWindowTokens
	config.OutputReserveTokens = options.OutputReserveTokens
	config.SafetyMarginTokens = options.SafetyMarginTokens
	config.CompactionMode = contextassembly.CompactionModeOff
	inputBudget := options.ContextWindowTokens - options.OutputReserveTokens - options.SafetyMarginTokens
	for _, item := range corpus(inputBudget) {
		sampleCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
		sample := calibrateSample(sampleCtx, options, backend, props, config, item)
		cancel()
		report.Samples = append(report.Samples, sample)
		if sample.Status == "failed" {
			report.Gate.Reasons = append(report.Gate.Reasons, item.id+": "+sample.Failure)
			continue
		}
		if item.rejectPreflight {
			if sample.WouldFitIfAdmitted {
				report.Summary.FalseRejections++
			}
			continue
		}
		if sample.AbsoluteErrorTokens > report.Summary.WorstAbsoluteTokens {
			report.Summary.WorstAbsoluteTokens, report.Summary.WorstAbsoluteSample = sample.AbsoluteErrorTokens, item.id
		}
		if sample.RelativeErrorPercent > report.Summary.WorstRelativePercent {
			report.Summary.WorstRelativePercent, report.Summary.WorstRelativeSample = sample.RelativeErrorPercent, item.id
		}
		report.Summary.MaxUnderestimate = max(report.Summary.MaxUnderestimate, sample.BackendInputTokens-sample.EstimatedInputTokens)
	}
	report.Gate.BlockingFailures = len(report.Gate.Reasons)
	report.Gate.BlockingSamples = report.Gate.BlockingFailures
	report.Gate.Passed = report.Gate.BlockingFailures == 0
	report.Identity.CompletedAt = time.Now().UTC()
	return report, nil
}

func calibrateSample(ctx context.Context, options Options, backend nativeBackend, props backendProps, config domain.ContextAssemblyConfig, item corpusCase) Sample {
	budget := options.ContextWindowTokens - options.OutputReserveTokens - options.SafetyMarginTokens
	sample := Sample{ID: item.id, Status: "failed", InputBudgetTokens: budget, UsageStatus: "not_requested"}
	observation, err := captureRequest(ctx, options, config, item)
	if item.rejectPreflight {
		var budgetErr *contextassembly.InputBudgetError
		if errors.As(err, &budgetErr) && len(observation.Payload) == 0 {
			sample.EstimatedInputTokens = budgetErr.RequiredTokens
			relaxed := config
			relaxed.ContextWindowTokens = max(config.ContextWindowTokens*4,
				budgetErr.RequiredTokens+config.OutputReserveTokens+config.SafetyMarginTokens+1)
			counterfactual, captureErr := captureRequest(ctx, options, relaxed, item)
			if captureErr != nil {
				sample.Failure = "counterfactual request reconstruction failed: " + captureErr.Error()
				return sample
			}
			sample.CounterfactualTokens, captureErr = backend.inputTokens(ctx, counterfactual.Payload)
			if captureErr != nil {
				sample.Failure = "counterfactual backend count failed: " + captureErr.Error()
				return sample
			}
			sample.WouldFitIfAdmitted = sample.CounterfactualTokens+options.OutputReserveTokens <= options.ContextWindowTokens
			sample.Status, sample.PreflightRejected = "passed", true
			return sample
		}
		sample.Failure = "over-budget input was not rejected before transport"
		return sample
	}
	if err != nil {
		sample.Failure = fmt.Sprintf("capture request: %v", err)
		return sample
	}
	sample.RequestSHA256 = digest(observation.Payload)
	sample.SourceTokenBreakdown = observation.SourceTokenBreakdown
	for _, tokens := range observation.SourceTokenBreakdown {
		sample.EstimatedInputTokens += tokens
	}
	if sample.EstimatedInputTokens <= 0 || sample.EstimatedInputTokens > budget {
		sample.Failure = "Context Assembly estimate is empty or exceeds its input budget"
		return sample
	}
	if sample.BackendInputTokens, err = backend.inputTokens(ctx, observation.Payload); err != nil {
		sample.Failure = "backend input-token count failed: " + err.Error()
		return sample
	}
	prompt, err := backend.applyTemplate(ctx, observation.Payload)
	if err != nil {
		sample.Failure = "chat template application failed: " + err.Error()
		return sample
	}
	withSpecial, err := backend.tokenize(ctx, prompt, true)
	if err != nil {
		sample.Failure = "template tokenization failed: " + err.Error()
		return sample
	}
	withoutSpecial, err := backend.tokenize(ctx, prompt, false)
	if err != nil {
		sample.Failure = "special-token comparison failed: " + err.Error()
		return sample
	}
	sample.TemplateTokens, sample.SpecialInsertionDelta = withSpecial, withSpecial-withoutSpecial
	sample.BOSMarkerInPrompt = props.BOSToken != "" && strings.Contains(prompt, props.BOSToken)
	if props.EOSToken != "" {
		sample.EOSMarkersInPrompt = strings.Count(prompt, props.EOSToken)
	}
	content, err := requestContent(observation.Payload)
	if err != nil {
		sample.Failure = "captured request has invalid messages"
		return sample
	}
	sample.ContentOnlyTokens, err = backend.tokenize(ctx, content, false)
	if err != nil {
		sample.Failure = "content-only tokenization failed: " + err.Error()
		return sample
	}
	if item.withTool {
		withoutTools, err := removeTools(observation.Payload)
		if err != nil {
			sample.Failure = "captured Tool definition is invalid"
			return sample
		}
		baseline, err := backend.inputTokens(ctx, withoutTools)
		if err != nil {
			sample.Failure = "Tool-free baseline count failed: " + err.Error()
			return sample
		}
		sample.ToolTemplateOverhead = sample.BackendInputTokens - baseline
		if sample.ToolTemplateOverhead <= 0 {
			sample.Failure = "Tool definitions did not increase the backend input-token count"
			return sample
		}
	}
	usage, err := backend.completionUsage(ctx, observation.Payload)
	if err != nil {
		sample.Failure = "completion usage failed: " + err.Error()
		return sample
	}
	if usage == nil {
		sample.UsageStatus, sample.UsageEstimated = "missing; estimate only", true
		sample.Failure = "provider omitted exact prompt-token usage"
		return sample
	}
	sample.ProviderPromptTokens, sample.UsageStatus = usage, "provider_exact"
	sample.FitsContextWindow = sample.BackendInputTokens+options.OutputReserveTokens <= options.ContextWindowTokens
	sample.AbsoluteErrorTokens = absInt(sample.EstimatedInputTokens - sample.BackendInputTokens)
	sample.RelativeErrorPercent = 100 * float64(sample.AbsoluteErrorTokens) / float64(sample.BackendInputTokens)
	switch {
	case sample.TemplateTokens != sample.BackendInputTokens:
		sample.Failure = "chat-template tokenization disagrees with backend input-token count"
	case *usage != sample.BackendInputTokens:
		sample.Failure = "provider prompt usage disagrees with backend input-token count"
	case !sample.FitsContextWindow:
		sample.Failure = "preflight admitted input plus output reserve beyond context window"
	default:
		sample.Status = "passed"
	}
	return sample
}

func captureRequest(ctx context.Context, options Options, config domain.ContextAssemblyConfig, item corpusCase) (requestcontrol.Observation, error) {
	recorder := &captureRecorder{}
	client := openai.NewClientWithTimeout(options.APIKey, options.BaseURL, options.Model, options.RequestTimeout)
	client.SetRequestRecorder(recorder)
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	ctx = contextassembly.WithSession(ctx, contextassembly.Session{
		Config: config, History: item.history, Knowledge: item.knowledge, CurrentInput: item.user,
	})
	var err error
	if item.withTool {
		catalog, catalogErr := calibrationToolCatalog()
		if catalogErr != nil {
			return requestcontrol.Observation{}, catalogErr
		}
		events, errs := toolloop.Stream(ctx, client, toolloop.Request{
			SystemPrompt: item.system, Latest: item.user, Catalog: catalog,
			Trace: provider.ChatTrace{Recorder: eventpkg.NewRecorder(nil)},
		})
		for range events {
		}
		err = <-errs
	} else {
		_, err = client.CompleteTextDetailed(ctx, item.system, item.user)
	}
	if err == nil && len(recorder.observation.Payload) == 0 {
		return recorder.observation, errors.New("model request was not captured")
	}
	if !errors.Is(err, errCaptured) || len(recorder.observation.Payload) == 0 {
		return recorder.observation, err
	}
	return recorder.observation, nil
}

func validate(options Options) error {
	if !strings.HasSuffix(strings.TrimRight(options.BaseURL, "/"), "/v1") || options.Model == "" || options.ModelArtifact == "" {
		return errors.New("llama.cpp /v1 base URL, model, and pinned model artifact are required")
	}
	decoded, err := hex.DecodeString(options.GGUFSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("a 64-character GGUF SHA-256 is required to pin the tokenizer")
	}
	if options.ContextWindowTokens < 512 || options.OutputReserveTokens <= 0 || options.SafetyMarginTokens <= 0 ||
		options.OutputReserveTokens+options.SafetyMarginTokens >= options.ContextWindowTokens {
		return errors.New("invalid context window, output reserve, or safety margin")
	}
	return nil
}

func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
