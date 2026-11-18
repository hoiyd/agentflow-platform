package sessionhistory

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type testStore struct {
	messages   []domain.Message
	events     []domain.RunEvent
	messageErr error
	eventErr   error
}

func (s testStore) ListMessages(string) ([]domain.Message, error) { return s.messages, s.messageErr }
func (s testStore) ListConversationRunEvents(string) ([]domain.RunEvent, error) {
	return s.events, s.eventErr
}

func TestSearchFiltersMessagesAndAddsAdjacentWindow(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{messages: []domain.Message{
		{ID: "msg-1", Role: "user", Content: "Set deployment ID to release-2026-08.", CreatedAt: base},
		{ID: "msg-2", Role: "assistant", Content: "Recorded the deployment ID.", CreatedAt: base.Add(time.Minute)},
		{ID: "msg-3", Role: "user", Content: "Unrelated recent question.", CreatedAt: base.Add(2 * time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv", Keywords: []string{"release-2026-08"},
		NeighborWindow: 1, MaxResults: 4, MaxCharacters: 1000, MaxTokens: 250,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].Reference != "message:msg-1" || result.Items[1].Reference != "message:msg-2" {
		t.Fatalf("unexpected message window: %#v", result.Items)
	}
	if !result.Items[0].DirectMatch || result.Items[0].MatchReason != "keyword_match" || result.Items[1].DirectMatch || result.Items[1].MatchReason != "adjacent_window" {
		t.Fatalf("match provenance was lost: %#v", result.Items)
	}
}

func TestSearchFindsEventKeywordsAndExcludesCurrentRun(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{events: []domain.RunEvent{
		{ID: "event-1", RunID: "old-run", Type: domain.EventToolFailed, Payload: map[string]any{"error": "connection refused on port 8123"}, Timestamp: base},
		{ID: "event-2", RunID: "current-run", Type: domain.EventToolFailed, Payload: map[string]any{"error": "connection refused on port 8123"}, Timestamp: base.Add(time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv",
		Keywords:       []string{"8123"}, ExcludeRunID: "current-run", MaxResults: 2, MaxCharacters: 1000, MaxTokens: 250,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Reference != "event:event-1" || result.Items[0].EventType != domain.EventToolFailed {
		t.Fatalf("unexpected event result: %#v", result.Items)
	}
	if result.Items[0].MatchReason != "keyword_match" || !strings.Contains(result.Items[0].Content, "8123") {
		t.Fatalf("event source was not preserved: %#v", result.Items[0])
	}
}

func TestSearchEnforcesResultCharacterAndTokenLimits(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{messages: []domain.Message{
		{ID: "msg-1", Role: "user", Content: "needle " + strings.Repeat("a", 400), CreatedAt: base},
		{ID: "msg-2", Role: "assistant", Content: "needle " + strings.Repeat("b", 400), CreatedAt: base.Add(time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv", Keywords: []string{"needle"}, MaxResults: 1, MaxCharacters: 80, MaxTokens: 20,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 1 || len(result.Items[0].Content) > 80 || !result.Items[0].Truncated || !result.Truncated {
		t.Fatalf("hard limits were not applied: %#v", result)
	}
}

func TestKeywordsAddsCJKSubtermsAndRemovesNoise(t *testing.T) {
	keywords := Keywords("请帮我恢复之前的错误原文 release-42")
	if !slices.Contains(keywords, "release-42") || !slices.Contains(keywords, "错误") || slices.Contains(keywords, "请") {
		t.Fatalf("unexpected keywords: %#v", keywords)
	}
}

func TestSearchExcludesOnlyLatestCurrentMessage(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{messages: []domain.Message{
		{ID: "msg-old", Role: "user", Content: "repeat release-42", CreatedAt: base},
		{ID: "msg-current", Role: "user", Content: "repeat release-42", CreatedAt: base.Add(time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv", Keywords: []string{"release-42"}, ExcludeLatestMessage: true,
		MaxResults: 4, MaxCharacters: 1000, MaxTokens: 250,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Reference != "message:msg-old" {
		t.Fatalf("expected only the older identical message, got %#v", result.Items)
	}
}

func TestKeywordsHaveBoundedCount(t *testing.T) {
	keywords := Keywords(strings.Repeat("上下文检索需要保持扫描成本可控", 20))
	if len(keywords) == 0 || len(keywords) > maxQueryKeywords {
		t.Fatalf("expected 1..%d bounded keywords, got %d", maxQueryKeywords, len(keywords))
	}
}

func TestSearchPropagatesStoreErrors(t *testing.T) {
	want := errors.New("store unavailable")
	if _, err := Search(testStore{messageErr: want}, Query{}); !errors.Is(err, want) {
		t.Fatalf("message error: got %v want %v", err, want)
	}
	if _, err := Search(testStore{eventErr: want}, Query{}); !errors.Is(err, want) {
		t.Fatalf("event error: got %v want %v", err, want)
	}
}

func TestSearchNormalizesKeywordsAndAppliesDefaultLimits(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{messages: []domain.Message{
		{ID: "msg-user", Role: "user", Content: "exact command release-42", CreatedAt: base},
		{ID: "msg-assistant", Role: "assistant", Content: "exact command result", CreatedAt: base.Add(time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: " conv ", Keywords: []string{" RELEASE-42 ", "release-42"},
		NeighborWindow: -1,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Reference != "message:msg-user" || result.Items[0].MatchReason != "keyword_match" {
		t.Fatalf("unexpected filtered message: %#v", result.Items)
	}
}

func TestSearchFindsEventKeywordsAndAdjacentWindow(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{events: []domain.RunEvent{
		{ID: "event-before", RunID: "run-old", Type: domain.EventToolStarted, Payload: map[string]any{"tool": "deploy"}, Timestamp: base},
		{ID: "event-match", RunID: "run-old", Type: domain.EventToolFailed, Payload: map[string]any{"error": "port 8123"}, Timestamp: base.Add(time.Minute)},
		{ID: "event-after", RunID: "run-old", Type: domain.EventRunFailed, Payload: map[string]any{"status": "failed"}, Timestamp: base.Add(2 * time.Minute)},
		{ID: "event-outside", RunID: "run-old", Type: domain.EventToolFailed, Payload: map[string]any{"error": "outside"}, Timestamp: base.Add(5 * time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv", Keywords: []string{"8123"}, NeighborWindow: 1,
		MaxResults: 5, MaxCharacters: 2000, MaxTokens: 500,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 3 || result.Items[0].Reference != "event:event-before" || result.Items[1].Reference != "event:event-match" || result.Items[2].Reference != "event:event-after" {
		t.Fatalf("unexpected event window: %#v", result.Items)
	}
	if result.Items[1].MatchReason != "keyword_match" || result.Items[0].MatchReason != "adjacent_window" {
		t.Fatalf("unexpected event match reasons: %#v", result.Items)
	}
}

func TestSearchSuppressesNoisyEventsAndDeduplicatesWindows(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{
		messages: []domain.Message{
			{ID: "msg-1", Role: "user", Content: "needle first", CreatedAt: base},
			{ID: "msg-2", Role: "assistant", Content: "needle second", CreatedAt: base},
		},
		events: []domain.RunEvent{
			{ID: "event-noisy", RunID: "old", Type: domain.EventRetrievalCompleted, Payload: map[string]any{"query": "needle"}, Timestamp: base},
			{ID: "event-useful", RunID: "old", Type: domain.EventToolCompleted, Payload: map[string]any{"result": "needle"}, Timestamp: base.Add(time.Minute)},
		},
	}
	result, err := Search(store, Query{
		ConversationID: "conv", Keywords: []string{"needle"}, NeighborWindow: 1,
		MaxResults: 8, MaxCharacters: 2000, MaxTokens: 500,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	seen := map[string]int{}
	for _, item := range result.Items {
		seen[item.Reference]++
	}
	if seen["event:event-noisy"] != 0 || seen["message:msg-1"] != 1 || seen["message:msg-2"] != 1 || seen["event:event-useful"] != 1 {
		t.Fatalf("unexpected deduplication/filter result: items=%#v counts=%#v", result.Items, seen)
	}
}

func TestSearchOrdersEqualTimestampsByReference(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	result, err := Search(testStore{messages: []domain.Message{
		{ID: "msg-b", Role: "user", Content: "needle", CreatedAt: createdAt},
		{ID: "msg-a", Role: "assistant", Content: "needle", CreatedAt: createdAt},
	}}, Query{Keywords: []string{"needle"}, MaxResults: 5, MaxCharacters: 1000, MaxTokens: 250})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].Reference != "message:msg-a" || result.Items[1].Reference != "message:msg-b" {
		t.Fatalf("unexpected stable order: %#v", result.Items)
	}
}

func TestSearchReturnsNoUnmatchedSources(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{
		messages: []domain.Message{{ID: "msg-1", Role: "user", Content: "release-42", CreatedAt: createdAt}},
		events: []domain.RunEvent{{
			ID: "event-1", RunID: "run-1", Type: domain.EventToolFailed,
			Payload: map[string]any{"error": "release-42"}, Timestamp: createdAt,
		}},
	}
	result, err := Search(store, Query{
		Keywords:   []string{"not-present"},
		MaxResults: 5, MaxCharacters: 1000, MaxTokens: 250,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("unmatched sources were included: %#v", result.Items)
	}
}

func TestApplyLimitsHandlesExhaustedAndEmptyCandidates(t *testing.T) {
	items, truncated := applyLimits([]candidate{
		{item: domain.RetrievedSessionHistory{Reference: "message:first", Content: "abcd"}},
		{item: domain.RetrievedSessionHistory{Reference: "message:second", Content: "efgh"}},
	}, Query{MaxResults: 2, MaxCharacters: 100, MaxTokens: 1})
	if len(items) != 1 || !truncated {
		t.Fatalf("token exhaustion was not enforced: items=%#v truncated=%v", items, truncated)
	}
	items, truncated = applyLimits([]candidate{{item: domain.RetrievedSessionHistory{Reference: "message:empty"}}}, Query{
		MaxResults: 1, MaxCharacters: 100, MaxTokens: 10,
	})
	if len(items) != 0 || !truncated {
		t.Fatalf("empty candidate was not skipped: items=%#v truncated=%v", items, truncated)
	}
}

func TestBoundTextAndTokenEstimateHandleEdgeCases(t *testing.T) {
	if content, truncated := boundText("value", 0, 10); content != "" || !truncated {
		t.Fatalf("zero character budget: content=%q truncated=%v", content, truncated)
	}
	if content, truncated := boundText("中文内容", 20, 20); content != "中文内容" || truncated {
		t.Fatalf("unicode content changed unexpectedly: content=%q truncated=%v", content, truncated)
	}
	if estimateTokens("") != 0 || estimateTokens("abcd") != 1 || estimateTokens("中文") != 2 {
		t.Fatalf("unexpected token estimates: empty=%d ascii=%d unicode=%d", estimateTokens(""), estimateTokens("abcd"), estimateTokens("中文"))
	}
}
