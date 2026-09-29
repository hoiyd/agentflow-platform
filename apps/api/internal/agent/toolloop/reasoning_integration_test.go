package toolloop_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/capture"
)

// Check the wire format independently of the provider's decoded message type.
func reasoningMessages(t *testing.T, input wireRequest) []map[string]any {
	t.Helper()
	encoded, err := json.Marshal(input.Messages)
	if err != nil {
		t.Fatal(err)
	}
	var messages []map[string]any
	if err := json.Unmarshal(encoded, &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestMultiRoundPreservesReasoningWithoutExposingIt(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      domain.ModelRequestCaptureMode
		reasoning string
		present   bool
		omitUsage bool
	}{
		{"full capture", domain.ModelRequestCaptureFull, "provider-only continuation state", true, false},
		{"metadata only", domain.ModelRequestCaptureMetadata, "provider-only continuation state", true, false},
		{"empty reasoning", domain.ModelRequestCaptureFull, "", true, false},
		{"ordinary completion", domain.ModelRequestCaptureFull, "", false, false},
		{"missing usage", domain.ModelRequestCaptureMetadata, "provider-only continuation state", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reasoningForRound := func(round int) string {
				if test.reasoning == "" {
					return ""
				}
				return fmt.Sprintf("%s [%d]", test.reasoning, round)
			}
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4, MaxToolCalls: 4}, func(round int, input wireRequest) any {
				previous := 0
				for _, message := range reasoningMessages(t, input) {
					if message["role"] != "assistant" {
						continue
					}
					previous++
					value, present := message["reasoning_content"]
					if present != test.present || present && value != reasoningForRound(previous) {
						t.Errorf("round %d assistant %d lost reasoning_content: present=%v value=%v", round, previous, present, value)
					}
				}
				if previous != round-1 {
					t.Errorf("round %d retained %d assistant messages", round, previous)
				}
				response := answer("final answer")
				if round < 3 {
					response = call("calculator", fmt.Sprintf(`{"expression":"%d + 1"}`, round))
				}
				if test.present {
					response.(map[string]any)["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["reasoning_content"] = reasoningForRound(round)
				}
				if test.omitUsage {
					delete(response.(map[string]any), "usage")
				}
				return response
			})
			f.client.SetRequestRecorder(capture.NewRecorder(f.store, capture.Options{Mode: test.mode}))
			output, err := f.execute()
			if err != nil || output != "final answer" || f.requests.Load() != 3 {
				t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
			}
			ledger, _, err := f.store.GetRunUsageLedger(f.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantCompletion := 6 // Three responses report two completion tokens each.
			if test.omitUsage {
				wantCompletion = len(reasoningForRound(1))/4 + 1 + len(reasoningForRound(2))/4 + 1 + len(reasoningForRound(3)+output)/4 + 1
			}
			if ledger.Totals.CompletionTokens != wantCompletion {
				t.Fatalf("completion tokens=%d want=%d", ledger.Totals.CompletionTokens, wantCompletion)
			}
			events, _ := f.store.ListRunEvents(f.run.ID)
			encoded, _ := json.Marshal(events)
			if strings.Contains(string(encoded), "reasoning_content") || test.reasoning != "" && strings.Contains(string(encoded), test.reasoning) {
				t.Fatal("ordinary events exposed provider reasoning")
			}
			for _, event := range events {
				if event.Type != domain.EventContextAssembled {
					continue
				}
				data, _ := json.Marshal(event.Payload["manifest"])
				var manifest domain.ContextManifest
				if err := json.Unmarshal(data, &manifest); err != nil {
					t.Fatal(err)
				}
				previous := 0
				for _, entry := range manifest.Entries {
					if entry.Source != contextassembly.SourceToolCall {
						continue
					}
					previous++
					reasoning := reasoningForRound(previous)
					if !entry.Selected || entry.OriginalBytes != len(reasoning) || entry.IncludedBytes != len(reasoning) || entry.EstimatedTokens <= contextassembly.EstimateTokens(reasoning) {
						t.Fatalf("reasoning not included in Tool-call accounting: %#v", entry)
					}
				}
			}
			records, _ := f.store.ListModelRequestRecords(f.run.ID)
			if len(records) != 3 {
				t.Fatalf("request evidence count=%d", len(records))
			}
			for index, record := range records {
				if test.mode == domain.ModelRequestCaptureMetadata {
					if record.Capture.Content != "" || record.Capture.Reconstructable {
						t.Fatal("metadata-only capture stored request content")
					}
					continue
				}
				if record.Capture.Content == "" || record.Capture.Mode == domain.ModelRequestCaptureFull && !record.Capture.Reconstructable {
					t.Fatal("content capture lost request evidence")
				}
				var body struct {
					Messages []map[string]any `json:"messages"`
				}
				if err := json.Unmarshal([]byte(record.Capture.Content), &body); err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, message := range body.Messages {
					if value, present := message["reasoning_content"]; present {
						count++
						if value != reasoningForRound(count) {
							t.Fatal("capture changed reasoning content")
						}
					}
				}
				if test.present && count != index || !test.present && count != 0 {
					t.Fatalf("capture round=%d reasoning messages=%d", index+1, count)
				}
			}
		})
	}
}

func TestMultiRoundReasoningOverflowStopsBeforeNextRequest(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4, MaxToolCalls: 4}, func(round int, _ wireRequest) any {
		if round != 1 {
			return answer("must not be requested")
		}
		response := call("calculator", `{"expression":"1 + 1"}`).(map[string]any)
		response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["reasoning_content"] = strings.Repeat("x", 512000)
		return response
	})
	output, err := f.execute()
	if !errors.Is(err, contextassembly.ErrInputBudgetExceeded) || output != "" || f.requests.Load() != 1 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}
