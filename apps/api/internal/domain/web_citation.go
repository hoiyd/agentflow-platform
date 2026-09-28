package domain

const WebCitationProtocolVersion = "web-citation-v1"

// WebCitation links an answer marker to a completed web_search Tool event.
// It is separate from RAGCitation so web URLs never masquerade as Knowledge.
type WebCitation struct {
	SourceID    string `json:"source_id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	RunID       string `json:"run_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolEventID string `json:"tool_event_id"`
}
