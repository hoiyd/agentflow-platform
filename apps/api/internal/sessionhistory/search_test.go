package sessionhistory

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type testStore struct {
	messages []domain.Message
	events   []domain.RunEvent
}

func (s testStore) ListMessages(string) ([]domain.Message, error)               { return s.messages, nil }
func (s testStore) ListConversationRunEvents(string) ([]domain.RunEvent, error) { return s.events, nil }

func TestSearchFiltersMessagesAndAddsAdjacentWindow(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{messages: []domain.Message{
		{ID: "msg-1", Role: "user", Content: "Set deployment ID to release-2026-08.", CreatedAt: base},
		{ID: "msg-2", Role: "assistant", Content: "Recorded the deployment ID.", CreatedAt: base.Add(time.Minute)},
		{ID: "msg-3", Role: "user", Content: "Unrelated recent question.", CreatedAt: base.Add(2 * time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv", Keywords: []string{"release-2026-08"}, Roles: []string{"user"},
		From: ptrTime(base.Add(-time.Second)), To: ptrTime(base.Add(time.Minute + time.Second)),
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

func TestSearchSupportsEventIDTypeAndExcludesCurrentRun(t *testing.T) {
	base := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := testStore{events: []domain.RunEvent{
		{ID: "event-1", RunID: "old-run", Type: domain.EventToolFailed, Payload: map[string]any{"error": "connection refused on port 8123"}, Timestamp: base},
		{ID: "event-2", RunID: "current-run", Type: domain.EventToolFailed, Payload: map[string]any{"error": "connection refused on port 9000"}, Timestamp: base.Add(time.Minute)},
	}}
	result, err := Search(store, Query{
		ConversationID: "conv", EventIDs: []string{"event-1"}, EventTypes: []domain.RunEventType{domain.EventToolFailed},
		Keywords: []string{"8123"}, ExcludeRunID: "current-run", MaxResults: 2, MaxCharacters: 1000, MaxTokens: 250,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Reference != "event:event-1" || result.Items[0].EventType != domain.EventToolFailed {
		t.Fatalf("unexpected event result: %#v", result.Items)
	}
	if result.Items[0].MatchReason != "source_id_match" || !strings.Contains(result.Items[0].Content, "8123") {
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
	if !containsFold(keywords, "release-42") || !containsFold(keywords, "错误") || containsFold(keywords, "请") {
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

func ptrTime(value time.Time) *time.Time { return &value }
