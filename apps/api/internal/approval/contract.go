// Package approval owns exact-call decisions. It does not grant Tool scopes or
// decide whether an external effect succeeded; Policy and Effect Journal do that.
package approval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

var (
	Pending     = errors.New("Tool execution is waiting for an exact-call approval")
	ErrConflict = failure.New(failure.Definition{Message: "Tool approval is stale, expired, unavailable or already claimed", Info: failure.Info{Code: "tool_approval_conflict", Source: "tool_approval", Category: failure.CategoryValidation}})
	ErrInvalid  = failure.New(failure.Definition{Message: "invalid Tool approval", Info: failure.Info{Code: "tool_approval_invalid", Source: "tool_approval", Category: failure.CategoryValidation}})
)

const Lifetime = 30 * time.Minute
const MaxCheckpointBytes = 256 * 1024

func Hash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// BindingHash excludes only mutable decision state, not the reviewed identity,
// parameters, revisions, owner or expiry. Claim consumption checks it again.
func BindingHash(record domain.ToolApproval) string {
	record.Status, record.DecidedAt = "", nil
	return Hash(record)
}

type Mutation struct {
	domain.ToolApproval
	Checkpoint json.RawMessage   `json:"-"`
	Claim      string            `json:"-"`
	Events     []domain.RunEvent `json:"-"`
}

type Store interface {
	SuspendToolApproval(context.Context, domain.ToolApproval, json.RawMessage) (Mutation, error)
	ListToolApprovals(context.Context, string, string, string) ([]domain.ToolApproval, error)
	DecideToolApproval(context.Context, string, string, string, string, domain.ToolApprovalDecision) (Mutation, error)
	ClaimToolApproval(context.Context, string, string, string, string) (Mutation, error)
	ConsumeToolApproval(context.Context, string, string, string, string, string) (Mutation, error)
}
