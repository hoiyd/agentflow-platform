package webcitation

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

var marker = regexp.MustCompile(`(?i)\[W([0-9]+)\]`)

type occurrence struct {
	citation domain.WebCitation
	index    int
}

// Catalog is rebuilt from durable, successful Tool events. A repeated URL
// keeps one source ID while each occurrence retains its own event provenance.
type Catalog struct {
	byCall  map[string][]occurrence
	ordered []occurrence
}

func (c Catalog) HasCall(callID string) bool { return len(c.byCall[callID]) > 0 }

func FromEvents(events []domain.RunEvent) Catalog {
	catalog := Catalog{byCall: make(map[string][]occurrence)}
	byURL := make(map[string]string)
	events = slices.Clone(events)
	slices.SortFunc(events, func(a, b domain.RunEvent) int { return cmp.Compare(a.Sequence, b.Sequence) })
	for _, event := range events {
		if event.Type != domain.EventToolCompleted || event.ID == "" || event.Payload["tool_name"] != "web_search" || event.Payload["truncated"] == true {
			continue
		}
		callID, _ := event.Payload["tool_call_id"].(string)
		toolError, _ := event.Payload["error"].(string)
		if callID == "" || toolError != "" {
			continue
		}
		var result struct {
			Results []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"results"`
		}
		encoded, err := json.Marshal(event.Payload["result"])
		if err != nil || json.Unmarshal(encoded, &result) != nil {
			continue
		}
		for index, item := range result.Results {
			normalized, ok := NormalizeURL(item.URL)
			if !ok || strings.TrimSpace(item.Title) == "" {
				continue
			}
			id := byURL[normalized]
			if id == "" {
				id = fmt.Sprintf("W%d", len(byURL)+1)
				byURL[normalized] = id
			}
			entry := occurrence{
				citation: domain.WebCitation{SourceID: id, Title: item.Title, URL: normalized, RunID: event.RunID, ToolCallID: callID, ToolEventID: event.ID},
				index:    index,
			}
			catalog.byCall[callID] = append(catalog.byCall[callID], entry)
			catalog.ordered = append(catalog.ordered, entry)
		}
	}
	return catalog
}

// Relabel replaces per-search W numbers with Run-scoped IDs before the Tool
// result is assembled into the model request. The durable Tool event remains
// the authoritative source of titles, URLs, and provenance.
func (c Catalog) Relabel(callID string, result any) (any, error) {
	items := c.byCall[callID]
	if len(items) == 0 {
		return result, nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	results, ok := object["results"].([]any)
	if !ok {
		return nil, fmt.Errorf("web search result has no results array")
	}
	for _, item := range items {
		if item.index >= len(results) {
			return nil, fmt.Errorf("web search result changed after tracing")
		}
		row, ok := results[item.index].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("web search result row is invalid")
		}
		originalURL, _ := row["url"].(string)
		normalizedURL, valid := NormalizeURL(originalURL)
		if !valid || normalizedURL != item.citation.URL {
			return nil, fmt.Errorf("web search result differs from completed Tool event")
		}
		row["source_id"] = item.citation.SourceID
		row["url"] = item.citation.URL
	}
	return object, nil
}

// Resolve requires the Tool result's call ID in the final model manifest.
// It never promotes URLs or model-invented markers into structured citations.
func (c Catalog) Resolve(answer string, selectedCalls map[string]bool) ([]domain.WebCitation, []string) {
	available := make(map[string]domain.WebCitation)
	for _, item := range c.SelectedSources(selectedCalls) {
		available[item.SourceID] = item
	}
	resolved := make([]domain.WebCitation, 0)
	invalid := make([]string, 0)
	seen := make(map[string]bool)
	for _, match := range marker.FindAllStringSubmatch(answer, -1) {
		number, err := strconv.ParseUint(match[1], 10, 64)
		id := "W" + match[1]
		if err == nil {
			id = fmt.Sprintf("W%d", number)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if citation, ok := available[id]; ok {
			resolved = append(resolved, citation)
		} else {
			invalid = append(invalid, id)
		}
	}
	return resolved, invalid
}

func (c Catalog) SelectedSources(selectedCalls map[string]bool) []domain.WebCitation {
	sources := make([]domain.WebCitation, 0)
	seen := make(map[string]bool)
	for _, item := range c.ordered {
		if !selectedCalls[item.citation.ToolCallID] {
			continue
		}
		if !seen[item.citation.SourceID] {
			sources = append(sources, item.citation)
			seen[item.citation.SourceID] = true
		}
	}
	return sources
}

// NormalizeURL permits only absolute HTTPS URLs with a usable host. Fragments
// and default ports are omitted so duplicate search results share an ID.
func NormalizeURL(raw string) (string, bool) {
	if len(raw) > 1024 || strings.TrimSpace(raw) != raw {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsAny(host, " \t\r\n") || strings.HasSuffix(host, ".") || !strings.Contains(host, ".") {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()) {
		return "", false
	}
	port := u.Port()
	if port != "" && port != "443" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", false
		}
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}
	u.Scheme = "https"
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), true
}
