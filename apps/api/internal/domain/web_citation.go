package domain

const WebCitationProtocolVersion = "web-citation-v1"

// WebCitation links an answer marker to model-visible search, fetch or Artifact
// read evidence. Fetch reuses the search ID; ToolEventID identifies the delivered
// evidence, not merely the discovery URL.
// It is separate from RAGCitation so web URLs never masquerade as Knowledge.
type WebCitation struct {
	SourceID    string `json:"source_id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	RunID       string `json:"run_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolEventID string `json:"tool_event_id"`
}
