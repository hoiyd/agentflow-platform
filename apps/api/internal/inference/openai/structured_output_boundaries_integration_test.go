package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/inference/provider"
)

// Provider rejection, incomplete generation and budget errors must stop at their
// own boundary, never become a parsed report or trigger whole-Run repair.
func TestStructuredGenerationFinishAndCapabilityBoundaries(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, outcome := range []string{"missing", "length", "refusal", "undeclared"} {
			t.Run(fmt.Sprintf("%s/stream=%t", outcome, stream), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					message := map[string]any{"content": `{"ok":true}`}
					choice := map[string]any{"index": 0}
					if outcome == "length" {
						choice["finish_reason"] = "length"
					}
					if outcome == "refusal" {
						choice["finish_reason"] = "stop"
						message["refusal"] = "request declined"
					}
					if stream {
						choice["delta"] = message
					} else {
						choice["message"] = message
					}
					body, _ := json.Marshal(map[string]any{"choices": []any{choice}})
					if stream {
						fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
					} else {
						_, _ = w.Write(body)
					}
				}))
				defer server.Close()
				client := NewClient("fixture", server.URL, "fixture")
				if outcome != "undeclared" {
					client.structuredOutputFormat = provider.OutputFormatOpenAIJSONSchema
				}
				ctx := provider.WithStructuredOutput(context.Background(), provider.StructuredOutput{Mode: "strict", Schema: map[string]any{"type": "object"}})
				var err error
				if stream {
					_, err = client.StreamToolRound(ctx, provider.PreparedChat{Messages: []Message{{Role: "user", Content: "input"}}}, nil, provider.ChatTrace{}, make(chan StreamEvent, 16))
				} else {
					_, err = client.CompletePreparedText(ctx, PreparedText{Messages: []Message{{Role: "user", Content: "input"}}})
				}
				want := ErrorInvalidResponse
				if outcome == "length" {
					want = ErrorIncompleteOutput
				}
				if outcome == "refusal" {
					want = ErrorContentPolicy
				}
				if outcome == "undeclared" {
					want = ErrorInvalidRequest
				}
				modelErr, ok := AsModelError(err)
				if !ok || modelErr.Kind != want {
					t.Fatalf("wrong terminal outcome: %v", err)
				}
				wantCalls := int32(1)
				if outcome == "undeclared" {
					wantCalls = 0
				}
				if calls.Load() != wantCalls {
					t.Fatalf("unexpected repair/provider requests=%d", calls.Load())
				}
			})
		}
	}
}

func TestOutputWireSchemaConsumesContextBeforeSending(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	client := NewClient("fixture", server.URL, "fixture")
	client.structuredOutputFormat = provider.OutputFormatOpenAIJSONSchema
	ctx := provider.WithStructuredOutput(context.Background(), provider.StructuredOutput{Mode: "strict", Schema: map[string]any{"type": "object", "description": strings.Repeat("x", 2000)}})
	config := contextassembly.DefaultConfig()
	config.OutputReserveTokens, config.SafetyMarginTokens = 10, 10
	prepared, err := client.PrepareAgentChat(contextassembly.WithSession(ctx, contextassembly.Session{Config: config}), provider.ChatRequest{Latest: "input"})
	if err != nil {
		t.Fatal(err)
	}
	schemaTokens := 0
	for _, entry := range prepared.Manifest.Entries {
		if entry.Source == "output_schema" {
			schemaTokens += entry.EstimatedTokens
		}
	}
	if schemaTokens == 0 {
		t.Fatal("strict wire schema is missing from Context accounting")
	}
	config.ContextWindowTokens = prepared.Manifest.EstimatedInputTokens - schemaTokens/2 + config.OutputReserveTokens + config.SafetyMarginTokens
	_, err = client.CompleteTextDetailed(contextassembly.WithSession(ctx, contextassembly.Session{Config: config}), "extract", "input")
	var budgetErr *contextassembly.InputBudgetError
	if !errors.As(err, &budgetErr) || calls.Load() != 0 {
		t.Fatalf("over-budget schema reached provider: %v calls=%d", err, calls.Load())
	}
	client.structuredOutputFormat = ""
	if _, err = client.PrepareText(ctx, "extract", "input"); err == nil {
		t.Fatal("undeclared strict format passed preparation")
	}
}
