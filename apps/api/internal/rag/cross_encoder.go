package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/egress"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/redaction"
)

// RerankerConfig is operator-owned. TEI serves one configured model; its response
// has no model identity, so Model/Revision declare the pinned deployment, not an
// independently verified artifact. Secrets never enter serializable identity.
type RerankerConfig struct {
	Mode          string
	BaseURL       string
	Model         string
	Revision      string
	Timeout       time.Duration
	MaxConcurrent int
	APIKey        credential.Value `json:"-"`
}

type crossEncoderReranker struct {
	client   *http.Client
	endpoint string
	token    string
	info     domain.RerankerInfo
	slots    chan struct{}
}

// NewReranker preserves heuristic defaults; opting into TEI never falls back.
func NewReranker(cfg RerankerConfig) (Reranker, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case "", "heuristic":
		return NewHeuristicReranker(DefaultHeuristicRerankerConfig()), nil
	case "tei":
	default:
		return nil, errors.New("RERANKER_MODE must be heuristic or tei")
	}
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, errors.New("reranker base URL must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || (!ip.IsLoopback() && !ip.IsPrivate())) {
			return nil, errors.New("remote reranker origins require HTTPS")
		}
	}
	cfg.Model, cfg.Revision = strings.TrimSpace(cfg.Model), strings.TrimSpace(cfg.Revision)
	if cfg.Model == "" || cfg.Revision == "" || redaction.ValidateText(cfg.Model, cfg.Revision) != nil {
		return nil, errors.New("TEI reranking requires non-secret model and pinned revision identities")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 2
	}
	u.Path = ""
	origin := u.String()
	identity, _ := json.Marshal(struct {
		Origin, Model, Revision string
		Timeout                 time.Duration
		Concurrency             int
	}{origin, cfg.Model, cfg.Revision, cfg.Timeout, cfg.MaxConcurrent})
	hash := sha256.Sum256(identity)
	return &crossEncoderReranker{
		client:   &http.Client{Timeout: cfg.Timeout, Transport: egress.NewTransport([]string{origin}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: origin + "/rerank", token: cfg.APIKey.Reveal(), slots: make(chan struct{}, cfg.MaxConcurrent),
		info: domain.RerankerInfo{Algorithm: "cross_encoder", Version: "tei-cross-encoder-v1", ConfigVersion: fmt.Sprintf("tei-%x", hash[:16]), Provider: "tei", Model: cfg.Model},
	}, nil
}

func (r *crossEncoderReranker) Rerank(ctx context.Context, request RerankRequest) (RerankResult, error) {
	result := RerankResult{Info: r.info, Decisions: []RerankDecision{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(request.Candidates) == 0 || request.Limit <= 0 {
		return result, nil
	}
	// Each recall arm is capped at 20; never quietly skip part of the fused set.
	if strings.TrimSpace(request.Query) == "" || len(request.Candidates) > 40 {
		return result, rerankFailure("reranker_invalid_input", failure.CategoryValidation, false)
	}
	texts := make([]string, len(request.Candidates))
	for index, candidate := range request.Candidates {
		texts[index] = candidate.Chunk.Content
	}
	encoded, err := json.Marshal(struct {
		Query      string   `json:"query"`
		Texts      []string `json:"texts"`
		RawScores  bool     `json:"raw_scores"`
		ReturnText bool     `json:"return_text"`
		Truncate   bool     `json:"truncate"`
	}{Query: request.Query, Texts: texts})
	if err != nil || len(encoded) > 1<<20 {
		return result, rerankFailure("reranker_invalid_input", failure.CategoryValidation, false)
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return result, rerankFailure("reranker_overloaded", failure.CategoryCapacity, true)
	}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return result, rerankFailure("reranker_invalid_input", failure.CategoryValidation, false)
	}
	call.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		call.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(call)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return result, rerankFailure("reranker_timeout", failure.CategoryTimeout, true)
		}
		return result, rerankFailure("reranker_unavailable", failure.CategoryAvailability, true)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch {
		case response.StatusCode == 401 || response.StatusCode == 403:
			return result, rerankFailure("reranker_authentication", failure.CategoryAuthentication, false)
		case response.StatusCode == 429:
			return result, rerankFailure("reranker_overloaded", failure.CategoryCapacity, true)
		case response.StatusCode >= 500:
			return result, rerankFailure("reranker_unavailable", failure.CategoryAvailability, true)
		default:
			return result, rerankFailure("reranker_request_rejected", failure.CategoryValidation, false)
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return result, rerankFailure("reranker_timeout", failure.CategoryTimeout, true)
		}
	}
	if err != nil || len(body) > 64<<10 {
		return result, rerankFailure("reranker_invalid_response", failure.CategoryValidation, false)
	}
	var scores []struct {
		Index *int     `json:"index"`
		Score *float64 `json:"score"`
	}
	if json.Unmarshal(body, &scores) != nil || len(scores) != len(texts) {
		return result, rerankFailure("reranker_invalid_response", failure.CategoryValidation, false)
	}
	seen := make(map[int]bool, len(scores))
	for _, item := range scores {
		if item.Index == nil || item.Score == nil || *item.Index < 0 || *item.Index >= len(texts) || seen[*item.Index] || !finiteScore(*item.Score) || *item.Score < 0 || *item.Score > 1 {
			return result, rerankFailure("reranker_invalid_response", failure.CategoryValidation, false)
		}
		seen[*item.Index] = true
	}
	sort.Slice(scores, func(i, j int) bool {
		if *scores[i].Score == *scores[j].Score {
			return *scores[i].Index < *scores[j].Index
		}
		return *scores[i].Score > *scores[j].Score
	})
	for rank, item := range scores[:min(request.Limit, len(scores))] {
		candidate := request.Candidates[*item.Index]
		result.Decisions = append(result.Decisions, RerankDecision{DocumentID: candidate.Document.ID, ChunkID: candidate.Chunk.ID, RerankRank: rank + 1, RerankScore: *item.Score})
	}
	return result, nil
}

func rerankFailure(code string, category failure.Category, retryable bool) error {
	return failure.New(failure.Definition{Message: code, Info: failure.Info{Code: code, Source: "reranker", Category: category, Retryable: retryable, Operation: "rag.rerank"}})
}
