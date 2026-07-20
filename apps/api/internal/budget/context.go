package budget

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func NewOperationID(prefix string) string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err == nil {
		return strings.TrimSpace(prefix) + "_" + hex.EncodeToString(bytes)
	}
	return fmt.Sprintf("%s_%d", strings.TrimSpace(prefix), time.Now().UTC().UnixNano())
}

func WithController(ctx context.Context, controller Controller) context.Context {
	if controller == nil {
		return ctx
	}
	return context.WithValue(ctx, controllerKey{}, controller)
}

func FromContext(ctx context.Context) Controller {
	if ctx == nil {
		return nil
	}
	controller, _ := ctx.Value(controllerKey{}).(Controller)
	return controller
}

func WithOperation(ctx context.Context, operationID string) context.Context {
	return context.WithValue(ctx, operationKey{}, strings.TrimSpace(operationID))
}

func OperationFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(operationKey{}).(string)
	return strings.TrimSpace(value)
}

func WithPurpose(ctx context.Context, purpose domain.RunUsagePurpose) context.Context {
	return context.WithValue(ctx, purposeKey{}, normalizePurpose(purpose))
}

func PurposeFromContext(ctx context.Context) domain.RunUsagePurpose {
	if ctx == nil {
		return domain.UsagePurposePrimary
	}
	purpose, _ := ctx.Value(purposeKey{}).(domain.RunUsagePurpose)
	return normalizePurpose(purpose)
}

type Scope struct {
	StageID string
	TurnID  string
}

func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

func ScopeFromContext(ctx context.Context) Scope {
	if ctx == nil {
		return Scope{}
	}
	scope, _ := ctx.Value(scopeKey{}).(Scope)
	return scope
}

type controllerKey struct{}
type operationKey struct{}
type purposeKey struct{}
type scopeKey struct{}
