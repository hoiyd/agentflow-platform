package budget

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Failure inventory: unknown/invalid/estimated cache usage or unknown price
// keeps full-input reservation/settlement; reasoning is an output subset.
func TestFrozenCacheCostSettlement(t *testing.T) {
	for _, tc := range []struct {
		name, breakdown, pricing string
		estimated                bool
		want                     int64
	}{
		{"cache discount", `{"source":"openai_details","cached_input_tokens":6,"reasoning_tokens":2}`, `,"cached_input_per_million_tokens_micros":500000`, false, 15},
		{"full hit", `{"source":"deepseek_cache","cached_input_tokens":10}`, `,"cached_input_per_million_tokens_micros":500000`, false, 9},
		{"free cached input", `{"source":"openai_details","cached_input_tokens":10}`, `,"cached_input_per_million_tokens_micros":0`, false, 4},
		{"known zero", `{"source":"openai_details","cached_input_tokens":0}`, `,"cached_input_per_million_tokens_micros":500000`, false, 24},
		{"unknown cache", `null`, `,"cached_input_per_million_tokens_micros":500000`, false, 24},
		{"unknown price", `{"source":"deepseek_cache","cached_input_tokens":6}`, "", false, 24},
		{"estimated cache", `{"source":"openai_details","cached_input_tokens":6}`, `,"cached_input_per_million_tokens_micros":500000`, true, 24},
		{"invalid cache", `{"source":"invalid_details"}`, `,"cached_input_per_million_tokens_micros":500000`, false, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := domain.RuntimeRunBudget{MaxModelCalls: 4, MaxCompletionTokens: 10, InputCostPerMillionTokensMicros: 9000000, OutputCostPerMillionTokensMicros: 9000000}
			run := trackerTestRun(limits)
			var price domain.ModelRoutePricing
			if err := json.Unmarshal([]byte(`{"source":"fixed_fixture","input_per_million_tokens_micros":2000000,"output_per_million_tokens_micros":1000000`+tc.pricing+`}`), &price); err != nil {
				t.Fatal(err)
			}
			run.RuntimeSnapshot.ModelRouting.Routes = []domain.ModelRouteDescriptor{{ID: "route", Model: "model", Pricing: price}}
			s := &trackerStoreStub{budget: limits}
			tracker := NewTracker(s, nil, run)
			var estimate ModelCallEstimate
			if err := json.Unmarshal([]byte(`{"OperationID":"call","Model":"model","RouteID":"route","EstimatedPromptTokens":10}`), &estimate); err != nil {
				t.Fatal(err)
			}
			reservation, err := tracker.BeginModelCall(context.Background(), estimate)
			if err != nil {
				t.Fatal(err)
			}
			if reservation.EstimatedCostMicros != 21 {
				t.Fatalf("reservation used a discount or live/global price: %d", reservation.EstimatedCostMicros)
			}
			var usage ModelUsage
			if err := json.Unmarshal([]byte(`{"PromptTokens":10,"CompletionTokens":4,"TotalTokens":14,"breakdown":`+tc.breakdown+`}`), &usage); err != nil {
				t.Fatal(err)
			}
			usage.Estimated = tc.estimated
			if err := tracker.SettleModelCall(context.Background(), reservation, usage); err != nil {
				t.Fatal(err)
			}
			ledger := BuildLedger(run.ID, limits, s.entries)
			if ledger.Totals.EstimatedCostMicros != tc.want || ledger.Totals.TotalTokens != 14 || ledger.Totals.ModelCalls != 1 {
				t.Fatalf("settlement=%+v", ledger.Totals)
			}
		})
	}
}

func TestCacheCostUnknownAndOverflow(t *testing.T) {
	_, details := usageCost(domain.ModelRoutePricing{Source: "run_budget"}, 10, 1, nil, true)
	if details.Status != "unknown" || details.Reason != "pricing_unknown" {
		t.Fatalf("unknown price mislabeled: %+v", details)
	}
	if got := tokenCostMicros(math.MaxInt, math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("cost overflowed: %d", got)
	}
	if err := Check(domain.RuntimeRunBudget{MaxEstimatedCostMicros: math.MaxInt64 - 1}, domain.RunUsageTotals{EstimatedCostMicros: math.MaxInt64 - 2}, domain.RunUsageTotals{EstimatedCostMicros: 10}, "op", domain.UsagePurposePrimary); err == nil {
		t.Fatal("overflow bypassed cost budget")
	}
}

func TestCacheSettlementRejectsInvalidSubsetAndRoute(t *testing.T) {
	run := trackerTestRun(domain.RuntimeRunBudget{})
	tracker := NewTracker(&trackerStoreStub{}, nil, run)
	if _, err := tracker.BeginModelCall(context.Background(), ModelCallEstimate{RouteID: "absent", Model: "model"}); err == nil {
		t.Fatal("unknown route accepted")
	}
	run.RuntimeSnapshot.ModelRouting.Routes = []domain.ModelRouteDescriptor{{ID: "route", Model: "model"}}
	if _, err := tracker.BeginModelCall(context.Background(), ModelCallEstimate{RouteID: "route", Model: "other"}); err == nil {
		t.Fatal("wrong model accepted")
	}
	bad := 11
	if err := tracker.SettleModelCall(context.Background(), ModelReservation{}, ModelUsage{PromptTokens: 10, Breakdown: &domain.UsageBreakdown{Source: "openai_details", CachedInputTokens: &bad}}); err == nil {
		t.Fatal("invalid subset accepted")
	}
}
