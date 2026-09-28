package webcitation

import (
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestCatalogUsesRunScopedIDsAndOnlySelectedSuccessfulResults(t *testing.T) {
	events := []domain.RunEvent{
		webEvent(2, "event-2", "call-2", false, "https://example.com/a#section", "Same page"),
		webEvent(1, "event-1", "call-1", false, "https://EXAMPLE.com:443/a", "First page"),
		webEvent(3, "event-3", "call-3", false, "https://example.org/b", "Second page"),
		webEvent(4, "event-4", "call-4", true, "https://example.net/c", "Truncated"),
		{ID: "event-5", Sequence: 5, Type: domain.EventToolFailed, Payload: map[string]any{"tool_name": "web_search", "tool_call_id": "call-5", "result": map[string]any{"results": []any{map[string]any{"url": "https://example.io/", "title": "Failed"}}}}},
	}
	catalog := FromEvents(events)
	if !catalog.HasCall("call-1") || catalog.HasCall("call-4") || catalog.HasCall("call-5") {
		t.Fatalf("wrong eligible calls: %#v", catalog.byCall)
	}
	first, _ := catalog.Relabel("call-2", map[string]any{"results": []any{map[string]any{"source_id": "W1", "url": "https://example.com/a#section"}}})
	row := first.(map[string]any)["results"].([]any)[0].(map[string]any)
	if row["source_id"] != "W1" || row["url"] != "https://example.com/a" {
		t.Fatalf("relabel did not use canonical source: %#v", row)
	}
	second, _ := catalog.Relabel("call-3", map[string]any{"results": []any{map[string]any{"source_id": "W1", "url": "https://example.org/b"}}})
	if second.(map[string]any)["results"].([]any)[0].(map[string]any)["source_id"] != "W2" {
		t.Fatalf("second call reused local W1: %#v", second)
	}
	if _, err := catalog.Relabel("call-3", map[string]any{"results": []any{map[string]any{"url": "https://example.net/forged"}}}); err == nil {
		t.Fatal("relabel accepted a result that differs from its Tool event")
	}
	selected := map[string]bool{"call-2": true, "call-3": true}
	resolved, invalid := catalog.Resolve("Repeat [W1] [w1], valid [W2], absent [W3], fake [W9], zero [W0], URL https://example.net/c", selected)
	if len(resolved) != 2 || !reflect.DeepEqual([]string{resolved[0].SourceID, resolved[1].SourceID}, []string{"W1", "W2"}) || !reflect.DeepEqual(invalid, []string{"W3", "W9", "W0"}) {
		t.Fatalf("resolved=%#v invalid=%#v", resolved, invalid)
	}
	if resolved[0].ToolCallID != "call-2" || resolved[0].ToolEventID != "event-2" || resolved[0].URL != "https://example.com/a" {
		t.Fatalf("citation provenance does not point to selected Tool event: %#v", resolved[0])
	}
	resolved, invalid = catalog.Resolve("[W1] [W2]", map[string]bool{"call-1": true})
	if len(resolved) != 1 || resolved[0].ToolEventID != "event-1" || !reflect.DeepEqual(invalid, []string{"W2"}) {
		t.Fatalf("unselected source escaped catalog: resolved=%#v invalid=%#v", resolved, invalid)
	}
}

func TestNormalizeURLRejectsUnsafeAndMergesDuplicates(t *testing.T) {
	first, ok := NormalizeURL("https://Example.COM:443/path#fragment")
	if !ok || first != "https://example.com/path" {
		t.Fatalf("normalized=%q ok=%t", first, ok)
	}
	for _, raw := range []string{"http://example.com/", "javascript:alert(1)", "https://user@example.com/", "https://localhost/", "https://127.0.0.1/", "https://example.com:bad/", " https://example.com/"} {
		if _, ok := NormalizeURL(raw); ok {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
}

func webEvent(sequence int64, id, callID string, truncated bool, url, title string) domain.RunEvent {
	return domain.RunEvent{ID: id, RunID: "run-1", Sequence: sequence, Type: domain.EventToolCompleted, Payload: map[string]any{
		"tool_name": "web_search", "tool_call_id": callID, "truncated": truncated,
		"result": map[string]any{"results": []any{map[string]any{"source_id": "W1", "url": url, "title": title}}},
	}}
}
