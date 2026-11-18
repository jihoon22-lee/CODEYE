package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// W23 run comparison inputs and human finding reviews (migration 0008,
// docs/40). Comparison is computed by internal/history from the immutable
// report bodies loaded here; nothing derived is stored.

// FingerprintBuilderSince is the first report builder version whose findings
// carry fp2 fingerprints (report.FingerprintSince). Default baselines are
// chosen only among such revisions.
const FingerprintBuilderSince = 3

var (
	fp2Pattern         = regexp.MustCompile(`^fp2:[0-9a-f]{64}$`)
	reviewDispositions = map[string]bool{"open": true, "acknowledged": true, "accepted_risk": true, "false_positive": true, "fixed_by_user": true, "suppressed": true}
)

const maxReviewReason = 2000

// FindingReview is one append-only review version.
type FindingReview struct {
	ID, ProjectID, Fingerprint string
	Version                    int64
	Disposition, Reason        string
	PathPrefix                 *string
	ExpiresAt                  *time.Time
	ActorID                    string
	RunID                      string
	ReportRevision             int
	FindingID                  string
	CreatedAt                  time.Time
}

const reviewColumns = "id::text,project_id::text,fingerprint,version,disposition,reason,path_prefix,expires_at,actor_id::text,run_id::text,report_revision,finding_id,created_at"

func scanReview(row pgx.CollectableRow) (FindingReview, error) {
	var r FindingReview
	err := row.Scan(&r.ID, &r.ProjectID, &r.Fingerprint, &r.Version, &r.Disposition, &r.Reason, &r.PathPrefix, &r.ExpiresAt, &r.ActorID, &r.RunID, &r.ReportRevision, &r.FindingID, &r.CreatedAt)
	r.CreatedAt = r.CreatedAt.UTC()
	if r.ExpiresAt != nil {
		t := r.ExpiresAt.UTC()
		r.ExpiresAt = &t
	}
	return r, err
}

// latestReviews returns the newest version of every reviewed fingerprint
// of the project.
func latestReviews(ctx context.Context, tx pgx.Tx, project string) ([]FindingReview, error) {
	rows, err := tx.Query(ctx, "SELECT DISTINCT ON (fingerprint) "+reviewColumns+" FROM finding_reviews WHERE project_id=$1 ORDER BY fingerprint, version DESC", project)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanReview)
}

// canReview reports whether actor may set dispositions (owner, manager or
// runner). The caller already checked read access.
func canReview(ctx context.Context, tx pgx.Tx, actor, project string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND owner_id=$2)
		OR EXISTS(SELECT 1 FROM project_members WHERE project_id=$1 AND user_id=$2 AND role IN ('manager','runner'))`, project, actor).Scan(&ok)
	return ok, err
}

// ReviewRequest sets a fingerprint's disposition. Expected is the current
// version (If-Match) or 0 when no review may exist yet (If-None-Match: *).
// Verify is called with the immutable body of the named report revision and
// must confirm that FindingID exists there with Fingerprint.
type ReviewRequest struct {
	Fingerprint, Disposition, Reason string
	PathPrefix                       *string
	ExpiresAt                        *time.Time
	RunID                            string
	ReportRevision                   int
	FindingID                        string
	Expected                         int64
	Key, Digest                      string
	Verify                           func(body []byte) error
}

func (in ReviewRequest) valid() bool {
	reason := strings.TrimSpace(in.Reason)
	return fp2Pattern.MatchString(in.Fingerprint) && reviewDispositions[in.Disposition] && reason != "" && utf8.RuneCountInString(in.Reason) <= maxReviewReason &&
		utf8.ValidString(in.Reason) && !strings.ContainsRune(in.Reason, 0) && validIDs(in.RunID) && in.ReportRevision >= 1 && in.FindingID != "" && len(in.FindingID) <= 200 &&
		(in.PathPrefix == nil || domain.ValidateEvidencePath(*in.PathPrefix) == nil) && in.Expected >= 0 && in.Key != "" && len(in.Key) <= 255 &&
		hashPattern.MatchString(in.Digest) && in.Verify != nil && (in.Disposition != "open" || (in.ExpiresAt == nil && in.PathPrefix == nil))
}

// SetFindingReview appends the next review version. Only the owner, managers
// and runners may (viewers ErrForbidden, non-members ErrNotFound). The same
// actor+Idempotency-Key with the same digest returns the stored row
// (created=false); with another digest ErrIdempotencyConflict. A version
// other than Expected is ErrConflict; a past expiry or a finding that is not
// in the named revision is ErrInvalid.
func (s *Store) SetFindingReview(ctx context.Context, actor, project string, in ReviewRequest) (FindingReview, bool, error) {
	if !validIDs(actor, project) || !in.valid() {
		return FindingReview{}, false, domain.ErrInvalid
	}
	type out struct {
		rec     FindingReview
		created bool
	}
	res, err := transaction(ctx, s, func(tx pgx.Tx) (out, error) {
		if err := requireProject(ctx, tx, actor, project, false, true); err != nil {
			return out{}, err
		}
		rows, err := tx.Query(ctx, "SELECT "+reviewColumns+",request_digest FROM finding_reviews WHERE project_id=$1 AND actor_id=$2 AND idempotency_key=$3", project, actor, in.Key)
		if err != nil {
			return out{}, err
		}
		var digest string
		prior, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (FindingReview, error) {
			var r FindingReview
			err := row.Scan(&r.ID, &r.ProjectID, &r.Fingerprint, &r.Version, &r.Disposition, &r.Reason, &r.PathPrefix, &r.ExpiresAt, &r.ActorID, &r.RunID, &r.ReportRevision, &r.FindingID, &r.CreatedAt, &digest)
			return r, err
		})
		if err != nil {
			return out{}, err
		}
		if len(prior) == 1 {
			if digest != in.Digest {
				return out{}, ErrIdempotencyConflict
			}
			rec := prior[0]
			rec.CreatedAt = rec.CreatedAt.UTC()
			return out{rec: rec}, nil
		}
		var body []byte
		if err = tx.QueryRow(ctx, "SELECT body FROM reports WHERE project_id=$1 AND run_id=$2 AND revision=$3", project, in.RunID, in.ReportRevision).Scan(&body); err != nil {
			return out{}, err
		}
		if err = in.Verify(body); err != nil {
			return out{}, domain.ErrInvalid
		}
		if in.ExpiresAt != nil {
			var future bool
			if err = tx.QueryRow(ctx, "SELECT $1::timestamptz > clock_timestamp()", *in.ExpiresAt).Scan(&future); err != nil {
				return out{}, err
			}
			if !future {
				return out{}, domain.ErrInvalid
			}
		}
		var current int64
		if err = tx.QueryRow(ctx, "SELECT COALESCE(max(version),0) FROM finding_reviews WHERE project_id=$1 AND fingerprint=$2", project, in.Fingerprint).Scan(&current); err != nil {
			return out{}, err
		}
		if current != in.Expected {
			return out{}, domain.ErrConflict
		}
		rows, err = tx.Query(ctx, `INSERT INTO finding_reviews(id,project_id,fingerprint,version,disposition,reason,path_prefix,expires_at,actor_id,run_id,report_revision,finding_id,idempotency_key,request_digest)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+reviewColumns,
			domain.NewID(), project, in.Fingerprint, current+1, in.Disposition, in.Reason, in.PathPrefix, in.ExpiresAt, actor, in.RunID, in.ReportRevision, in.FindingID, in.Key, in.Digest)
		if err != nil {
			return out{}, err
		}
		rec, err := pgx.CollectExactlyOneRow(rows, scanReview)
		return out{rec: rec, created: true}, err
	})
	return res.rec, res.created, err
}

// ReviewPage is a page of reviews and whether the actor may set them.
type ReviewPage struct {
	Items      []FindingReview
	NextCursor string
	CanReview  bool
}

// ListFindingReviews returns, for a project reader, either every version of
// one fingerprint (newest first) or the latest version per fingerprint
// ordered by fingerprint with cursor pagination (cursor = last fingerprint).
func (s *Store) ListFindingReviews(ctx context.Context, actor, project, fingerprint, cursor string, limit int) (ReviewPage, error) {
	if !validIDs(actor, project) || limit < 1 || limit > 200 || (fingerprint != "" && !fp2Pattern.MatchString(fingerprint)) || (cursor != "" && !fp2Pattern.MatchString(cursor)) {
		return ReviewPage{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (ReviewPage, error) {
		var p ReviewPage
		if err := requireProject(ctx, tx, actor, project, false, false); err != nil {
			return p, err
		}
		var err error
		if p.CanReview, err = canReview(ctx, tx, actor, project); err != nil {
			return p, err
		}
		var rows pgx.Rows
		if fingerprint != "" {
			rows, err = tx.Query(ctx, "SELECT "+reviewColumns+" FROM finding_reviews WHERE project_id=$1 AND fingerprint=$2 ORDER BY version DESC", project, fingerprint)
		} else {
			rows, err = tx.Query(ctx, "SELECT * FROM (SELECT DISTINCT ON (fingerprint) "+reviewColumns+" FROM finding_reviews WHERE project_id=$1 AND fingerprint > $2 ORDER BY fingerprint, version DESC) x ORDER BY fingerprint LIMIT $3",
				project, cursor, limit+1)
		}
		if err != nil {
			return p, err
		}
		if p.Items, err = pgx.CollectRows(rows, scanReview); err != nil {
			return p, err
		}
		if fingerprint == "" && len(p.Items) > limit {
			p.Items = p.Items[:limit]
			p.NextCursor = p.Items[limit-1].Fingerprint
		}
		if p.Items == nil {
			p.Items = []FindingReview{}
		}
		return p, nil
	})
}

// ComparisonSide is one published revision with the run facts comparison
// needs: profile and scope from the run request, the source variant from the
// run's own project revision (not the project's current one).
type ComparisonSide struct {
	RunID      string
	Revision   int
	CreatedAt  time.Time
	Body       []byte
	BodySHA256 string
	Profile    string
	Scope      domain.Scope
	Source     json.RawMessage
}

// Baseline selections.
const (
	BaselineDefault  = "default"
	BaselineExplicit = "explicit"
)

// ComparisonData is everything a comparison reads, from one transaction.
type ComparisonData struct {
	ProjectID string
	Current   ComparisonSide
	Baseline  *ComparisonSide
	Selection string
	Reviews   []FindingReview
	Now       time.Time
	CanReview bool
}

// BaselineRef names a baseline revision.
type BaselineRef struct {
	RunID    string
	Revision int
}

func loadSide(ctx context.Context, tx pgx.Tx, run string, revision int) (ComparisonSide, error) {
	var c ComparisonSide
	var request, config []byte
	q := `SELECT r.id::text,p.revision,p.created_at,p.body,p.body_sha256,r.request,pr.config FROM runs r
		JOIN reports p ON p.run_id=r.id
		JOIN project_revisions pr ON pr.project_id=r.project_id AND pr.revision=r.project_revision
		WHERE r.id=$1 AND ($2=0 OR p.revision=$2) ORDER BY p.revision DESC LIMIT 1`
	if err := tx.QueryRow(ctx, q, run, revision).Scan(&c.RunID, &c.Revision, &c.CreatedAt, &c.Body, &c.BodySHA256, &request, &config); err != nil {
		return c, err
	}
	c.CreatedAt = c.CreatedAt.UTC()
	var req struct {
		Profile string       `json:"profile"`
		Scope   domain.Scope `json:"scope"`
	}
	if request == nil || json.Unmarshal(request, &req) != nil {
		return c, domain.ErrInvalid
	}
	c.Profile, c.Scope = req.Profile, req.Scope
	var cfg struct {
		Source json.RawMessage `json:"source"`
	}
	if json.Unmarshal(config, &cfg) != nil || len(cfg.Source) == 0 {
		return c, domain.ErrInvalid
	}
	src, err := domain.CanonicalJSON(cfg.Source)
	if err != nil {
		return c, domain.ErrInvalid
	}
	c.Source = src
	return c, nil
}

// defaultBaseline is the newest earlier run of the same project with the same
// source variant (the run's project-revision source: Git repository+branch or
// root+path), the same profile and scope, and a completed or partial report
// written with fp2 fingerprints; its newest such revision. nil when none.
func defaultBaseline(ctx context.Context, tx pgx.Tx, run string) (*BaselineRef, error) {
	var ref BaselineRef
	err := tx.QueryRow(ctx, `SELECT b.id::text, p.revision FROM runs c
		JOIN project_revisions cr ON cr.project_id=c.project_id AND cr.revision=c.project_revision
		JOIN runs b ON b.project_id=c.project_id AND b.id<>c.id AND (b.created_at, b.id) < (c.created_at, c.id)
		JOIN project_revisions br ON br.project_id=b.project_id AND br.revision=b.project_revision
		JOIN reports p ON p.run_id=b.id
		WHERE c.id=$1 AND br.config->'source' = cr.config->'source'
		  AND b.request->'profile' = c.request->'profile' AND b.request->'scope' = c.request->'scope'
		  AND p.status IN ('completed','partial') AND p.builder_version ~ '^[0-9]{1,6}$' AND p.builder_version::int >= $2
		ORDER BY b.created_at DESC, b.id DESC, p.revision DESC LIMIT 1`, run, FingerprintBuilderSince).Scan(&ref.RunID, &ref.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &ref, err
}

// LoadComparison reads the run's revision (0 = latest) and its baseline for
// a project reader: the explicit baselineRun (same project only, else
// ErrNotFound; the run itself is ErrInvalid; baselineRevision 0 = latest) or
// the default baseline. A run without a published report is ErrNotFound.
func (s *Store) LoadComparison(ctx context.Context, actor, runID string, revision int, baselineRun string, baselineRevision int) (ComparisonData, error) {
	if !validIDs(actor, runID) || revision < 0 || baselineRevision < 0 || (baselineRun != "" && !validIDs(baselineRun)) || (baselineRun == "" && baselineRevision != 0) {
		return ComparisonData{}, domain.ErrInvalid
	}
	if baselineRun == runID {
		return ComparisonData{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (ComparisonData, error) {
		var d ComparisonData
		r, err := authorizeRunRead(ctx, tx, actor, runID)
		if err != nil {
			return d, err
		}
		d.ProjectID = r.ProjectID
		if d.Current, err = loadSide(ctx, tx, runID, revision); err != nil {
			return d, err
		}
		ref := &BaselineRef{RunID: baselineRun, Revision: baselineRevision}
		if baselineRun != "" {
			var same bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE id=$1 AND project_id=$2)", baselineRun, r.ProjectID).Scan(&same); err != nil {
				return d, err
			}
			if !same {
				// Another project's run is not revealed, readable or not.
				return d, domain.ErrNotFound
			}
			d.Selection = BaselineExplicit
		} else if ref, err = defaultBaseline(ctx, tx, runID); err != nil {
			return d, err
		} else if ref != nil {
			d.Selection = BaselineDefault
		}
		if ref != nil {
			side, err := loadSide(ctx, tx, ref.RunID, ref.Revision)
			if err != nil {
				return d, err
			}
			d.Baseline = &side
		}
		if d.Reviews, err = latestReviews(ctx, tx, r.ProjectID); err != nil {
			return d, err
		}
		if d.CanReview, err = canReview(ctx, tx, actor, r.ProjectID); err != nil {
			return d, err
		}
		err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&d.Now)
		d.Now = d.Now.UTC()
		return d, err
	})
}

// DefaultBaseline returns the default comparison baseline of a run the actor
// may read, or nil.
func (s *Store) DefaultBaseline(ctx context.Context, actor, runID string) (*BaselineRef, error) {
	if !validIDs(actor, runID) {
		return nil, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (*BaselineRef, error) {
		if _, err := authorizeRunRead(ctx, tx, actor, runID); err != nil {
			return nil, err
		}
		return defaultBaseline(ctx, tx, runID)
	})
}
