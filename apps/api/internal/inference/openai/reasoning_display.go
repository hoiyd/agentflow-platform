package openai

import (
	"context"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
)

const maxReasoningDisplayBytes = 16 * 1024

// Redact the complete bounded protocol copy before slicing or publishing. Filtering
// individual deltas can leak a credential split across chunks. Failed calls never
// release incomplete text (including unterminated credential/private-key fragments).
func reasoningDisplayText(raw, apiKey string) (string, bool) {
	if apiKey != "" {
		raw = strings.ReplaceAll(raw, apiKey, "[REDACTED]")
	}
	text, _ := redaction.Text(raw)
	// An unfinished private-key block is sensitive even without its closing marker.
	if start := strings.Index(text, "-----BEGIN "); start >= 0 {
		text = text[:start] + "[REDACTED]"
	}
	if len(text) <= maxReasoningDisplayBytes {
		return text, false
	}
	end := maxReasoningDisplayBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

func sendReasoningDisplay(ctx context.Context, events chan<- StreamEvent, display provider.ReasoningDisplay) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- StreamEvent{Type: "reasoning", Reasoning: &display}:
		return true
	}
}
