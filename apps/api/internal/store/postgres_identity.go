package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/identity"
)

func (s *PostgresStore) UpsertIdentity(ctx context.Context, user identity.User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_users (id,issuer,subject,name) VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name`, user.ID, user.Issuer, user.Subject, user.Name)
	return err
}

// The unique user row is both the personal Workspace record and the durable
// onboarding marker. Membership removal never deletes this marker; login cannot
// recreate revoked access. INSERT conflict handling serializes concurrent logins.
func (s *PostgresStore) ProvisionPersonalWorkspace(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var workspace string
	err = tx.QueryRowContext(ctx, `INSERT INTO auth_personal_workspaces (user_id,workspace_id) VALUES ($1,$2) ON CONFLICT (user_id) DO NOTHING RETURNING workspace_id`, userID, identity.PersonalWorkspaceID(userID)).Scan(&workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_memberships (user_id,workspace_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, userID, workspace); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListMemberships(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM auth_memberships WHERE user_id=$1 ORDER BY workspace_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) IsMember(ctx context.Context, userID, workspaceID string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth_memberships WHERE user_id=$1 AND workspace_id=$2)`, userID, workspaceID).Scan(&found)
	return found, err
}

func (s *PostgresStore) CreateSession(ctx context.Context, hash, userID string, expires time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_sessions (token_hash,user_id,expires_at) VALUES ($1,$2,$3)`, hash, userID, expires)
	return err
}

func (s *PostgresStore) SessionUser(ctx context.Context, hash string) (identity.User, error) {
	var user identity.User
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.issuer,u.subject,u.name FROM auth_sessions s JOIN auth_users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>NOW()`, hash).Scan(&user.ID, &user.Issuer, &user.Subject, &user.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, identity.ErrUnauthenticated
	}
	return user, err
}

func (s *PostgresStore) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=$1`, hash)
	return err
}

func (s *PostgresStore) SaveLoginAttempt(ctx context.Context, attempt identity.LoginAttempt) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_login_attempts WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_login_attempts (state_hash,nonce,verifier,expires_at) VALUES ($1,$2,$3,$4)`, attempt.StateHash, attempt.Nonce, attempt.Verifier, attempt.ExpiresAt)
	return err
}

// DELETE RETURNING consumes the transaction once, including across API restarts.
func (s *PostgresStore) ConsumeLoginAttempt(ctx context.Context, hash string) (identity.LoginAttempt, error) {
	var attempt identity.LoginAttempt
	err := s.db.QueryRowContext(ctx, `DELETE FROM auth_login_attempts WHERE state_hash=$1 AND expires_at>NOW() RETURNING state_hash,nonce,verifier,expires_at`, hash).Scan(&attempt.StateHash, &attempt.Nonce, &attempt.Verifier, &attempt.ExpiresAt)
	return attempt, err
}

var _ identity.Store = (*PostgresStore)(nil)
