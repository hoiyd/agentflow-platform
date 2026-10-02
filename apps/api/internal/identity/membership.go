// Package identity owns OIDC login, revocable sessions and Workspace membership.
// It does not authorize individual objects or provide account administration.
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// SuperUserID owns trusted-local Workspaces. It is not an OIDC role or login;
// AUTH_MODE=local still means a trusted, unauthenticated deployment.
const SuperUserID = "super"

type User struct {
	ID      string `json:"id"`
	Issuer  string `json:"-"`
	Subject string `json:"subject"`
	Name    string `json:"name"`
}

type LoginAttempt struct {
	StateHash, Nonce, Verifier string
	ExpiresAt                  time.Time
}

type Store interface {
	UpsertIdentity(context.Context, User) error
	ProvisionPersonalWorkspace(context.Context, string) error
	ListMemberships(context.Context, string) ([]string, error)
	IsMember(context.Context, string, string) (bool, error)
	DefaultWorkspace(context.Context, string) (string, error)
	CreateSession(context.Context, string, string, time.Time) error
	SessionUser(context.Context, string) (User, error)
	DeleteSession(context.Context, string) error
	SaveLoginAttempt(context.Context, LoginAttempt) error
	ConsumeLoginAttempt(context.Context, string) (LoginAttempt, error)
}

// UserID is stable across logins/restarts, scoped by issuer plus opaque subject.
// Email and display name are neither identity keys nor membership grants.
func UserID(issuer, subject string) string {
	hash := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "user_" + hex.EncodeToString(hash[:])
}
