package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/identity"
)

type WorkspaceError struct {
	Message  string
	Conflict bool
}

func (e *WorkspaceError) Error() string { return e.Message }
func (e *WorkspaceError) FailureInfo() failure.Info {
	info := failure.Info{Code: "workspace_invalid", Source: "workspace", Category: failure.CategoryValidation}
	if e.Conflict {
		info.Code, info.Category = "workspace_conflict", failure.CategoryExecution
	}
	return info
}

const workspaceColumns = `w.id,w.owner_user_id,w.name,w.description,w.status,
	EXISTS(SELECT 1 FROM auth_personal_workspaces p WHERE p.user_id=w.owner_user_id AND p.workspace_id=w.id),
	w.created_at,w.updated_at,w.deleted_at`

func scanWorkspace(row scanner) (domain.Workspace, error) {
	var item domain.Workspace
	err := row.Scan(&item.ID, &item.OwnerUserID, &item.Name, &item.Description, &item.Status, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound("workspace")
	}
	return item, err
}

func (s *PostgresStore) ListWorkspaces(ctx context.Context, owner string) ([]domain.Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w
	 JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id
	 WHERE w.owner_user_id=$1 AND w.deleted_at IS NULL ORDER BY w.id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Workspace{}
	for rows.Next() {
		item, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetWorkspace(ctx context.Context, owner, id string) (domain.Workspace, error) {
	if !validWorkspaceID(id) {
		return domain.Workspace{}, ErrNotFound("workspace")
	}
	return scanWorkspace(s.db.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w
	 JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id
	 WHERE w.owner_user_id=$1 AND w.id=$2 AND w.deleted_at IS NULL`, owner, id))
}

func validWorkspaceID(id string) bool {
	value, err := strconv.ParseInt(id, 10, 64)
	return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
}

func (s *PostgresStore) DefaultWorkspace(ctx context.Context, owner string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT w.id FROM auth_personal_workspaces p
	 JOIN workspaces w ON w.owner_user_id=p.user_id AND w.id=p.workspace_id
	 JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id
	 WHERE p.user_id=$1 AND w.deleted_at IS NULL AND w.status='active'`, owner).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound("default workspace")
	}
	return id, err
}

func validateWorkspaceFields(name, description string) (string, string, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if !utf8.ValidString(name) || !utf8.ValidString(description) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 || utf8.RuneCountInString(description) > 2000 {
		return "", "", &WorkspaceError{Message: "Workspace name must contain 1-80 characters; description cannot exceed 2000 characters"}
	}
	return name, description, nil
}

// All lifecycle/default changes lock the owner before the Workspace. Concurrent
// operations cannot both remove the last active space or leave a stale default.
func (s *PostgresStore) CreateWorkspace(ctx context.Context, owner, name, description string) (domain.Workspace, error) {
	name, description, err := validateWorkspaceFields(name, description)
	if err != nil {
		return domain.Workspace{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer tx.Rollback()
	if err = lockWorkspaceOwner(ctx, tx, owner); err != nil {
		return domain.Workspace{}, err
	}
	item, err := createOwnedWorkspace(ctx, tx, owner, name, description)
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func lockWorkspaceOwner(ctx context.Context, tx *sql.Tx, owner string) error {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE id=$1 FOR UPDATE`, owner).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound("user")
	}
	return err
}

func createOwnedWorkspace(ctx context.Context, tx *sql.Tx, owner, name, description string) (domain.Workspace, error) {
	var id string
	if err := tx.QueryRowContext(ctx, `INSERT INTO workspaces(owner_user_id,name,description) VALUES($1,$2,$3) RETURNING id::text`, owner, name, description).Scan(&id); err != nil {
		return domain.Workspace{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_memberships(user_id,workspace_id) VALUES($1,$2)`, owner, id); err != nil {
		return domain.Workspace{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_personal_workspaces(user_id,workspace_id) VALUES($1,$2)
	 ON CONFLICT(user_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id
	 WHERE NOT EXISTS(SELECT 1 FROM workspaces w JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id
	 WHERE w.id=auth_personal_workspaces.workspace_id AND w.owner_user_id=$1 AND w.status='active' AND w.deleted_at IS NULL)`, owner, id); err != nil {
		return domain.Workspace{}, err
	}
	return scanWorkspace(tx.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w WHERE w.id=$1`, id))
}

// auth_personal_workspaces is also the durable onboarding marker. Its reference
// now selects the owner's default; an existing marker never grants access again.
func (s *PostgresStore) ProvisionPersonalWorkspace(ctx context.Context, owner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockWorkspaceOwner(ctx, tx, owner); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth_personal_workspaces WHERE user_id=$1)`, owner).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err = createOwnedWorkspace(ctx, tx, owner, "Personal workspace", ""); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) UpdateWorkspace(ctx context.Context, owner, id string, change domain.WorkspaceUpdate, remove bool) (domain.Workspace, error) {
	if !validWorkspaceID(id) {
		return domain.Workspace{}, ErrNotFound("workspace")
	}
	if change.ReplacementWorkspaceID != "" && !validWorkspaceID(change.ReplacementWorkspaceID) {
		return domain.Workspace{}, &WorkspaceError{Message: "Select another owned active Workspace as the replacement default", Conflict: true}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer tx.Rollback()
	if err = lockWorkspaceOwner(ctx, tx, owner); err != nil {
		return domain.Workspace{}, err
	}
	item, err := scanWorkspace(tx.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w
	 JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id
	 WHERE w.owner_user_id=$1 AND w.id=$2 AND w.deleted_at IS NULL FOR UPDATE OF w`, owner, id))
	if err != nil {
		return item, err
	}
	if change.Name != nil {
		item.Name = *change.Name
	}
	if change.Description != nil {
		item.Description = *change.Description
	}
	item.Name, item.Description, err = validateWorkspaceFields(item.Name, item.Description)
	if err != nil {
		return item, err
	}
	status := item.Status
	if change.Status != nil {
		status = *change.Status
	}
	if status != "active" && status != "archived" {
		return item, &WorkspaceError{Message: "Workspace status must be active or archived"}
	}
	closing := remove || status == "archived" && item.Status != "archived"
	if closing {
		var busy bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE workspace_id=$1 AND status IN('queued','running','waiting_for_user','failed_recoverable','canceling'))
		 OR EXISTS(SELECT 1 FROM tool_effects e JOIN runs r ON r.id=e.run_id WHERE r.workspace_id=$1 AND e.status IN('executing','needs_reconciliation','reconciling'))`, id).Scan(&busy)
		if err != nil {
			return item, err
		}
		if busy {
			return item, &WorkspaceError{Message: "Finish or cancel unfinished Runs and reconcile uncertain tool effects before closing this Workspace", Conflict: true}
		}
		var other bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces w JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id WHERE w.owner_user_id=$1 AND w.id<>$2 AND w.status='active' AND w.deleted_at IS NULL)`, owner, id).Scan(&other); err != nil {
			return item, err
		}
		if !other {
			return item, &WorkspaceError{Message: "Create another active Workspace before closing the last active Workspace", Conflict: true}
		}
		if item.IsDefault {
			if change.ReplacementWorkspaceID == "" {
				return item, &WorkspaceError{Message: "Select another owned active Workspace as the replacement default", Conflict: true}
			}
			var replacement string
			if err = tx.QueryRowContext(ctx, `SELECT w.id FROM workspaces w JOIN auth_memberships m ON m.user_id=w.owner_user_id AND m.workspace_id=w.id WHERE w.owner_user_id=$1 AND w.id=$2 AND w.id<>$3 AND w.status='active' AND w.deleted_at IS NULL FOR UPDATE OF w`, owner, change.ReplacementWorkspaceID, id).Scan(&replacement); errors.Is(err, sql.ErrNoRows) {
				return item, &WorkspaceError{Message: "Select another owned active Workspace as the replacement default", Conflict: true}
			} else if err != nil {
				return item, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE auth_personal_workspaces SET workspace_id=$2 WHERE user_id=$1`, owner, replacement); err != nil {
				return item, err
			}
		}
	}
	if change.MakeDefault {
		if remove || status != "active" {
			return item, &WorkspaceError{Message: "Only an active Workspace can be the default", Conflict: true}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE auth_personal_workspaces SET workspace_id=$2 WHERE user_id=$1`, owner, id); err != nil {
			return item, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET name=$2,description=$3,status=$4,deleted_at=CASE WHEN $5 THEN NOW() ELSE deleted_at END,updated_at=NOW() WHERE id=$1`, id, item.Name, item.Description, status, remove); err != nil {
		return item, err
	}
	item, err = scanWorkspace(tx.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces w WHERE w.id=$1`, id))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s *PostgresStore) InitializeWorkspaceLifecycle(ctx context.Context) error {
	plan, err := s.PreviewWorkspaceMigration(ctx, nil)
	if err != nil {
		return err
	}
	if len(plan) > 0 {
		return errors.New("Workspace ownership migration required; run scripts/workspace-migrate.sh to preview and apply explicit owner mappings before starting the API")
	}
	if err = s.ApplyWorkspaceMigration(ctx, nil); err != nil {
		return err
	}
	if err = s.UpsertIdentity(ctx, identity.User{ID: identity.SuperUserID, Issuer: "agentflow:local", Subject: "local", Name: "Super"}); err != nil {
		return err
	}
	return s.ProvisionPersonalWorkspace(ctx, identity.SuperUserID)
}
