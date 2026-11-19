package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// RoleOwner is the caller role reported for a project's owner: an implicit
// manager that membership changes cannot remove or demote.
const RoleOwner = "owner"

// Member is one person with access to a project. Role is owner, manager,
// runner or viewer.
type Member struct {
	UserID      string
	Username    *string
	DisplayName *string
	Role        string
	Disabled    bool
}

// ListMembers returns the owner and the members of a project, for a project
// manager (owner or manager role).
func (s *Store) ListMembers(ctx context.Context, actor, project string) ([]Member, error) {
	if !validIDs(actor, project) {
		return nil, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) ([]Member, error) {
		if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT id,username,display_name,role,disabled FROM (
			SELECT u.id::text AS id,u.username,u.display_name,'owner' AS role,u.disabled_at IS NOT NULL AS disabled FROM projects p JOIN users u ON u.id=p.owner_id WHERE p.id=$1
			UNION ALL
			SELECT u.id::text,u.username,u.display_name,m.role,u.disabled_at IS NOT NULL FROM project_members m JOIN users u ON u.id=m.user_id JOIN projects p ON p.id=m.project_id
			WHERE m.project_id=$1 AND m.user_id<>p.owner_id) x
			ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'manager' THEN 1 WHEN 'runner' THEN 2 ELSE 3 END, username NULLS LAST, id`, project)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToStructByPos[Member])
	})
}

// PutMember gives user a role on a project (adding or changing the
// membership). Only a project manager may; the owner's role cannot change
// (ErrConflict); an unknown or disabled user is ErrNotFound. The change and
// its audit row are one transaction.
func (s *Store) PutMember(ctx context.Context, actor, project, user string, role domain.Role) (Member, error) {
	if !validIDs(actor, project, user) || !role.Valid() {
		return Member{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Member, error) {
		return putMember(ctx, tx, actor, project, "id=$1", user, role)
	})
}

// AddMemberByUsername is PutMember naming the user by username, so a
// manager who is not an administrator can add a colleague.
func (s *Store) AddMemberByUsername(ctx context.Context, actor, project, username string, role domain.Role) (Member, error) {
	name, err := account.NormalizeUsername(username)
	if err != nil || !validIDs(actor, project) || !role.Valid() {
		return Member{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Member, error) {
		return putMember(ctx, tx, actor, project, "username=$1", name, role)
	})
}

// putMember: lock order project (FOR UPDATE, by requireProject) -> user
// (FOR SHARE) -> membership row.
func putMember(ctx context.Context, tx pgx.Tx, actor, project, where string, arg any, role domain.Role) (Member, error) {
	if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
		return Member{}, err
	}
	m := Member{Role: string(role)}
	var enabled bool
	if err := tx.QueryRow(ctx, "SELECT id::text,username,display_name,disabled_at IS NULL FROM users WHERE "+where+" FOR SHARE", arg).
		Scan(&m.UserID, &m.Username, &m.DisplayName, &enabled); err != nil {
		return Member{}, err
	}
	if !enabled {
		return Member{}, domain.ErrNotFound
	}
	var owner string
	if err := tx.QueryRow(ctx, "SELECT owner_id::text FROM projects WHERE id=$1", project).Scan(&owner); err != nil {
		return Member{}, err
	}
	if owner == m.UserID {
		return Member{}, domain.ErrConflict
	}
	var previous *string
	err := tx.QueryRow(ctx, "SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2", project, m.UserID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Member{}, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role", project, m.UserID, role); err != nil {
		return Member{}, err
	}
	var prev any
	if previous != nil {
		prev = *previous
	}
	detail := map[string]any{"user_id": m.UserID, "role": string(role), "previous_role": prev}
	return m, writeAudit(ctx, tx, userAudit(ctx, actor, AuditMemberSet).target("user", m.UserID).in(project).with(detail))
}

// RemoveMember ends a user's membership. Removing a non-member succeeds
// without an audit row; the owner cannot be removed (ErrConflict).
func (s *Store) RemoveMember(ctx context.Context, actor, project, user string) error {
	if !validIDs(actor, project, user) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
			return false, err
		}
		var owner string
		if err := tx.QueryRow(ctx, "SELECT owner_id::text FROM projects WHERE id=$1", project).Scan(&owner); err != nil {
			return false, err
		}
		if owner == user {
			return false, domain.ErrConflict
		}
		var previous string
		err := tx.QueryRow(ctx, "DELETE FROM project_members WHERE project_id=$1 AND user_id=$2 RETURNING role", project, user).Scan(&previous)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		detail := map[string]any{"user_id": user, "previous_role": previous}
		return true, writeAudit(ctx, tx, userAudit(ctx, actor, AuditMemberRemoved).target("user", user).in(project).with(detail))
	})
	return err
}

// SetMember is PutMember, or RemoveMember for role "".
func (s *Store) SetMember(ctx context.Context, actor, project, user string, role domain.Role) error {
	if role == "" {
		return s.RemoveMember(ctx, actor, project, user)
	}
	_, err := s.PutMember(ctx, actor, project, user, role)
	return err
}

// callerRoles returns the actor's role (owner, manager, runner, viewer) for
// each listed project the actor can read.
func callerRoles(ctx context.Context, tx pgx.Tx, actor string, projects []string) (map[string]string, error) {
	out := map[string]string{}
	if len(projects) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT p.id::text,CASE WHEN p.owner_id=$1 THEN 'owner' ELSE m.role END FROM projects p
		LEFT JOIN project_members m ON m.project_id=p.id AND m.user_id=$1 WHERE p.id=ANY($2::uuid[])`, actor, projects)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var role *string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		if role != nil {
			out[id] = *role
		}
	}
	return out, rows.Err()
}
