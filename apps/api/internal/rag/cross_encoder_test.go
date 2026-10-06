package rag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

func TestCrossEncoderHTTPContractAndFailures(t *testing.T) {
	candidates := []domain.RetrievedDocumentChunk{
		{Document: domain.Document{ID: "doc-a", WorkspaceID: "private-workspace"}, Chunk: domain.DocumentChunk{ID: "chunk-a", Content: "first recovery procedure"}},
		{Document: domain.Document{ID: "doc-b", WorkspaceID: "private-workspace"}, Chunk: domain.DocumentChunk{ID: "chunk-b", Content: "second recovery procedure"}},
	}
	for _, scenario := range []struct {
		name, body, code string
		status           int
	}{
		{"reordered", `[{"index":0,"score":0.6,"text":"forged content"},{"index":1,"score":0.9}]`, "", 200},
		{"ties", `[{"index":1,"score":0.8},{"index":0,"score":0.8}]`, "", 200},
		{"zero score", `[{"index":0,"score":0},{"index":1,"score":1}]`, "", 200},
		{"missing candidate", `[{"index":0,"score":0.8}]`, "reranker_invalid_response", 200},
		{"duplicate", `[{"index":0,"score":0.8},{"index":0,"score":0.9}]`, "reranker_invalid_response", 200},
		{"foreign index", `[{"index":0,"score":0.8},{"index":2,"score":0.9}]`, "reranker_invalid_response", 200},
		{"negative index", `[{"index":-1,"score":0.8},{"index":1,"score":0.9}]`, "reranker_invalid_response", 200},
		{"missing index", `[{"score":0.8},{"index":1,"score":0.9}]`, "reranker_invalid_response", 200},
		{"missing score", `[{"index":0},{"index":1,"score":0.9}]`, "reranker_invalid_response", 200},
		{"raw logits", `[{"index":0,"score":2},{"index":1,"score":0.9}]`, "reranker_invalid_response", 200},
		{"negative score", `[{"index":0,"score":-0.1},{"index":1,"score":0.9}]`, "reranker_invalid_response", 200},
		{"non-finite", `[{"index":0,"score":1e999},{"index":1,"score":0.9}]`, "reranker_invalid_response", 200},
		{"trailing JSON", `[{"index":0,"score":0.8},{"index":1,"score":0.9}] {}`, "reranker_invalid_response", 200},
		{"large response", strings.Repeat(" ", 65537), "reranker_invalid_response", 200},
		{"provider error", `PRIVATE_PROVIDER_BODY`, "reranker_unavailable", 503},
		{"authentication", `PRIVATE_PROVIDER_BODY`, "reranker_authentication", 401},
		{"rate limit", `PRIVATE_PROVIDER_BODY`, "reranker_overloaded", 429},
		{"invalid request", `PRIVATE_PROVIDER_BODY`, "reranker_request_rejected", 422},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Query                           string   `json:"query"`
					Texts                           []string `json:"texts"`
					RawScores, ReturnText, Truncate *bool
				}
				// Decode flags independently; production JSON tags must match TEI.
				var input map[string]json.RawMessage
				if r.Method != "POST" || r.URL.Path != "/rerank" || json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Error("invalid wire request")
					return
				}
				_ = json.Unmarshal(input["query"], &wire.Query)
				_ = json.Unmarshal(input["texts"], &wire.Texts)
				if wire.Query != "recovery" || len(wire.Texts) != 2 || wire.Texts[1] != candidates[1].Chunk.Content {
					t.Errorf("wire=%+v", wire)
				}
				for _, key := range []string{"raw_scores", "return_text", "truncate"} {
					if string(input[key]) != "false" {
						t.Errorf("%s=%s", key, input[key])
					}
				}
				if _, ok := input["workspace_id"]; ok {
					t.Error("scope metadata leaked into provider body")
				}
				w.WriteHeader(scenario.status)
				_, _ = w.Write([]byte(scenario.body))
			}))
			defer server.Close()
			reranker, err := NewReranker(RerankerConfig{Mode: "tei", BaseURL: server.URL, Model: "fixture-cross-encoder", Revision: "fixture-revision", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			result, err := reranker.Rerank(t.Context(), RerankRequest{Query: "recovery", Candidates: candidates, Limit: 1})
			if scenario.code != "" {
				if err == nil || failure.Describe(err).Code != scenario.code || strings.Contains(err.Error(), "PRIVATE_PROVIDER_BODY") {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			items, err := applyRerankResult(RerankRequest{Candidates: candidates, Limit: 1}, result)
			want := "chunk-b"
			if scenario.name == "ties" {
				want = "chunk-a"
			}
			if err != nil || len(items) != 1 || items[0].Chunk.ID != want || items[0].Document.WorkspaceID != "private-workspace" || strings.Contains(items[0].Chunk.Content, "forged") {
				t.Fatalf("items=%+v err=%v", items, err)
			}
			if result.Info.Algorithm != "cross_encoder" || result.Info.Provider != "tei" || result.Info.Model != "fixture-cross-encoder" || result.Info.ConfigVersion == "" {
				t.Fatalf("identity=%+v", result.Info)
			}
			if candidates[0].RerankRank != 0 {
				t.Fatal("input candidates mutated")
			}
		})
	}
}

func TestCrossEncoderConfigurationAndInputLimits(t *testing.T) {
	if r, err := NewReranker(RerankerConfig{}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(*HeuristicReranker); !ok {
		t.Fatal("default changed")
	}
	for _, cfg := range []RerankerConfig{
		{Mode: "typo"}, {Mode: "tei"}, {Mode: "tei", BaseURL: "file:///tmp/rerank", Model: "m", Revision: "r"},
		{Mode: "tei", BaseURL: "https://user:password@fixture.example", Model: "m", Revision: "r"},
		{Mode: "tei", BaseURL: "https://fixture.example?token=private", Model: "m", Revision: "r"},
		{Mode: "tei", BaseURL: "https://fixture.example/path", Model: "m", Revision: "r"},
		{Mode: "tei", BaseURL: "https://fixture.example", Model: "", Revision: "r"},
		{Mode: "tei", BaseURL: "https://fixture.example", Model: "m", Revision: ""},
	} {
		if _, err := NewReranker(cfg); err == nil {
			t.Fatalf("invalid configuration accepted: mode=%q", cfg.Mode)
		}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("invalid input reached network")
	}))
	defer server.Close()
	reranker, err := NewReranker(RerankerConfig{Mode: "tei", BaseURL: server.URL, Model: "m", Revision: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reranker.Rerank(t.Context(), RerankRequest{}); err != nil || len(result.Decisions) != 0 || result.Info.Provider != "tei" {
		t.Fatalf("empty result=%+v err=%v", result, err)
	}
	for _, request := range []RerankRequest{
		{Query: " ", Limit: 1, Candidates: []domain.RetrievedDocumentChunk{{Chunk: domain.DocumentChunk{Content: "text"}}}},
		{Query: "q", Limit: 1, Candidates: []domain.RetrievedDocumentChunk{{Chunk: domain.DocumentChunk{Content: strings.Repeat("x", 1<<20)}}}},
		{Query: "q", Limit: 1, Candidates: make([]domain.RetrievedDocumentChunk, 41)},
	} {
		if _, err := reranker.Rerank(t.Context(), request); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid input consumed network requests")
	}
}

func TestCrossEncoderCancellationOverloadAndRedirect(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rerank" {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			_, _ = w.Write([]byte(`[{"index":0,"score":0.8}]`))
		}
	}))
	defer server.Close()
	r, err := NewReranker(RerankerConfig{Mode: "tei", BaseURL: server.URL, Model: "m", Revision: "r", Timeout: time.Second, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := RerankRequest{Query: "q", Limit: 1, Candidates: []domain.RetrievedDocumentChunk{{Document: domain.Document{ID: "d"}, Chunk: domain.DocumentChunk{ID: "c", Content: "text"}}}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := r.Rerank(ctx, input); done <- err }()
	<-started
	if _, err := r.Rerank(t.Context(), input); err == nil || failure.Describe(err).Code != "reranker_overloaded" {
		t.Fatalf("overload err=%v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	close(release)
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer redirect.Close()
	r, err = NewReranker(RerankerConfig{Mode: "tei", BaseURL: redirect.URL, Model: "m", Revision: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rerank(t.Context(), input); err == nil || redirected.Load() != 0 {
		t.Fatalf("redirect followed: count=%d err=%v", redirected.Load(), err)
	}
}
