package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Real persistence, not a mocked Store: explicit zero, absent details, quote
// identity, duplicate settlement and malformed subsets survive the DB boundary.
func TestPostgresUsageBreakdownRoundTrip(t *testing.T) {
	s, err := NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conversation, err := s.CreateConversation("usage breakdown")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testRuntimeSnapshot()
	price := int64(500000)
	snapshot.ModelRouting.Routes = []domain.ModelRouteDescriptor{{ID: "fixture", Model: "model", Pricing: domain.ModelRoutePricing{Source: "fixture", InputPerMillionTokensMicros: 2000000, OutputPerMillionTokensMicros: 1000000, CachedInputPerMillionTokensMicros: &price}}}
	run, err := s.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := s.GetRun(run.ID)
	if err != nil || !ok || !reflect.DeepEqual(loaded.RuntimeSnapshot.ModelRouting, snapshot.ModelRouting) {
		t.Fatalf("frozen pricing lost: %#v", loaded.RuntimeSnapshot)
	}
	for _, operation := range []string{"known", "unknown"} {
		entry := domain.RunUsageEntry{ID: operation + "-reserve", RunID: run.ID, OperationID: operation, Kind: domain.UsageModelReservation, Purpose: domain.UsagePurposePrimary, ModelCalls: 1, PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11, Timestamp: time.Now().UTC()}
		if _, _, err := s.ApplyRunUsage(entry); err != nil {
			t.Fatal(err)
		}
		entry.ID, entry.Kind = operation+"-settle", domain.UsageModelSettlement
		entry.CompletionTokens, entry.TotalTokens = 4, 14
		if operation == "known" {
			zero, two := 0, 2
			entry.Breakdown = &domain.UsageBreakdown{Source: "openai_details", CachedInputTokens: &zero, ReasoningTokens: &two}
			entry.EstimatedCostMicros = 24
			entry.CostDetails = &domain.UsageCostDetails{Status: "estimated", Reason: "provider_usage", Pricing: snapshot.ModelRouting.Routes[0].Pricing}
		}
		if _, _, err := s.ApplyRunUsage(entry); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(entry)
		var copy domain.RunUsageEntry
		if err := json.Unmarshal(encoded, &copy); err != nil {
			t.Fatal(err)
		}
		ledger, applied, err := s.ApplyRunUsage(copy)
		if err != nil || applied {
			t.Fatalf("duplicate charged again: applied=%v err=%v", applied, err)
		}
		found := false
		for _, persisted := range ledger.Entries {
			if persisted.ID == entry.ID {
				found = true
				if !reflect.DeepEqual(persisted.Breakdown, entry.Breakdown) || !reflect.DeepEqual(persisted.CostDetails, entry.CostDetails) {
					t.Fatalf("round trip lost detail: %+v", persisted)
				}
			}
		}
		if !found {
			t.Fatal("settlement missing")
		}
		copy.CostDetails = &domain.UsageCostDetails{Status: "unknown", Reason: "changed", Pricing: domain.ModelRoutePricing{Source: "other"}}
		if _, _, err := s.ApplyRunUsage(copy); err == nil {
			t.Fatal("conflicting duplicate accepted")
		}
		invalid := 11
		copy.ID, copy.OperationID = "invalid", "invalid"
		copy.Breakdown = &domain.UsageBreakdown{Source: "openai_details", CachedInputTokens: &invalid}
		if _, _, err := s.ApplyRunUsage(copy); err == nil {
			t.Fatal("invalid subset persisted")
		}
	}
	ledger, ok, err := s.GetRunUsageLedger(run.ID)
	if err != nil || !ok || ledger.Totals.TotalTokens != 28 || ledger.Totals.ModelCalls != 2 || ledger.Totals.CostUnknownEntries != 1 {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
}
