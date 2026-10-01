// Package identity owns OIDC login, revocable sessions and Workspace membership.
// It does not authorize individual objects or provide account administration.
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

type User struct {
	ID      string `json:"id"`
	Issuer  string `json:"-"`
	Subject string `json:"subject"`
	Name    string `json:"name"`
}

type Member struct {
	Subject    string   `json:"subject"`
	Workspaces []string `json:"workspaces"`
}

type LoginAttempt struct {
	StateHash, Nonce, Verifier string
	ExpiresAt                  time.Time
}

type Store interface {
	ImportMemberships(context.Context, string, []Member) error
	UpsertIdentity(context.Context, User) error
	ProvisionPersonalWorkspace(context.Context, string) error
	ListMemberships(context.Context, string) ([]string, error)
	IsMember(context.Context, string, string) (bool, error)
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

// A personal namespace is stable per verified issuer/subject identity, not email.
func PersonalWorkspaceID(userID string) string { return "personal_" + userID }

func loadMembers(path string) ([]Member, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read AUTH_MEMBERSHIP_PATH")
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, errors.New("membership configuration must be a regular JSON file under 64 KiB")
	}
	var config *struct {
		Members []Member `json:"members"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || config == nil {
		return nil, errors.New("invalid membership configuration")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("membership configuration must contain one JSON object")
	}
	seen := map[string]bool{}
	for i := range config.Members {
		member := &config.Members[i]
		if member.Subject == "" || member.Subject != strings.TrimSpace(member.Subject) || len(member.Subject) > 255 || seen[member.Subject] {
			return nil, errors.New("members require unique nonempty OIDC subjects")
		}
		seen[member.Subject] = true
		workspaces := map[string]bool{}
		for j, workspace := range member.Workspaces {
			if strings.TrimSpace(workspace) == "" || len(workspace) > 128 {
				return nil, errors.New("invalid membership workspace")
			}
			workspace = domain.NormalizeWorkspaceID(workspace)
			if workspaces[workspace] {
				return nil, errors.New("duplicate membership workspace")
			}
			workspaces[workspace] = true
			member.Workspaces[j] = workspace
		}
	}
	return config.Members, nil
}
