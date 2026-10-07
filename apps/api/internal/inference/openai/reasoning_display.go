package openai

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
)

const maxReasoningDisplayBytes = 16 * 1024

// Redact a protocol copy before capping, never individual provider deltas.
func reasoningDisplayText(raw, apiKey string) (string, bool) {
	return sanitizedDisplayText(raw, apiKey, maxReasoningDisplayBytes)
}

func sanitizedDisplayText(raw, apiKey string, limit int) (string, bool) {
	if apiKey != "" {
		raw = strings.ReplaceAll(raw, apiKey, "[REDACTED]")
	}
	text, _ := redaction.Text(raw)
	// An unfinished private-key block is sensitive even without its closing marker.
	if start := strings.Index(text, "-----BEGIN "); start >= 0 {
		text = text[:start] + "[REDACTED]"
	}
	if len(text) <= limit {
		return text, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

// A live replacement contains only closed tokens. Keep enough raw lookahead for
// a configured key spanning whitespace; unfinished PEM blocks are removed by
// the same whole-prefix filter as the final copy. No transport state is mutated.
// ponytail: unbroken text waits for whitespace/completion; no speculative tokenizer.
func reasoningDisplayPrefix(raw, apiKey string) (string, bool) {
	return sanitizedDisplayPrefix(raw, apiKey, maxReasoningDisplayBytes)
}

func sanitizedDisplayPrefix(raw, apiKey string, limit int) (string, bool) {
	// Replace complete configured keys before choosing a boundary: otherwise the
	// lookahead window itself could cut a multi-word key into a publishable prefix.
	if apiKey != "" {
		raw = strings.ReplaceAll(raw, apiKey, "[REDACTED]")
	}
	end := len(raw)
	if apiKey != "" {
		end -= len(apiKey) - 1
	}
	if end <= 0 {
		return "", false
	}
	end = strings.LastIndexFunc(raw[:end], unicode.IsSpace)
	if end < 0 {
		return "", false
	}
	return sanitizedDisplayText(raw[:end], "", limit)
}

func sendReasoningDisplay(ctx context.Context, events chan<- StreamEvent, display provider.ReasoningDisplay) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- StreamEvent{Type: "reasoning", Reasoning: &display}:
		return true
	}
}
