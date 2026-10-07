package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxToolProgressMessageBytes = 1024

// ToolProgressUpdate is display-only business progress, never a Tool result,
// heartbeat, execution checkpoint or model observation. Counts are optional;
// supplying both asserts known units within this phase, not overall completion.
type ToolProgressUpdate struct {
	Phase     string `json:"phase"`
	Message   string `json:"message,omitempty"`
	Completed *int64 `json:"completed,omitempty"`
	Total     *int64 `json:"total,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (p ToolProgressUpdate) Validate() error {
	if p.Phase == "" || len(p.Phase) > 64 || strings.Trim(p.Phase, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		return fmt.Errorf("invalid Tool progress phase")
	}
	if !utf8.ValidString(p.Message) || len(p.Message) > MaxToolProgressMessageBytes {
		return fmt.Errorf("invalid Tool progress message")
	}
	if (p.Completed == nil) != (p.Total == nil) || (p.Completed != nil && (*p.Completed < 0 || *p.Total <= 0 || *p.Completed > *p.Total || *p.Total > 1<<53-1)) {
		return fmt.Errorf("invalid Tool progress counts")
	}
	return nil
}

// ToolProgress is the latest committed update and the actual execution status
// derived from lifecycle events. A phase or a 100% count never changes status.
type ToolProgress struct {
	ToolProgressUpdate
	RunID      string `json:"run_id"`
	StageID    string `json:"stage_id,omitempty"`
	TurnID     string `json:"turn_id"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Status     string `json:"status"`
	Sequence   int64  `json:"sequence"`
}
