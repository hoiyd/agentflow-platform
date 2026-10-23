package webcitation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

// FetchResult keeps the search identity separate from the exact text delivered
// by a fetch. A partial extraction is never represented as a complete page.
type FetchResult struct {
	SourceID        string `json:"source_id"`
	Title           string `json:"title"`
	URL             string `json:"url"`
	FinalURL        string `json:"final_url"`
	Content         string `json:"content"`
	ContentHash     string `json:"content_hash"`
	ContentComplete bool   `json:"content_complete"`
	Truncated       bool   `json:"truncated"`
	MediaType       string `json:"media_type"`
	HTTPStatus      int    `json:"http_status"`
	Status          string `json:"status"`
	TrustBoundary   string `json:"trust_boundary"`
}

// Lookup returns discovery provenance only, never a model-supplied URL or an ID
// created by fetch/Artifact content. The caller supplies events for one Run.
func (c Catalog) Lookup(sourceID string) (domain.WebCitation, bool) {
	for _, item := range c.ordered {
		if item.citation.SourceID == sourceID {
			return item.citation, true
		}
	}
	return domain.WebCitation{}, false
}

func (c *Catalog) addFetchEvidence(events []domain.RunEvent) {
	type fetchedArtifact struct {
		source    domain.WebCitation
		reference domain.ToolArtifactReference
		callID    string
	}
	artifacts := map[string]fetchedArtifact{}
	for _, event := range events {
		if event.Type != domain.EventToolCompleted || event.ID == "" || event.Payload["error"] != nil && event.Payload["error"] != "" {
			continue
		}
		callID, _ := event.Payload["tool_call_id"].(string)
		if callID == "" {
			continue
		}
		var page FetchResult
		switch event.Payload["tool_name"] {
		case "web_fetch":
			if event.Payload["truncated"] == true {
				var args struct {
					SourceID string `json:"source_id"`
				}
				raw, _ := event.Payload["arguments"].(string)
				var reference domain.ToolArtifactReference
				if json.Unmarshal([]byte(raw), &args) == nil && decode(event.Payload["artifact"], &reference) && reference.ID != "" {
					if source, ok := c.Lookup(args.SourceID); ok && source.RunID == event.RunID {
						artifacts[reference.ID] = fetchedArtifact{source: source, reference: reference, callID: callID}
					}
				}
				continue
			}
			if !decode(event.Payload["result"], &page) {
				continue
			}
		case "artifact_read":
			if event.Payload["truncated"] == true {
				continue
			}
			var read domain.ToolArtifactRead
			if !decode(event.Payload["result"], &read) || !read.Complete || read.Offset != 0 || read.Artifact.RunID != event.RunID || read.Artifact.ToolName != "web_fetch" || len(read.Content) != read.Artifact.StoredByteSize || contentHash(read.Content) != read.Artifact.ContentHash {
				continue
			}
			fetched, ok := artifacts[read.Artifact.ID]
			if !ok || read.Artifact.ContentHash != fetched.reference.ContentHash || read.Artifact.StoredByteSize != fetched.reference.ByteSize || read.Artifact.ToolCallID != fetched.callID || json.Unmarshal([]byte(read.Content), &page) != nil || page.SourceID != fetched.source.SourceID {
				continue
			}
		default:
			continue
		}
		source, ok := c.Lookup(page.SourceID)
		if !ok || source.RunID != event.RunID || page.URL != source.URL || strings.TrimSpace(page.Content) == "" || page.ContentHash != contentHash(page.Content) || page.TrustBoundary != "untrusted_external_content" || page.Status != "read" && page.Status != "partial" {
			continue
		}
		source.ToolCallID, source.ToolEventID = callID, event.ID
		entry := occurrence{citation: source}
		c.byCall[callID] = append(c.byCall[callID], entry)
		c.ordered = append(c.ordered, entry)
	}
}

func decode(value any, target any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && json.Unmarshal(encoded, target) == nil
}

func contentHash(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}
