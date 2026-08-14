package openai

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelrequest"
	"agentflow-platform/apps/api/internal/tools"
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
	requestLimiter      modelrequest.Limiter
	requestRecorder     modelrequest.Recorder
	retryPolicy         RetryPolicy
}

type RuntimeIdentity struct {
	Provider            string
	BaseURL             string
	Model               string
	EmbeddingBaseURL    string
	EmbeddingModel      string
	EmbeddingDimensions int
}

type Message struct {
	Role        string     `json:"role"`
	Content     string     `json:"content,omitempty"`
	ToolCallID  string     `json:"tool_call_id,omitempty"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
	Source      string     `json:"-"`
	ReferenceID string     `json:"-"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamEvent struct {
	Type       string
	Delta      string
	ToolName   string
	ToolCallID string
	Error      string
}

type streamToolExecutionTracer struct {
	delegate tools.ExecutionTracer
	events   chan<- StreamEvent
}

func (t *streamToolExecutionTracer) ToolStarted(ctx context.Context, request tools.ExecutionRequest) {
	if t.delegate != nil {
		t.delegate.ToolStarted(ctx, request)
	}
	log.Printf("tool_call_start id=%s tool=%s arguments=%q", request.CallID, request.Tool, string(request.Arguments))
	select {
	case <-ctx.Done():
	case t.events <- StreamEvent{Type: "tool_start", ToolName: request.Tool, ToolCallID: request.CallID}:
	}
}

func (t *streamToolExecutionTracer) ToolFinished(ctx context.Context, result tools.ExecutionResult) {
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
	log.Printf(
		"tool_call_end id=%s tool=%s status=%s duration_ms=%d arguments=%q result=%q error=%q",
		result.CallID,
		result.Tool,
		status,
		result.DurationMS,
		string(result.Arguments),
		marshalResult(result),
		result.ErrorMessage(),
	)
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	Estimated        bool
}

type TextCompletion struct {
	Text  string
	Model string
	Usage Usage
}

type PreparedText struct {
	Messages []Message
	Manifest domain.ContextManifest
}

type Embedding struct {
	Vector     []float64
	Model      string
	Provider   string
	Estimated  bool
	Dimensions int
}

func NewClient(apiKey string, baseURL string, model string) *Client {
	return NewClientWithTimeout(apiKey, baseURL, model, 5*time.Minute)
}

func NewClientWithTimeout(apiKey string, baseURL string, model string, timeout time.Duration) *Client {
	return NewClientWithTimeoutAndEmbeddingModel(apiKey, baseURL, defaultEmbeddingBaseURL, model, defaultEmbeddingModel, 1536, timeout)
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
		timeout:     timeout,
		retryPolicy: DefaultRetryPolicy(),
	}
}

func (c *Client) HasAPIKey() bool {
	return c.apiKey != ""
}

func (c *Client) SetRequestLimiter(limiter modelrequest.Limiter) {
	c.requestLimiter = limiter
}

func (c *Client) SetRequestRecorder(recorder modelrequest.Recorder) {
	c.requestRecorder = recorder
}

func (c *Client) SetRetryPolicy(policy RetryPolicy) {
	c.retryPolicy = policy.normalized()
}

func (c *Client) RuntimeIdentity() RuntimeIdentity {
	return RuntimeIdentity{
		Provider: providerForURL(c.baseURL), BaseURL: safeRuntimeURL(c.baseURL), Model: c.model,
		EmbeddingBaseURL: safeRuntimeURL(c.embeddingBaseURL), EmbeddingModel: c.embeddingModel,
		EmbeddingDimensions: c.embeddingDimensions,
	}
}

func (c *Client) WithRuntimeIdentity(identity RuntimeIdentity) *Client {
	client := NewClientWithTimeoutAndEmbeddingModel(c.apiKey, identity.BaseURL, identity.EmbeddingBaseURL,
		identity.Model, identity.EmbeddingModel, identity.EmbeddingDimensions, c.timeout)
	client.requestLimiter = c.requestLimiter
	client.requestRecorder = c.requestRecorder
	client.retryPolicy = c.retryPolicy
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
