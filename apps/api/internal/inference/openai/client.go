package openai

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

const (
	defaultEmbeddingBaseURL = "http://localhost:11434/api/embed"
	defaultEmbeddingModel   = "embeddinggemma"
)

type Client struct {
	apiKey              string
	baseURL             string
	embeddingBaseURL    string
	model               string
	embeddingModel      string
	embeddingDimensions int
	httpClient          *http.Client
	timeout             time.Duration
	requestLimiter      requestcontrol.Limiter
	requestRecorder     requestcontrol.Recorder
	retryPolicy         RetryPolicy
	toolEffectJournal   tool.ToolEffectJournal
	toolArtifactStore   tool.ToolArtifactWriter
	toolArtifactPolicy  ToolArtifactPolicy
	generationPolicy    domain.GenerationPolicy
	seedSupported       bool
}

var _ provider.Client = (*Client)(nil)

type RuntimeIdentity = provider.RuntimeIdentity
type Message = provider.Message
type ToolCall = provider.ToolCall
type FunctionCall = provider.FunctionCall
type StreamEvent = provider.StreamEvent

type streamToolExecutionTracer struct {
	delegate tool.ExecutionTracer
	events   chan<- StreamEvent
}

func (t *streamToolExecutionTracer) ToolStarted(ctx context.Context, request tool.ExecutionRequest) {
	if t.delegate != nil {
		t.delegate.ToolStarted(ctx, request)
	}
	log.Printf("tool_call_start id=%s tool=%s arguments_bytes=%d arguments_hash=%s", request.CallID, request.Tool, len(request.Arguments), request.ArgumentsHash)
	select {
	case <-ctx.Done():
	case t.events <- StreamEvent{Type: "tool_start", ToolName: request.Tool, ToolCallID: request.CallID}:
	}
}

func (t *streamToolExecutionTracer) ToolFinished(ctx context.Context, result tool.ExecutionResult) {
	if t.delegate != nil {
		t.delegate.ToolFinished(ctx, result)
	}
	select {
	case <-ctx.Done():
	case t.events <- StreamEvent{Type: "tool_end", ToolName: result.Tool, ToolCallID: result.CallID, Error: result.ErrorMessage()}:
	}
	status := "tool_end"
	if result.Error != nil {
		status = "tool_error"
	}
	encodedResult, _ := json.Marshal(result.Result)
	log.Printf(
		"tool_call_end id=%s tool=%s status=%s duration_ms=%d arguments_hash=%s result_bytes=%d error=%q",
		result.CallID,
		result.Tool,
		status,
		result.DurationMS,
		result.ArgumentsHash,
		len(encodedResult),
		result.ErrorMessage(),
	)
}

func (t *streamToolExecutionTracer) ToolPolicyEvaluated(ctx context.Context, request tool.ExecutionRequest, decision policy.Decision) error {
	delegate, ok := t.delegate.(tool.PolicyDecisionTracer)
	if !ok {
		return errors.New("durable Tool policy tracer is unavailable")
	}
	return delegate.ToolPolicyEvaluated(ctx, request, decision)
}

func (t *streamToolExecutionTracer) ToolProgressEvaluated(ctx context.Context, request tool.ExecutionRequest, decision progress.Decision) {
	if delegate, ok := t.delegate.(tool.ProgressDecisionTracer); ok {
		delegate.ToolProgressEvaluated(ctx, request, decision)
	}
}

type Usage = provider.Usage
type TextCompletion = provider.TextCompletion
type PreparedText = provider.PreparedText
type Embedding = provider.Embedding

type ToolArtifactPolicy struct {
	MaxBatchResultBytes int
	MaxArtifactBytes    int
	PreviewBytes        int
	Retention           time.Duration
}

func NewClient(apiKey string, baseURL string, model string) *Client {
	return NewClientWithTimeout(apiKey, baseURL, model, 5*time.Minute)
}

func NewClientWithTimeout(apiKey string, baseURL string, model string, timeout time.Duration) *Client {
	return NewClientWithTimeoutAndEmbeddingModel(apiKey, baseURL, defaultEmbeddingBaseURL, model, defaultEmbeddingModel, 1536, timeout)
}

func NewEmbeddingClient(apiKey string, baseURL string, model string, dimensions int, timeout time.Duration) *Client {
	return NewClientWithTimeoutAndEmbeddingModel(apiKey, baseURL, baseURL, "", model, dimensions, timeout)
}

func NewClientWithTimeoutAndEmbeddingModel(apiKey string, baseURL string, embeddingBaseURL string, model string, embeddingModel string, embeddingDimensions int, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	embeddingModel = strings.TrimSpace(embeddingModel)
	if embeddingModel == "" {
		embeddingModel = defaultEmbeddingModel
	}
	if embeddingDimensions <= 0 {
		embeddingDimensions = 1536
	}
	baseURL = normalizeBaseURL(baseURL)
	embeddingBaseURL = strings.TrimSpace(embeddingBaseURL)
	if embeddingBaseURL == "" {
		embeddingBaseURL = defaultEmbeddingBaseURL
	} else {
		embeddingBaseURL = normalizeBaseURL(embeddingBaseURL)
	}
	return &Client{
		apiKey:              strings.TrimSpace(apiKey),
		baseURL:             baseURL,
		embeddingBaseURL:    embeddingBaseURL,
		model:               strings.TrimSpace(model),
		embeddingModel:      embeddingModel,
		embeddingDimensions: embeddingDimensions,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: timeout,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		timeout:          timeout,
		retryPolicy:      DefaultRetryPolicy(),
		generationPolicy: domain.DefaultGenerationPolicy(),
	}
}

func (c *Client) HasAPIKey() bool {
	return c.apiKey != ""
}

func (c *Client) SetRequestLimiter(limiter requestcontrol.Limiter) {
	c.requestLimiter = limiter
}

func (c *Client) SetRequestRecorder(recorder requestcontrol.Recorder) {
	c.requestRecorder = recorder
}

func (c *Client) SetRetryPolicy(policy RetryPolicy) {
	c.retryPolicy = policy.normalized()
}

func (c *Client) SetToolEffectJournal(journal tool.ToolEffectJournal) {
	c.toolEffectJournal = journal
}

func (c *Client) SetToolArtifactStore(artifactStore tool.ToolArtifactWriter) {
	c.toolArtifactStore = artifactStore
}

func (c *Client) SetToolArtifactPolicy(policy ToolArtifactPolicy) {
	c.toolArtifactPolicy = policy
}

func (c *Client) RuntimeIdentity() RuntimeIdentity {
	return RuntimeIdentity{
		Provider: providerForURL(c.baseURL), BaseURL: safeRuntimeURL(c.baseURL), Model: c.model,
		EmbeddingBaseURL: safeRuntimeURL(c.embeddingBaseURL), EmbeddingModel: c.embeddingModel,
		EmbeddingDimensions: c.embeddingDimensions, EmbeddingProvider: providerForURL(c.embeddingBaseURL),
		GenerationPolicy: c.generationPolicy.Clone(), SeedSupported: c.seedSupported,
	}
}

func (c *Client) WithRuntimeIdentity(identity RuntimeIdentity) provider.Client {
	client := NewClientWithTimeoutAndEmbeddingModel(c.apiKey, identity.BaseURL, identity.EmbeddingBaseURL,
		identity.Model, identity.EmbeddingModel, identity.EmbeddingDimensions, c.timeout)
	client.requestLimiter = c.requestLimiter
	client.requestRecorder = c.requestRecorder
	client.retryPolicy = c.retryPolicy
	client.toolEffectJournal = c.toolEffectJournal
	client.toolArtifactStore = c.toolArtifactStore
	client.toolArtifactPolicy = c.toolArtifactPolicy
	client.generationPolicy = identity.GenerationPolicy.Clone()
	if client.generationPolicy.AnswerStream.Temperature == nil && client.generationPolicy.Completion.Temperature == nil {
		client.generationPolicy = domain.DefaultGenerationPolicy()
	}
	client.seedSupported = identity.SeedSupported
	return client
}

func safeRuntimeURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if isSensitiveRuntimeQueryKey(key) {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func isSensitiveRuntimeQueryKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "api_key", "apikey", "x_api_key", "subscription_key", "key", "token", "access_token", "password", "secret", "signature", "sig", "authorization", "credential", "auth":
		return true
	}
	for _, suffix := range []string{"_token", "_secret", "_password", "_signature", "_credential"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func providerForURL(value string) string {
	host := strings.ToLower(strings.TrimSpace(value))
	if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	switch {
	case strings.Contains(host, "openrouter"):
		return "openrouter"
	case strings.Contains(host, "groq"):
		return "groq"
	case strings.Contains(host, "openai"):
		return "openai"
	case strings.Contains(host, "localhost"), strings.Contains(host, "127.0.0.1"):
		return "local"
	default:
		return "openai_compatible"
	}
}
