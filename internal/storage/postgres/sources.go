package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// SourceRoot is an operator-registered server-local directory under which
// projects may declare local sources. Its filesystem path is deliberately not
// part of this type: it is never returned to API clients.
type SourceRoot struct{ ID, Label string }

// Provider is an approved public HTTPS Git host. No credentials are stored.
type Provider struct{ ID, Kind, Host, APIURL string }

// AddSourceRoot registers a source root. path must be absolute; whether it
// exists is the caller's (operator CLI) check and is not a claim that any
// project under it will be readable at acquire time.
func (s *Store) AddSourceRoot(ctx context.Context, label, path string) (SourceRoot, error) {
	clean, ok := cleanLabel(label, 100)
	if !ok || path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return SourceRoot{}, domain.ErrInvalid
	}
	r := SourceRoot{ID: domain.NewID(), Label: clean}
	_, err := s.pool.Exec(ctx, "INSERT INTO source_roots(id,label,path) VALUES($1,$2,$3)", r.ID, r.Label, path)
	return r, classify(err)
}

// GrantSourceRoot lets user register projects under root. Granting twice is a no-op.
func (s *Store) GrantSourceRoot(ctx context.Context, root, user string) error {
	if !validIDs(root, user) {
		return domain.ErrInvalid
	}
	_, err := s.pool.Exec(ctx, "INSERT INTO source_root_grants(source_root_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING", root, user)
	return classify(err)
}

// ListSourceRoots returns the roots granted to actor, ordered by label.
func (s *Store) ListSourceRoots(ctx context.Context, actor string) ([]SourceRoot, error) {
	if !validIDs(actor) {
		return nil, domain.ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,r.label FROM source_roots r
		JOIN source_root_grants g ON g.source_root_id=r.id WHERE g.user_id=$1 ORDER BY r.label`, actor)
	if err != nil {
		return nil, classify(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[SourceRoot])
	if out == nil {
		out = []SourceRoot{}
	}
	return out, classify(err)
}

// AddProvider registers an approved public HTTPS GitHub (github.com) or GHES host.
func (s *Store) AddProvider(ctx context.Context, kind, host, apiURL string) (Provider, error) {
	host = strings.ToLower(host)
	if domain.ValidateHost(host) != nil || (kind != "github" && kind != "ghes") || (kind == "github" && host != "github.com") {
		return Provider{}, domain.ErrInvalid
	}
	var api any
	if apiURL != "" {
		u, err := url.Parse(apiURL)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(apiURL, " \t\r\n#?@") {
			return Provider{}, domain.ErrInvalid
		}
		api = apiURL
	}
	p := Provider{ID: domain.NewID(), Kind: kind, Host: host, APIURL: apiURL}
	_, err := s.pool.Exec(ctx, "INSERT INTO providers(id,kind,host,api_url) VALUES($1,$2,$3,$4)", p.ID, kind, host, api)
	return p, classify(err)
}

// ListProviders returns all approved providers, ordered by host.
func (s *Store) ListProviders(ctx context.Context, actor string) ([]Provider, error) {
	if !validIDs(actor) {
		return nil, domain.ErrInvalid
	}
	rows, err := s.pool.Query(ctx, "SELECT id::text,kind,host,coalesce(api_url,'') FROM providers ORDER BY host,kind")
	if err != nil {
		return nil, classify(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Provider])
	if out == nil {
		out = []Provider{}
	}
	return out, classify(err)
}

// ProjectRegistration is a user's declared project. Registration validates the
// declaration and the actor's right to use the root or provider; it never
// touches the filesystem or the network.
type ProjectRegistration struct {
	Name        string
	Source      domain.Source
	Description string
}

const maxDescription = 20000

// RegisterProject creates a project whose revision-1 config is
// {"config_version":1,"source":...,"description":...}. A root not granted to
// the actor or an unknown provider yields ErrNotFound; a repository host that
// differs from the provider host yields ErrInvalid.
func (s *Store) RegisterProject(ctx context.Context, actor string, in ProjectRegistration) (Project, error) {
	if !validIDs(actor) || len(in.Description) > maxDescription || !utf8.ValidString(in.Description) {
		return Project{}, domain.ErrInvalid
	}
	if err := in.Source.Validate(); err != nil {
		return Project{}, err
	}
	name := ""
	if strings.TrimSpace(in.Name) != "" {
		clean, ok := cleanLabel(in.Name, 200)
		if !ok {
			return Project{}, domain.ErrInvalid
		}
		name = clean
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Project, error) {
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1 FOR SHARE", actor).Scan(&enabled); err != nil {
			return Project{}, err
		}
		if !enabled {
			return Project{}, domain.ErrForbidden
		}
		derived := ""
		switch in.Source.Kind {
		case domain.SourceLocal:
			var label string
			err := tx.QueryRow(ctx, `SELECT r.label FROM source_roots r
				JOIN source_root_grants g ON g.source_root_id=r.id AND g.user_id=$2
				WHERE r.id=$1 FOR SHARE OF g`, in.Source.SourceRootID, actor).Scan(&label)
			if err != nil {
				return Project{}, err
			}
			derived = label
			if p := in.Source.RelativePath; p != "." {
				derived = p[strings.LastIndex(p, "/")+1:]
			}
		case domain.SourceGit:
			var host string
			if err := tx.QueryRow(ctx, "SELECT host FROM providers WHERE id=$1 FOR SHARE", in.Source.ProviderID).Scan(&host); err != nil {
				return Project{}, err
			}
			got, err := domain.RepositoryHost(in.Source.RepositoryURL)
			if err != nil {
				return Project{}, err
			}
			if got != host {
				return Project{}, domain.ErrInvalid
			}
			derived = domain.RepositoryName(in.Source.RepositoryURL)
		}
		if name == "" {
			if clean, ok := cleanLabel(derived, 200); ok {
				name = clean
			} else {
				name = "project"
			}
		}
		config := map[string]any{"config_version": 1, "source": in.Source}
		if strings.TrimSpace(in.Description) != "" {
			config["description"] = in.Description
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return Project{}, domain.ErrInvalid
		}
		canonical, digest, err := normalizeConfig(ctx, tx, raw)
		if err != nil {
			return Project{}, err
		}
		return insertProject(ctx, tx, actor, name, canonical, digest)
	})
}

// RunRef identifies a run and its status.
type RunRef struct {
	ID     string
	Status domain.RunStatus
}

// ProjectSummary is a project as listed to an authorized actor. Source is the
// current revision's config "source" member (nil if the config has none).
type ProjectSummary struct {
	ID       string
	Name     string
	Revision int64
	Source   json.RawMessage
	LastRun  *RunRef
}

// ProjectPage is one keyset page of ProjectSummary, newest first.
type ProjectPage struct {
	Items      []ProjectSummary
	NextCursor string
}

const projectSummarySQL = `SELECT p.id::text,p.name,p.current_revision,r.config->'source',p.created_at,lr.id::text,lr.status
	FROM projects p
	JOIN project_revisions r ON r.project_id=p.id AND r.revision=p.current_revision
	LEFT JOIN LATERAL (SELECT id,status FROM runs WHERE project_id=p.id ORDER BY created_at DESC,id DESC LIMIT 1) lr ON true
	WHERE p.deleted_at IS NULL `

type projectCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

func scanSummary(row pgx.Row) (ProjectSummary, time.Time, error) {
	var (
		p        ProjectSummary
		created  time.Time
		source   []byte
		lastID   *string
		lastStat *string
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Revision, &source, &created, &lastID, &lastStat); err != nil {
		return p, created, err
	}
	if source != nil {
		canonical, err := domain.CanonicalJSON(source)
		if err != nil {
			// A non-object source (only possible via trusted CreateProject) is shown raw.
			canonical = source
		}
		p.Source = canonical
	}
	if lastID != nil && lastStat != nil {
		p.LastRun = &RunRef{ID: *lastID, Status: domain.RunStatus(*lastStat)}
	}
	return p, created, nil
}

// GetProjectSummary returns one project the actor may read.
func (s *Store) GetProjectSummary(ctx context.Context, actor, id string) (ProjectSummary, error) {
	if !validIDs(actor, id) {
		return ProjectSummary{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (ProjectSummary, error) {
		if err := requireProject(ctx, tx, actor, id, false, false); err != nil {
			return ProjectSummary{}, err
		}
		p, _, err := scanSummary(tx.QueryRow(ctx, projectSummarySQL+"AND p.id=$1", id))
		return p, err
	})
}

// ListProjects returns the projects the actor owns or is a member of, newest
// first. cursor is the opaque NextCursor of the previous page ("" for the first).
func (s *Store) ListProjects(ctx context.Context, actor, cursor string, limit int) (ProjectPage, error) {
	if !validIDs(actor) || limit < 1 || limit > 100 {
		return ProjectPage{}, domain.ErrInvalid
	}
	var after *projectCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 200 {
			return ProjectPage{}, domain.ErrInvalid
		}
		var c projectCursor
		if domain.DecodeStrict(raw, &c) != nil || !validIDs(c.ID) || c.CreatedAt.IsZero() {
			return ProjectPage{}, domain.ErrInvalid
		}
		after = &c
	}
	var afterTime *time.Time
	var afterID *string
	if after != nil {
		afterTime, afterID = &after.CreatedAt, &after.ID
	}
	return transaction(ctx, s, func(tx pgx.Tx) (ProjectPage, error) {
		rows, err := tx.Query(ctx, projectSummarySQL+`AND (p.owner_id=$1 OR EXISTS (SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=$1))
			AND ($2::timestamptz IS NULL OR (p.created_at,p.id) < ($2::timestamptz,$3::uuid))
			ORDER BY p.created_at DESC,p.id DESC LIMIT $4`, actor, afterTime, afterID, limit+1)
		if err != nil {
			return ProjectPage{}, err
		}
		defer rows.Close()
		page := ProjectPage{Items: []ProjectSummary{}}
		var last projectCursor
		for rows.Next() {
			p, created, err := scanSummary(rows)
			if err != nil {
				return ProjectPage{}, err
			}
			if len(page.Items) == limit {
				raw, _ := json.Marshal(last)
				page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
				break
			}
			page.Items = append(page.Items, p)
			last = projectCursor{CreatedAt: created, ID: p.ID}
		}
		return page, rows.Err()
	})
}

// RunView is a run with task counts for status display. TotalTasks is 0 when
// no plan has created tasks yet.
type RunView struct {
	Run
	CompletedTasks, TotalTasks int64
}

// GetRunView returns the run and its task counts for an actor who may read it.
func (s *Store) GetRunView(ctx context.Context, actor, id string) (RunView, error) {
	if !validIDs(actor, id) {
		return RunView{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (RunView, error) {
		r, err := authorizeRunRead(ctx, tx, actor, id)
		if err != nil {
			return RunView{}, err
		}
		v := RunView{Run: r}
		err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('succeeded','failed','timed_out','cancelled','unsupported','skipped_policy','budget_exhausted')),count(*)
			FROM tasks WHERE run_id=$1`, id).Scan(&v.CompletedTasks, &v.TotalTasks)
		return v, err
	})
}
