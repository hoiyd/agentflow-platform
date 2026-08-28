package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type DelegationStatus string
type DelegationBlockReason string

const (
	DelegationCreated   DelegationStatus = "created"
	DelegationRunning   DelegationStatus = "running"
	DelegationBlocked   DelegationStatus = "blocked"
	DelegationCompleted DelegationStatus = "completed"
	DelegationFailed    DelegationStatus = "failed"
	DelegationCanceled  DelegationStatus = "canceled"
)

const DelegationBlockReasonChildRecoveryRequired DelegationBlockReason = "child_recovery_required"

// RunDelegation is the durable relationship between a parent orchestration
// stage and an isolated child Run. OutputRef points to the child's own trace;
// Summary is the only child output admitted back into the parent context.
type RunDelegation struct {
	ID               string                `json:"id"`
	WorkspaceID      string                `json:"workspace_id"`
	ConversationID   string                `json:"conversation_id"`
	ParentRunID      string                `json:"parent_run_id"`
	ParentTurnID     string                `json:"parent_turn_id"`
	ParentStageID    string                `json:"parent_stage_id,omitempty"`
	ChildRunID       string                `json:"child_run_id"`
	AgentID          string                `json:"agent_id"`
	Depth            int                   `json:"depth"`
	Status           DelegationStatus      `json:"status"`
	BlockReason      DelegationBlockReason `json:"block_reason,omitempty"`
	Task             string                `json:"task"`
	Summary          string                `json:"summary,omitempty"`
	OutputRef        string                `json:"output_ref,omitempty"`
	OutputHash       string                `json:"output_hash,omitempty"`
	OutputBytes      int                   `json:"output_bytes,omitempty"`
	SummaryTruncated bool                  `json:"summary_truncated,omitempty"`
	TimeoutMS        int64                 `json:"timeout_ms"`
	Error            string                `json:"error,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

type ChildRunRequest struct {
	Delegation      RunDelegation
	RuntimeSnapshot RuntimeSnapshot
}

type DelegationResult struct {
	Status           DelegationStatus
	BlockReason      DelegationBlockReason
	Summary          string
	OutputRef        string
	OutputHash       string
	OutputBytes      int
	SummaryTruncated bool
	Error            string
}

// CompletedDelegationResult creates the bounded parent-visible result while
// retaining a stable reference and digest for the full child-owned output.
func CompletedDelegationResult(childRunID string, step CollaborationStep, maxChars int) DelegationResult {
	output := step.Output
	runes := []rune(strings.TrimSpace(output))
	truncated := len(runes) > maxChars
	if truncated {
		marker := []rune("\n...[child output truncated; see trace]")
		limit := maxChars - len(marker)
		if limit < 0 {
			limit = 0
		}
		runes = append(runes[:limit], marker...)
	}
	digest := sha256.Sum256([]byte(output))
	return DelegationResult{
		Status: DelegationCompleted, Summary: string(runes),
		OutputRef:  fmt.Sprintf("run://%s/stages/%s", childRunID, step.ID),
		OutputHash: hex.EncodeToString(digest[:]), OutputBytes: len([]byte(output)),
		SummaryTruncated: truncated,
	}
}
