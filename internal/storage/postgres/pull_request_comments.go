package postgres

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Pull-request comment states (pull_request_comments.state).
const (
	CommentPending    = "pending"
	CommentPosted     = "posted"
	CommentSuperseded = "superseded"
	CommentFailed     = "failed"
)

// PullRequestComment is the comment row of a pull-request run that asked
// for one: queued when its report is published, then posted, superseded by
// a newer run of the same pull request, or failed.
type PullRequestComment struct {
	RunID, ProjectID string
	State            string
	Attempts         int
	NextAttemptAt    time.Time
	Reason           *string
	CommentID        *int64
	CommentURL       *string
	ReportRevision   *int
	UpdatedAt        time.Time
}

// CommentJob is one due comment: the run's pin, its publication version
// (the run's creation time in microseconds, so a later run of the pull
// request always has a higher version) and the report revision to render.
type CommentJob struct {
	RunID, ProjectID string
	Pin              domain.PullRequest
	Version          int64
	Attempts         int
	ReportRevision   int
}

// CommentOutcome is how one posting attempt ended. State pending with
// RetryAt schedules another attempt.
type CommentOutcome struct {
	State      string
	Reason     string
	CommentID  int64
	CommentURL string
	RetryAt    time.Time
}

var commentReason = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// queuePullRequestComment adds (or re-arms, for a later report revision) the
// comment row of a run whose pin asked for a comment. Other runs are left
// alone, and a superseded row stays superseded.
func queuePullRequestComment(ctx context.Context, tx pgx.Tx, runID string, revision int) error {
	_, err := tx.Exec(ctx, `INSERT INTO pull_request_comments(run_id,project_id,repository,number,head_sha,state,report_revision)
		SELECT r.id,r.project_id,lower((r.pull_request->>'host')||'/'||(r.pull_request->>'repository')),(r.pull_request->>'number')::int,r.pull_request->>'head_sha','pending',$2
		FROM runs r WHERE r.id=$1 AND r.pull_request IS NOT NULL AND (r.pull_request->>'comment')::boolean
		ON CONFLICT (run_id) DO UPDATE SET state='pending',report_revision=EXCLUDED.report_revision,attempts=0,reason=NULL,next_attempt_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE pull_request_comments.state<>'superseded'`, runID, revision)
	return err
}

// DuePullRequestComments takes up to limit pending comments whose time has
// come, oldest first, and leases them for lease (their next attempt moves
// that far ahead and the attempt is counted) so a second poster does not
// take them meanwhile.
func (s *Store) DuePullRequestComments(ctx context.Context, limit int, lease time.Duration) ([]CommentJob, error) {
	if limit < 1 || lease <= 0 {
		return nil, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) ([]CommentJob, error) {
		rows, err := tx.Query(ctx, `UPDATE pull_request_comments c SET attempts=c.attempts+1,next_attempt_at=clock_timestamp()+$2*interval '1 millisecond',updated_at=clock_timestamp()
			FROM runs r WHERE r.id=c.run_id AND c.run_id IN (SELECT run_id FROM pull_request_comments WHERE state='pending' AND next_attempt_at<=clock_timestamp() ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
			RETURNING c.run_id::text,c.project_id::text,r.pull_request,r.created_at,c.attempts,c.report_revision`, limit, lease.Milliseconds())
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []CommentJob
		for rows.Next() {
			var j CommentJob
			var raw []byte
			var revision *int
			var created time.Time
			if err := rows.Scan(&j.RunID, &j.ProjectID, &raw, &created, &j.Attempts, &revision); err != nil {
				return nil, err
			}
			j.Version = created.UnixMicro()
			if j.Pin, err = domain.ParsePullRequest(raw); err != nil {
				return nil, err
			}
			if revision != nil {
				j.ReportRevision = *revision
			}
			out = append(out, j)
		}
		return out, rows.Err()
	})
}

// NewerPullRequestCommentPosted reports whether a run of the same pull
// request created after runID already posted its comment: then runID's
// result is older than what the pull request shows and must not replace it.
func (s *Store) NewerPullRequestCommentPosted(ctx context.Context, runID string) (bool, error) {
	if !validIDs(runID) {
		return false, domain.ErrInvalid
	}
	var newer bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pull_request_comments c JOIN pull_request_comments o ON o.repository=c.repository AND o.number=c.number AND o.run_id<>c.run_id
		JOIN runs rc ON rc.id=c.run_id JOIN runs ro ON ro.id=o.run_id
		WHERE c.run_id=$1 AND o.state='posted' AND ro.created_at>rc.created_at)`, runID).Scan(&newer)
	return newer, err
}

// FinishPullRequestComment records one posting attempt. Pending with a
// RetryAt is the next try; posted, superseded and failed end the row.
func (s *Store) FinishPullRequestComment(ctx context.Context, runID string, o CommentOutcome) error {
	if !validIDs(runID) || (o.Reason != "" && !commentReason.MatchString(o.Reason)) {
		return domain.ErrInvalid
	}
	var reason, url *string
	var id *int64
	if o.Reason != "" {
		reason = &o.Reason
	}
	if o.CommentURL != "" {
		url = &o.CommentURL
	}
	if o.CommentID > 0 {
		id = &o.CommentID
	}
	var tag interface{ RowsAffected() int64 }
	var err error
	switch o.State {
	case CommentPending:
		if o.RetryAt.IsZero() {
			return domain.ErrInvalid
		}
		tag, err = s.pool.Exec(ctx, "UPDATE pull_request_comments SET next_attempt_at=$2,reason=$3,updated_at=clock_timestamp() WHERE run_id=$1 AND state='pending'", runID, o.RetryAt, reason)
	case CommentPosted:
		if id == nil {
			return domain.ErrInvalid
		}
		tag, err = s.pool.Exec(ctx, "UPDATE pull_request_comments SET state='posted',comment_id=$2,comment_url=$3,reason=NULL,updated_at=clock_timestamp() WHERE run_id=$1 AND state='pending'", runID, id, url)
	case CommentSuperseded, CommentFailed:
		tag, err = s.pool.Exec(ctx, "UPDATE pull_request_comments SET state=$2,reason=$3,updated_at=clock_timestamp() WHERE run_id=$1 AND state='pending'", runID, o.State, reason)
	default:
		return domain.ErrInvalid
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

// RunPullRequest is a pull-request run's pin and its comment row (nil
// before the report is published or when the run posts no comment).
type RunPullRequest struct {
	Pin     domain.PullRequest
	Comment *PullRequestComment
}

// loadRunPullRequest reads runID's pin and comment row; nil for a run
// without a pull request.
func loadRunPullRequest(ctx context.Context, tx pgx.Tx, runID string) (*RunPullRequest, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT pull_request FROM runs WHERE id=$1", runID).Scan(&raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	pin, err := domain.ParsePullRequest(raw)
	if err != nil {
		return nil, err
	}
	out := &RunPullRequest{Pin: pin}
	var c PullRequestComment
	err = tx.QueryRow(ctx, `SELECT run_id::text,project_id::text,state,attempts,next_attempt_at,reason,comment_id,comment_url,report_revision,updated_at FROM pull_request_comments WHERE run_id=$1`, runID).
		Scan(&c.RunID, &c.ProjectID, &c.State, &c.Attempts, &c.NextAttemptAt, &c.Reason, &c.CommentID, &c.CommentURL, &c.ReportRevision, &c.UpdatedAt)
	switch {
	case err == nil:
		c.NextAttemptAt, c.UpdatedAt = c.NextAttemptAt.UTC(), c.UpdatedAt.UTC()
		out.Comment = &c
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	return out, nil
}

// CommentPreview is what a comment preview renders from: the run's pin,
// its publication version and the newest published report (nil Body before
// one exists).
type CommentPreview struct {
	RunID, ProjectID string
	// RequesterID is the user who started the run (whose token reads the
	// current head).
	RequesterID    string
	Pin            domain.PullRequest
	Version        int64
	Comment        *PullRequestComment
	ReportRevision int
	ReportBody     []byte
}

// PullRequestCommentPreview loads a pull-request run for an actor who may
// read it: ErrNotFound for a run the actor cannot see, ErrUnsupported for a
// run without a pull request. revision 0 is the newest report.
func (s *Store) PullRequestCommentPreview(ctx context.Context, actor, runID string, revision int) (CommentPreview, error) {
	if !validIDs(actor, runID) || revision < 0 {
		return CommentPreview{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (CommentPreview, error) {
		r, err := authorizeRunRead(ctx, tx, actor, runID)
		if err != nil {
			return CommentPreview{}, err
		}
		return commentPreview(ctx, tx, r, revision)
	})
}

// PullRequestCommentSource is PullRequestCommentPreview for the controller's
// poster, which acts for no user.
func (s *Store) PullRequestCommentSource(ctx context.Context, runID string, revision int) (CommentPreview, error) {
	if !validIDs(runID) || revision < 0 {
		return CommentPreview{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (CommentPreview, error) {
		r, err := readRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1", runID))
		if err != nil {
			return CommentPreview{}, err
		}
		return commentPreview(ctx, tx, r, revision)
	})
}

func commentPreview(ctx context.Context, tx pgx.Tx, r Run, revision int) (CommentPreview, error) {
	pr, err := loadRunPullRequest(ctx, tx, r.ID)
	if err != nil {
		return CommentPreview{}, err
	}
	if pr == nil {
		return CommentPreview{}, domain.ErrUnsupported
	}
	out := CommentPreview{RunID: r.ID, ProjectID: r.ProjectID, RequesterID: r.ActorID, Pin: pr.Pin, Version: r.CreatedAt.UnixMicro(), Comment: pr.Comment}
	q := "SELECT revision,body FROM reports WHERE run_id=$1 ORDER BY revision DESC LIMIT 1"
	args := []any{r.ID}
	if revision > 0 {
		q = "SELECT revision,body FROM reports WHERE run_id=$1 AND revision=$2"
		args = append(args, revision)
	}
	err = tx.QueryRow(ctx, q, args...).Scan(&out.ReportRevision, &out.ReportBody)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CommentPreview{}, err
	}
	return out, nil
}
