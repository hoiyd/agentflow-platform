package rageval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/openai"
)

const (
	EmbeddingProfileHash             = "hash"
	EmbeddingProfileOpenAICompatible = "openai_compatible"
	EmbeddingProfileOllama           = "ollama"

	phaseIndex = "index"
	phaseQuery = "query"
)

type EmbeddingProfileOptions struct {
	Name             string
	Live             bool
	APIKey           string
	BaseURL          string
	Model            string
	Dimensions       int
	MaxCalls         int
	MaxInputTokens   int
	RetryMaxAttempts int
	Timeout          time.Duration
}

type EmbeddingProfileReport struct {
	Name                  string         `json:"name"`
	Live                  bool           `json:"live"`
	ConfiguredModel       string         `json:"configured_model"`
	ConfiguredDimensions  int            `json:"configured_dimensions"`
	MaxCalls              int            `json:"max_calls,omitempty"`
	MaxInputTokens        int            `json:"max_input_tokens,omitempty"`
	RetryMaxAttempts      int            `json:"retry_max_attempts"`
	TimeoutMS             int64          `json:"timeout_ms"`
	IndexInputTransform   string         `json:"index_input_transform"`
	QueryInputTransform   string         `json:"query_input_transform"`
	DistanceMetric        string         `json:"distance_metric"`
	LexicalImplementation string         `json:"lexical_implementation"`
	Usage                 EmbeddingUsage `json:"usage"`
}

type EmbeddingUsage struct {
	Source                 string              `json:"source"`
	LogicalInputs          int                 `json:"logical_inputs"`
	PhysicalRequests       int                 `json:"physical_requests"`
	EstimatedInputTokens   int                 `json:"estimated_input_tokens"`
	EstimatedRequestTokens int                 `json:"estimated_request_tokens"`
	RejectedInputs         int                 `json:"rejected_inputs"`
	RejectedRequests       int                 `json:"rejected_requests"`
	Index                  EmbeddingPhaseUsage `json:"index"`
	Query                  EmbeddingPhaseUsage `json:"query"`
	EstimatedCostUSD       *float64            `json:"estimated_cost_usd"`
}

type EmbeddingPhaseUsage struct {
	LogicalInputs          int `json:"logical_inputs"`
	PhysicalRequests       int `json:"physical_requests"`
	EstimatedInputTokens   int `json:"estimated_input_tokens"`
	EstimatedRequestTokens int `json:"estimated_request_tokens"`
}

type embeddingProfileError struct {
	code     string
	category failure.Category
	message  string
}

func (e *embeddingProfileError) Error() string { return e.message }
func (e *embeddingProfileError) FailureInfo() failure.Info {
	return failure.Info{Code: e.code, Source: "rag_embedding_profile", Category: e.category}
}

type embeddingPhaseKey struct{}

type evaluationEmbedder struct {
	client  *openai.Client
	options EmbeddingProfileOptions

	mu       sync.Mutex
	usage    EmbeddingUsage
	identity domain.EmbeddingInfo
}

func newEvaluationEmbedder(options EmbeddingProfileOptions) (*evaluationEmbedder, error) {
	normalized, err := normalizeEmbeddingProfile(options)
	if err != nil {
		return nil, err
	}
	client := openai.NewClientWithTimeoutAndEmbeddingModel(normalized.APIKey, "", normalized.BaseURL, "", normalized.Model, normalized.Dimensions, normalized.Timeout)
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: normalized.RetryMaxAttempts})
	embedder := &evaluationEmbedder{client: client, options: normalized, usage: EmbeddingUsage{Source: "estimated_input_tokens_and_physical_requests"}}
	if normalized.Name != EmbeddingProfileHash {
		client.SetRequestLimiter(embedder)
	}
	return embedder, nil
}

func normalizeEmbeddingProfile(options EmbeddingProfileOptions) (EmbeddingProfileOptions, error) {
	options.Name = strings.ToLower(strings.TrimSpace(options.Name))
	if options.Name == "" {
		options.Name = EmbeddingProfileHash
	}
	if options.Name == EmbeddingProfileHash {
		if options.Live {
			return options, errors.New("hash embedding profile cannot be combined with live model access")
		}
		options.Model, options.Dimensions = "local_hash_embedding", 1536
		options.BaseURL, options.APIKey, options.RetryMaxAttempts, options.Timeout = "https://offline.invalid/v1", "", 1, time.Second
		return options, nil
	}
	if options.Name != EmbeddingProfileOpenAICompatible && options.Name != EmbeddingProfileOllama {
		return options, fmt.Errorf("unsupported embedding profile %q", options.Name)
	}
	if !options.Live {
		return options, errors.New("real embedding profiles require explicit live model access")
	}
	if strings.TrimSpace(options.Model) == "" || options.Dimensions <= 0 || options.Dimensions > 65536 {
		return options, errors.New("real embedding profiles require a model and dimensions between 1 and 65536")
	}
	if options.MaxCalls <= 0 || options.MaxInputTokens <= 0 {
		return options, errors.New("real embedding profiles require positive call and input-token budgets")
	}
	if options.Timeout <= 0 || options.Timeout > 5*time.Minute {
		return options, errors.New("real embedding profile timeout must be between 1ns and 5m")
	}
	if options.RetryMaxAttempts < 1 || options.RetryMaxAttempts > 5 {
		return options, errors.New("embedding retry attempts must be between 1 and 5")
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(options.BaseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return options, errors.New("real embedding profile requires an HTTP(S) base URL")
	}
	options.BaseURL = strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	options.Model = strings.TrimSpace(options.Model)
	if options.Name == EmbeddingProfileOpenAICompatible && strings.TrimSpace(options.APIKey) == "" {
		return options, errors.New("openai-compatible embedding profile requires OPENAI_API_KEY; hash fallback is disabled")
	}
	if options.Name == EmbeddingProfileOllama && !strings.HasSuffix(options.BaseURL, "/api/embed") {
		return options, errors.New("ollama embedding base URL must end with /api/embed")
	}
	if options.Name == EmbeddingProfileOllama {
		options.APIKey = ""
	}
	return options, nil
}

func (e *evaluationEmbedder) EmbedText(ctx context.Context, input string) (modelprovider.Embedding, error) {
	tokens := contextassembly.EstimateTokens(input)
	phase := embeddingPhase(ctx)
	if err := e.reserveInput(phase, tokens); err != nil {
		return modelprovider.Embedding{}, err
	}
	embedding, err := e.client.EmbedText(ctx, input)
	if err != nil {
		return embedding, err
	}
	if err := e.validate(embedding); err != nil {
		return modelprovider.Embedding{}, err
	}
	return embedding, nil
}

func (e *evaluationEmbedder) AcquireRequest(ctx context.Context, _ string, estimatedTokens int) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.usage.PhysicalRequests >= e.options.MaxCalls {
		e.usage.RejectedRequests++
		return nil, &openai.ModelError{Kind: openai.ErrorRequestCallCapacity, Message: "embedding evaluation call budget exhausted"}
	}
	e.usage.PhysicalRequests++
	e.usage.EstimatedRequestTokens += estimatedTokens
	phase := phaseUsage(&e.usage, embeddingPhase(ctx))
	phase.PhysicalRequests++
	phase.EstimatedRequestTokens += estimatedTokens
	return func() {}, nil
}

func (e *evaluationEmbedder) reserveInput(phaseName string, tokens int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.options.MaxInputTokens > 0 && e.usage.EstimatedInputTokens+tokens > e.options.MaxInputTokens {
		e.usage.RejectedInputs++
		return &embeddingProfileError{code: "embedding_input_budget_exceeded", category: failure.CategoryCapacity, message: "embedding evaluation input-token budget exhausted"}
	}
	e.usage.LogicalInputs++
	e.usage.EstimatedInputTokens += tokens
	phase := phaseUsage(&e.usage, phaseName)
	phase.LogicalInputs++
	phase.EstimatedInputTokens += tokens
	return nil
}

func (e *evaluationEmbedder) validate(embedding modelprovider.Embedding) error {
	if len(embedding.Vector) == 0 || embedding.Dimensions != len(embedding.Vector) {
		return &embeddingProfileError{code: "embedding_invalid_vector", category: failure.CategoryExecution, message: "embedding response returned an empty or inconsistent vector"}
	}
	for _, value := range embedding.Vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return &embeddingProfileError{code: "embedding_invalid_vector", category: failure.CategoryExecution, message: "embedding response returned a non-finite vector"}
		}
	}
	expectedProvider := map[string]string{EmbeddingProfileHash: "local", EmbeddingProfileOpenAICompatible: "openai_compatible", EmbeddingProfileOllama: "ollama"}[e.options.Name]
	if embedding.Provider != expectedProvider || embedding.Model != e.options.Model || embedding.Dimensions != e.options.Dimensions {
		return &embeddingProfileError{code: "embedding_profile_mismatch", category: failure.CategoryValidation, message: "embedding response does not match the configured provider, model, or dimensions"}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	identity := domain.EmbeddingInfo{Provider: embedding.Provider, Model: embedding.Model, Dimensions: embedding.Dimensions, Estimated: embedding.Estimated}
	if e.identity.Provider != "" && e.identity != identity {
		return &embeddingProfileError{code: "embedding_profile_mismatch", category: failure.CategoryValidation, message: "embedding identity changed during evaluation"}
	}
	e.identity = identity
	return nil
}

func (e *evaluationEmbedder) report() (domain.EmbeddingInfo, EmbeddingUsage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.identity, e.usage
}

func withEmbeddingPhase(ctx context.Context, phase string) context.Context {
	return context.WithValue(ctx, embeddingPhaseKey{}, phase)
}

func embeddingPhase(ctx context.Context) string {
	if phase, _ := ctx.Value(embeddingPhaseKey{}).(string); phase == phaseQuery {
		return phaseQuery
	}
	return phaseIndex
}

func phaseUsage(usage *EmbeddingUsage, phase string) *EmbeddingPhaseUsage {
	if phase == phaseQuery {
		return &usage.Query
	}
	return &usage.Index
}
