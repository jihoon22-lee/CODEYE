package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Project.Config is the canonical form of the PostgreSQL jsonb round trip of the
// submitted config, and ConfigDigest is its SHA-256. Config therefore always
// hashes to ConfigDigest, but it can differ textually from the submitted JSON
// where jsonb normalizes (for example 1e2 is stored as 100).
type Project struct {
	ID           string
	OwnerID      string
	Name         string
	Revision     int64
	Config       json.RawMessage
	ConfigDigest string
}

// normalizeConfig passes a validated config through the same jsonb conversion the
// stored column performs and re-canonicalizes the result. Values jsonb cannot hold
// (U+0000 escapes, numeric overflow) fail here as ErrInvalid instead of surfacing
// later as an insert error.
func normalizeConfig(ctx context.Context, tx pgx.Tx, config json.RawMessage) (json.RawMessage, string, error) {
	canonical, err := domain.CanonicalJSON(config)
	if err != nil {
		return nil, "", domain.ErrInvalid
	}
	var stored []byte
	if err = tx.QueryRow(ctx, "SELECT $1::text::jsonb", string(canonical)).Scan(&stored); err != nil {
		return nil, "", err
	}
	normalized, err := domain.CanonicalJSON(stored)
	if err != nil {
		return nil, "", err
	}
	return normalized, domain.Digest(normalized), nil
}

// CreateUser is a trusted provisioning operation; authentication is separate.
func (s *Store) CreateUser(ctx context.Context) (string, error) {
	id := domain.NewID()
	_, err := s.pool.Exec(ctx, "INSERT INTO users(id) VALUES($1)", id)
	return id, classify(err)
}
func (s *Store) CreateProject(ctx context.Context, actor, name string, config json.RawMessage) (Project, error) {
	if !validIDs(actor) || strings.TrimSpace(name) == "" || len(name) > 200 {
		return Project{}, domain.ErrInvalid
	}
	if _, err := domain.CanonicalJSON(config); err != nil {
		return Project{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Project, error) {
		canonical, digest, err := normalizeConfig(ctx, tx, config)
		if err != nil {
			return Project{}, err
		}
		return insertProject(ctx, tx, actor, name, canonical, digest)
	})
}

// insertProject creates the project, its manager membership and revision 1 in tx.
func insertProject(ctx context.Context, tx pgx.Tx, actor, name string, canonical json.RawMessage, digest string) (Project, error) {
	p := Project{ID: domain.NewID(), OwnerID: actor, Name: name, Revision: 1, Config: canonical, ConfigDigest: digest}
	if _, err := tx.Exec(ctx, "INSERT INTO projects(id,owner_id,name,current_revision) VALUES($1,$2,$3,1)", p.ID, actor, name); err != nil {
		return Project{}, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,'manager')", p.ID, actor); err != nil {
		return Project{}, err
	}
	_, err := tx.Exec(ctx, "INSERT INTO project_revisions(project_id,revision,config,config_digest) VALUES($1,1,$2,$3)", p.ID, string(canonical), digest)
	return p, err
}

// requireProject checks the actor's access under a project row lock so a concurrent
// membership change cannot interleave. Pure reads take FOR SHARE (readers do not
// block each other or heartbeats); anything that acts takes FOR UPDATE, which also
// serializes idempotent enqueue. A transaction must never take FOR SHARE and then
// ask for FOR UPDATE on the same project (lock upgrade deadlock).
func requireProject(ctx context.Context, tx pgx.Tx, actor, project string, manage, run bool) error {
	lock := "FOR UPDATE"
	if !manage && !run {
		lock = "FOR SHARE"
	}
	var owner string
	if err := tx.QueryRow(ctx, "SELECT owner_id::text FROM projects WHERE id=$1 AND deleted_at IS NULL "+lock, project).Scan(&owner); err != nil {
		return err
	}
	if owner == actor {
		return nil
	}
	var role domain.Role
	if err := tx.QueryRow(ctx, "SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2", project, actor).Scan(&role); err != nil {
		if err == pgx.ErrNoRows {
			// Non-members must not learn whether the project exists.
			return domain.ErrNotFound
		}
		return err
	}
	if !role.Valid() || (manage && !role.CanManage()) || (run && !role.CanRun()) {
		return domain.ErrForbidden
	}
	return nil
}
func (s *Store) GetProject(ctx context.Context, actor, id string) (Project, error) {
	if !validIDs(actor, id) {
		return Project{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Project, error) {
		if err := requireProject(ctx, tx, actor, id, false, false); err != nil {
			return Project{}, err
		}
		var p Project
		err := tx.QueryRow(ctx, `SELECT p.id::text,p.owner_id::text,p.name,p.current_revision,r.config,r.config_digest FROM projects p JOIN project_revisions r ON r.project_id=p.id AND r.revision=p.current_revision WHERE p.id=$1`, id).Scan(&p.ID, &p.OwnerID, &p.Name, &p.Revision, &p.Config, &p.ConfigDigest)
		if err != nil {
			return p, err
		}
		// jsonb reorders keys; return the canonical form that ConfigDigest describes.
		if p.Config, err = domain.CanonicalJSON(p.Config); err != nil {
			return Project{}, err
		}
		return p, nil
	})
}
func (s *Store) ReviseProject(ctx context.Context, actor, id string, expected int64, config json.RawMessage) (int64, error) {
	if !validIDs(actor, id) || expected < 1 {
		return 0, domain.ErrInvalid
	}
	if _, err := domain.CanonicalJSON(config); err != nil {
		return 0, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (int64, error) {
		if err := requireProject(ctx, tx, actor, id, true, false); err != nil {
			return 0, err
		}
		canonical, digest, err := normalizeConfig(ctx, tx, config)
		if err != nil {
			return 0, err
		}
		var revision int64
		if err := tx.QueryRow(ctx, "UPDATE projects SET current_revision=current_revision+1 WHERE id=$1 AND current_revision=$2 RETURNING current_revision", id, expected).Scan(&revision); err != nil {
			if err == pgx.ErrNoRows {
				return 0, domain.ErrConflict
			}
			return 0, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO project_revisions(project_id,revision,config,config_digest) VALUES($1,$2,$3,$4)", id, revision, string(canonical), digest)
		return revision, err
	})
}
func (s *Store) SetMember(ctx context.Context, actor, project, user string, role domain.Role) error {
	if !validIDs(actor, project, user) || (role != "" && !role.Valid()) {
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
		var err error
		if role == "" {
			_, err = tx.Exec(ctx, "DELETE FROM project_members WHERE project_id=$1 AND user_id=$2", project, user)
		} else {
			_, err = tx.Exec(ctx, "INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role", project, user, role)
		}
		return true, err
	})
	return err
}
