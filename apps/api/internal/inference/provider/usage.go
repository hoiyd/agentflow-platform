package provider

import (
	"encoding/json"

	"agentflow-platform/apps/api/internal/domain"
)

// Only documented Chat Completions details are normalized. Bad optional
// details preserve authoritative totals but cannot authorize a cache discount.
func (u *Usage) UnmarshalJSON(data []byte) error {
	var wire struct {
		PromptTokens     int             `json:"prompt_tokens"`
		CompletionTokens int             `json:"completion_tokens"`
		TotalTokens      int             `json:"total_tokens"`
		Prompt           json.RawMessage `json:"prompt_tokens_details"`
		Completion       json.RawMessage `json:"completion_tokens_details"`
		Hit              json.RawMessage `json:"prompt_cache_hit_tokens"`
		Miss             json.RawMessage `json:"prompt_cache_miss_tokens"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*u = Usage{PromptTokens: wire.PromptTokens, CompletionTokens: wire.CompletionTokens, TotalTokens: wire.TotalTokens}
	b := &domain.UsageBreakdown{Source: "openai_details"}
	invalid := false
	read := func(raw json.RawMessage) *int {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		var number int
		if json.Unmarshal(raw, &number) != nil {
			invalid = true
			return nil
		}
		return &number
	}
	nested := func(raw json.RawMessage, key string) *int {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			invalid = true
			return nil
		}
		return read(fields[key])
	}
	b.CachedInputTokens = nested(wire.Prompt, "cached_tokens")
	b.ReasoningTokens = nested(wire.Completion, "reasoning_tokens")
	hit, miss := read(wire.Hit), read(wire.Miss)
	if hit != nil {
		if b.CachedInputTokens != nil && *b.CachedInputTokens != *hit {
			invalid = true
		}
		b.CachedInputTokens = hit
		b.Source = "deepseek_cache"
	}
	if miss != nil && (*miss < 0 || *miss > u.PromptTokens || hit == nil || *hit != u.PromptTokens-*miss) {
		invalid = true
	}
	if b.Validate(u.PromptTokens, u.CompletionTokens) != nil {
		invalid = true
	}
	switch {
	case invalid:
		u.Breakdown = &domain.UsageBreakdown{Source: "invalid_details"}
	case b.CachedInputTokens != nil || b.ReasoningTokens != nil:
		u.Breakdown = b
	default:
		u.Breakdown = nil
	}
	return nil
}
