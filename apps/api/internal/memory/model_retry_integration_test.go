package memory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Exercise the real client inside Memory: testing either retry loop alone
// cannot detect multiplicative HTTP attempts.
func TestMemoryModelRetryOwnership(t *testing.T) {
	for _, endpoint := range []string{"/v1/embeddings", "/api/embed"} {
		for _, operation := range []string{"recall", "commit", "mutate", "extract"} {
			for _, scenario := range []struct {
				name     string
				status   int
				code     string
				attempts int32
				success  bool
			}{
				{"exhausted", 503, "server_error", 3, false},
				{"recovered", 503, "server_error", 2, true},
				{"auth", 401, "invalid_api_key", 1, false},
				{"quota", 429, "insufficient_quota", 1, false},
			} {
				t.Run(endpoint+"/"+operation+"/"+scenario.name, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n := requests.Add(1)
						w.Header().Set("Content-Type", "application/json")
						if !scenario.success || n == 1 {
							w.WriteHeader(scenario.status)
							fmt.Fprintf(w, `{"error":{"message":"fixture failure","code":%q,"type":%q}}`, scenario.code, scenario.code)
							return
						}
						if operation == "extract" {
							fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"decision\":\"noop\"}"}}]}`)
						} else if r.URL.Path == "/api/embed" {
							fmt.Fprint(w, `{"model":"fixture","embeddings":[[1,0]]}`)
						} else {
							fmt.Fprint(w, `{"model":"fixture","data":[{"embedding":[1,0]}]}`)
						}
					}))
					defer server.Close()
					client := openai.NewClientWithTimeoutAndEmbeddingModel("fixture-key", server.URL+"/v1", server.URL+endpoint, "fixture", "fixture", 2, time.Second)
					client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
					p := newTestProvider(t, fixturestore.New(), client, ProviderOptions{MaxAttempts: 3, RetryBaseDelay: time.Millisecond})
					defer closeProvider(t, p)
					err := runMemoryModelOperation(t, p, operation, client)
					if (err == nil) != scenario.success {
						t.Fatalf("unexpected result: %v", err)
					}
					if requests.Load() != scenario.attempts {
						t.Fatalf("HTTP attempts=%d want=%d: %v", requests.Load(), scenario.attempts, err)
					}
					if err != nil && failure.Describe(err).Details["attempts"] != int(scenario.attempts) {
						t.Fatalf("physical attempt evidence lost: %+v", failure.Describe(err))
					}
				})
			}
		}
	}
}

func runMemoryModelOperation(t *testing.T, p *BuiltinProvider, operation string, client *openai.Client) error {
	t.Helper()
	ctx := t.Context()
	switch operation {
	case "recall":
		_, err := p.Recall(ctx, domain.MemorySearch{Query: "durable fact"})
		return err
	case "commit":
		_, err := p.Commit(ctx, domain.Memory{Kind: "fact", Content: "durable fact"})
		return err
	case "mutate":
		original, err := p.store.CreateMemory(domain.Memory{Kind: "fact", Content: "original fact"}, domain.MemoryEmbedding{Provider: "fixture", Model: "fixture", Embedding: []float64{1, 0}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.MutateMemory(ctx, original.WorkspaceID, original.ID, domain.MemoryMutation{OperationID: "replace", ExpectedVersion: 1, Action: "replace", Content: "corrected fact", Actor: "user", Reason: "correction"})
		return err
	default:
		p.extractor = AdaptiveCandidateExtractor{ModelForRun: func(string) (CandidateCompletionModel, error) { return client, nil }}
		_, err := p.Propose(ctx, ProposalRequest{Message: domain.Message{Role: "user", Content: "I work on a durable infrastructure project."}})
		return err
	}
}

type deniedModelLimiter struct{ calls int }

func (l *deniedModelLimiter) AcquireRequest(context.Context, string, int) (func(), error) {
	l.calls++
	return nil, &requestcontrol.OwnerAdmissionError{Code: "owner_model_queue_full"}
}

func TestMemoryModelAdmissionAndCancellationDoNotRestart(t *testing.T) {
	client := openai.NewEmbeddingClient("fixture-key", "http://localhost/embeddings", "fixture", 2, time.Second)
	limiter := &deniedModelLimiter{}
	client.SetRequestLimiter(limiter)
	p := newTestProvider(t, &recordingStore{}, client, ProviderOptions{MaxAttempts: 3, RetryBaseDelay: time.Millisecond})
	defer closeProvider(t, p)
	_, err := p.Recall(t.Context(), domain.MemorySearch{Query: "fact"})
	var denied *requestcontrol.OwnerAdmissionError
	if !errors.As(err, &denied) || limiter.calls != 1 {
		t.Fatalf("admission amplified: %d %v", limiter.calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = p.Commit(ctx, domain.Memory{Kind: "fact", Content: "fact"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
