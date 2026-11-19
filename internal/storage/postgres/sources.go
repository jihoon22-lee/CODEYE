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
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if _, err := tx.Exec(ctx, "INSERT INTO source_roots(id,label,path) VALUES($1,$2,$3)", r.ID, r.Label, path); err != nil {
			return false, err
		}
		// The path is the operator's own input; the audit row keeps only the label.
		return true, writeAudit(ctx, tx, principalAudit(OperatorPrincipal(), AuditSourceRootAdded).target("source_root", r.ID).with(map[string]any{"label": r.Label}))
	})
	return r, err
}

// GrantSourceRoot is GrantSourceRootBy the operator CLI.
func (s *Store) GrantSourceRoot(ctx context.Context, root, user string) error {
	return s.GrantSourceRootBy(ctx, OperatorPrincipal(), root, user)
}

// GrantSourceRootBy lets user register projects under root. by is the
// operator or an administrator. Granting twice changes nothing and writes no
// second audit row. An unknown root or user is ErrNotFound.
func (s *Store) GrantSourceRootBy(ctx context.Context, by Principal, root, user string) error {
	if !validIDs(root, user) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return false, err
		}
		if err := sourceRootAndUser(ctx, tx, root, user); err != nil {
			return false, err
		}
		tag, err := tx.Exec(ctx, "INSERT INTO source_root_grants(source_root_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING", root, user)
		if err != nil || tag.RowsAffected() == 0 {
			return false, err
		}
		return true, writeAudit(ctx, tx, principalAudit(by, AuditSourceRootGranted).target("source_root", root).with(map[string]any{"user_id": user}))
	})
	return err
}

// RevokeSourceRootGrant removes user's grant on root. Projects the user
// registered under it stay, but the claim-time re-check refuses their new
// runs. Revoking a missing grant succeeds without an audit row.
func (s *Store) RevokeSourceRootGrant(ctx context.Context, by Principal, root, user string) error {
	if !validIDs(root, user) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return false, err
		}
		if err := sourceRootAndUser(ctx, tx, root, user); err != nil {
			return false, err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM source_root_grants WHERE source_root_id=$1 AND user_id=$2", root, user)
		if err != nil || tag.RowsAffected() == 0 {
			return false, err
		}
		return true, writeAudit(ctx, tx, principalAudit(by, AuditSourceRootRevoked).target("source_root", root).with(map[string]any{"user_id": user}))
	})
	return err
}

func sourceRootAndUser(ctx context.Context, tx pgx.Tx, root, user string) error {
	var ok bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM source_roots WHERE id=$1) AND EXISTS(SELECT 1 FROM users WHERE id=$2)", root, user).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// SourceRootGrant is one user granted a source root.
type SourceRootGrant struct {
	UserID    string
	Username  *string
	GrantedAt time.Time
}

// SourceRootAccess is a root (label only, never its server path) with its grants.
type SourceRootAccess struct {
	ID, Label string
	CreatedAt time.Time
	Grants    []SourceRootGrant
}

// ListSourceRootAccess is the administrator's view of every root and who
// may use it.
func (s *Store) ListSourceRootAccess(ctx context.Context, by Principal) ([]SourceRootAccess, error) {
	return transaction(ctx, s, func(tx pgx.Tx) ([]SourceRootAccess, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT r.id::text,r.label,r.created_at,g.user_id::text,u.username,g.created_at FROM source_roots r
			LEFT JOIN source_root_grants g ON g.source_root_id=r.id LEFT JOIN users u ON u.id=g.user_id
			ORDER BY r.label,r.id,u.username NULLS LAST,g.user_id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []SourceRootAccess{}
		for rows.Next() {
			var id, label string
			var created time.Time
			var user, username *string
			var granted *time.Time
			if err := rows.Scan(&id, &label, &created, &user, &username, &granted); err != nil {
				return nil, err
			}
			if len(out) == 0 || out[len(out)-1].ID != id {
				out = append(out, SourceRootAccess{ID: id, Label: label, CreatedAt: created.UTC(), Grants: []SourceRootGrant{}})
			}
			if user != nil && granted != nil {
				last := &out[len(out)-1]
				last.Grants = append(last.Grants, SourceRootGrant{UserID: *user, Username: username, GrantedAt: granted.UTC()})
			}
		}
		return out, rows.Err()
	})
}

// ListProvidersBy is the administrator's provider list (with API URLs).
func (s *Store) ListProvidersBy(ctx context.Context, by Principal) ([]Provider, error) {
	return transaction(ctx, s, func(tx pgx.Tx) ([]Provider, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, "SELECT id::text,kind,host,coalesce(api_url,'') FROM providers ORDER BY host,kind")
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToStructByPos[Provider])
	})
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
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if _, err := tx.Exec(ctx, "INSERT INTO providers(id,kind,host,api_url) VALUES($1,$2,$3,$4)", p.ID, kind, host, api); err != nil {
			return false, err
		}
		return true, writeAudit(ctx, tx, principalAudit(OperatorPrincipal(), AuditProviderAdded).target("provider", p.ID).with(map[string]any{"kind": kind, "host": truncateAudit(host)}))
	})
	return p, err
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
	// Analysis are the project's analysis choices; nil records none (the
	// defaults of domain.DefaultAnalysisChoices for the source kind apply).
	Analysis *domain.AnalysisChoices
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
		if in.Analysis != nil {
			config["analysis"] = in.Analysis.Stored()
		}
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
	// Analysis are the current revision's analysis choices (defaults when
	// the configuration records none).
	Analysis domain.AnalysisChoices
	LastRun  *RunRef
	// HasReport is true once any run of the project has a published report.
	HasReport bool
	// Role is the reading actor's role: owner, manager, runner or viewer.
	Role string
	// Activity is the representative run's stage view. Store reads
	// always fill it; a hand-built summary has CheckedAt zero.
	Activity Activity
	// representative is the run chosen per docs/ARCHITECTURE.md#대표-실행-선택 ("" without runs).
	representative string
}

// ProjectPage is one keyset page of ProjectSummary, newest first.
type ProjectPage struct {
	Items      []ProjectSummary
	NextCursor string
}

// nonTerminalRuns are the run states that can still change; activeRuns are
// those executing or settling a cancellation (docs/ARCHITECTURE.md#대표-실행-선택 first group).
const (
	nonTerminalRuns = `('queued','preparing','running','finalizing','cancel_requested','cancelling','recovery_required')`
	activeRuns      = `('preparing','running','finalizing','cancel_requested','cancelling')`
)

// projectSummarySQL selects the project, its latest run (last_run), the
// representative run of docs/ARCHITECTURE.md#대표-실행-선택 (the best non-terminal run by group, then
// requested_at desc, id desc; else the latest run, which then is terminal),
// the count of the other non-terminal runs, the latest published report and
// the read time. Each lateral is an indexed per-project lookup.
const projectSummarySQL = `SELECT p.id::text,p.name,p.current_revision,r.config->'source',r.config->'analysis',p.created_at,lr.id::text,lr.status,
	lp.run_id::text,lp.revision,lp.created_at,COALESCE(act.id,lr.id)::text,
	(SELECT count(*) FROM runs o WHERE o.project_id=p.id AND o.status IN ` + nonTerminalRuns + `)-(CASE WHEN act.id IS NULL THEN 0 ELSE 1 END),
	statement_timestamp()
	FROM projects p
	JOIN project_revisions r ON r.project_id=p.id AND r.revision=p.current_revision
	LEFT JOIN LATERAL (SELECT id,status FROM runs WHERE project_id=p.id ORDER BY created_at DESC,id DESC LIMIT 1) lr ON true
	LEFT JOIN LATERAL (SELECT id FROM runs WHERE project_id=p.id AND status IN ` + nonTerminalRuns + `
		ORDER BY CASE WHEN status IN ` + activeRuns + ` THEN 0 WHEN status='recovery_required' THEN 1 ELSE 2 END,created_at DESC,id DESC LIMIT 1) act ON true
	LEFT JOIN LATERAL (SELECT run_id,revision,created_at FROM reports WHERE project_id=p.id ORDER BY created_at DESC,run_id DESC,revision DESC LIMIT 1) lp ON true
	WHERE p.deleted_at IS NULL `

type projectCursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

func scanSummary(row pgx.Row) (ProjectSummary, time.Time, error) {
	var (
		p          ProjectSummary
		created    time.Time
		source     []byte
		analysis   []byte
		lastID     *string
		lastStat   *string
		reportRun  *string
		reportRev  *int
		reportAt   *time.Time
		represents *string
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Revision, &source, &analysis, &created, &lastID, &lastStat,
		&reportRun, &reportRev, &reportAt, &represents, &p.Activity.OtherActiveRuns, &p.Activity.CheckedAt); err != nil {
		return p, created, err
	}
	p.Activity.CheckedAt = p.Activity.CheckedAt.UTC()
	var kind struct {
		Kind domain.SourceKind `json:"kind"`
	}
	_ = json.Unmarshal(source, &kind)
	p.Analysis = domain.AnalysisChoicesOf(analysis, kind.Kind)
	if reportRun != nil && reportRev != nil && reportAt != nil {
		p.HasReport = true
		p.Activity.LastReport = &ReportRef{RunID: *reportRun, Revision: *reportRev, PublishedAt: reportAt.UTC()}
	}
	if represents != nil {
		p.representative = *represents
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
		if err != nil {
			return p, err
		}
		items := []ProjectSummary{p}
		if err = attachRoles(ctx, tx, actor, items); err != nil {
			return p, err
		}
		err = attachActivity(ctx, tx, items)
		return items[0], err
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
		rows.Close()
		if err := rows.Err(); err != nil {
			return ProjectPage{}, err
		}
		// One statement for the stage views of the whole page (no per-project
		// round trip); only queued representatives add waiting-reason reads.
		if err := attachRoles(ctx, tx, actor, page.Items); err != nil {
			return ProjectPage{}, err
		}
		return page, attachActivity(ctx, tx, page.Items)
	})
}

// attachRoles fills each summary's Role for actor (one statement per page).
func attachRoles(ctx context.Context, tx pgx.Tx, actor string, items []ProjectSummary) error {
	ids := make([]string, len(items))
	for i, p := range items {
		ids[i] = p.ID
	}
	roles, err := callerRoles(ctx, tx, actor, ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Role = roles[items[i].ID]
	}
	return nil
}

// RunView is a run with task counts for status display. TotalTasks is 0 when
// no plan has created tasks yet.
type RunView struct {
	Run
	CompletedTasks, TotalTasks int64
	// WaitingReason is set only for a queued run: a structured reason derived
	// from nodes, grants, snapshot locations and limits (nodeproto.Waiting*),
	// or nil when a capable node with free capacity exists.
	WaitingReason *string
	// LatestReport is the newest published report revision, nil if none.
	LatestReport *ReportSummary
	// Stages is the stage view, computed like the project activity.
	Stages RunStages
	// Origin is the schedule occurrence the run was requested for; nil
	// for a manual request.
	Origin *RunOrigin
	// PullRequest is the pin and comment row of a pull-request run; nil
	// for every other run.
	PullRequest *RunPullRequest
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
		var scheduleID *string
		var scheduledFor *time.Time
		if err = tx.QueryRow(ctx, "SELECT schedule_id::text,scheduled_for FROM runs WHERE id=$1", id).Scan(&scheduleID, &scheduledFor); err != nil {
			return v, err
		}
		if scheduleID != nil && scheduledFor != nil {
			v.Origin = &RunOrigin{ScheduleID: *scheduleID, ScheduledFor: scheduledFor.UTC()}
		}
		err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('succeeded','failed','timed_out','cancelled','unsupported','skipped_policy','budget_exhausted')),count(*)
			FROM tasks WHERE run_id=$1`, id).Scan(&v.CompletedTasks, &v.TotalTasks)
		if err != nil {
			return v, err
		}
		var rep ReportSummary
		err = tx.QueryRow(ctx, "SELECT id::text,revision,status,quality_verdict,body_sha256,octet_length(body),created_at FROM reports WHERE run_id=$1 ORDER BY revision DESC LIMIT 1", id).
			Scan(&rep.ID, &rep.Revision, &rep.Status, &rep.QualityVerdict, &rep.BodySHA256, &rep.SizeBytes, &rep.CreatedAt)
		switch {
		case err == nil:
			rep.CreatedAt = rep.CreatedAt.UTC()
			v.LatestReport = &rep
		case err != pgx.ErrNoRows:
			return v, err
		}
		stages, err := loadRunStages(ctx, tx, []string{id})
		if err != nil {
			return v, err
		}
		v.Stages = stages[id]
		if v.PullRequest, err = loadRunPullRequest(ctx, tx, id); err != nil {
			return v, err
		}
		if r.Status != domain.RunQueued {
			return v, nil
		}
		v.WaitingReason, err = waitingReason(ctx, tx, id)
		return v, err
	})
}
