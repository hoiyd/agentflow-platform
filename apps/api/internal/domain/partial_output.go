package domain

import (
	"fmt"
	"unicode/utf8"
)

const (
	MaxPartialOutputBytes    = 64 * 1024
	MaxPartialOutputEntries  = 32
	MaxPartialReasoningBytes = 16 * 1024
)

// PartialOutput is a bounded display replacement at a durable event sequence.
// It is never a Message, execution checkpoint, or model continuation input.
type PartialOutput struct {
	RunID       string `json:"run_id"`
	StageID     string `json:"stage_id,omitempty"`
	TurnID      string `json:"turn_id"`
	ModelCallID string `json:"model_call_id,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	Round       int    `json:"round"`
	Channel     string `json:"channel"`
	Role        string `json:"role,omitempty"`
	Revision    int64  `json:"revision"`
	Offset      int    `json:"offset"`
	Text        string `json:"text"`
	Status      string `json:"status"`
	Truncated   bool   `json:"truncated,omitempty"`
	Sequence    int64  `json:"sequence,omitempty"`
}

func (p PartialOutput) Validate() error {
	limit := MaxPartialOutputBytes
	if p.Channel == "reasoning" {
		limit = MaxPartialReasoningBytes
	}
	if p.RunID == "" || p.TurnID == "" || (p.Channel != "answer" && p.Channel != "reasoning") ||
		p.Revision < 1 || p.Round < 1 || p.Attempt < 0 || p.Offset != len(p.Text) || len(p.Text) > limit || !utf8.ValidString(p.Text) {
		return fmt.Errorf("invalid partial output identity, revision or bounded text")
	}
	switch p.Status {
	case "provisional", "final", "interrupted":
		return nil
	case "retracted":
		if p.Text == "" {
			return nil
		}
	}
	return fmt.Errorf("invalid partial output status %q", p.Status)
}
