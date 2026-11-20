package prcomment

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Store is the part of the database the poster uses.
type Store interface {
	DuePullRequestComments(ctx context.Context, limit int, lease time.Duration) ([]postgres.CommentJob, error)
	PullRequestCommentSource(ctx context.Context, runID string, revision int) (postgres.CommentPreview, error)
	NewerPullRequestCommentPosted(ctx context.Context, runID string) (bool, error)
	FinishPullRequestComment(ctx context.Context, runID string, o postgres.CommentOutcome) error
}

// Retry policy of a comment that could not be posted for a passing reason
// (rate limit, GitHub unreachable): the wait doubles from FirstRetry up to
// MaxRetry, and after MaxAttempts the comment is recorded as failed.
const (
	FirstRetry  = time.Minute
	MaxRetry    = 30 * time.Minute
	MaxAttempts = 6
	// Lease is how long a taken comment is kept from a second poster.
	Lease = 2 * time.Minute
)

// Reasons the poster records besides the GitHub error kinds.
const (
	ReasonNewerRun     = "newer_run_posted"
	ReasonNoReport     = "report_missing"
	ReasonNoToken      = "github_token_missing"
	ReasonReportBroken = "report_invalid"
	ReasonStore        = "database_unavailable"
)

// Poster posts the due comments. A failed post is recorded on the comment
// row and retried; it never changes the run or its report.
type Poster struct {
	Store Store
	// GitHub returns the platform client and account login for the pinned host.
	GitHub func(context.Context, string) (*github.Client, string)
	Log    *slog.Logger
	Now    func() time.Time
}

func (p *Poster) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Poster) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Run works through the due comments once and returns how many it finished
// (posted, superseded or failed).
func (p *Poster) Run(ctx context.Context) (int, error) {
	jobs, err := p.Store.DuePullRequestComments(ctx, 10, Lease)
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	done := 0
	for _, j := range jobs {
		client, login := p.GitHub(ctx, j.Pin.Host)
		o := p.post(ctx, client, &login, j)
		if err := p.Store.FinishPullRequestComment(ctx, j.RunID, o); err != nil && !errors.Is(err, domain.ErrConflict) {
			return done, err
		}
		if o.State != postgres.CommentPending {
			done++
		}
		p.log().Info("pull request comment", "run_id", j.RunID, "state", o.State, "reason", o.Reason, "attempt", j.Attempts)
	}
	return done, nil
}

// post makes one attempt for j. login is filled in from the API when the
// stored account is unknown.
func (p *Poster) post(ctx context.Context, c *github.Client, login *string, j postgres.CommentJob) postgres.CommentOutcome {
	if newer, err := p.Store.NewerPullRequestCommentPosted(ctx, j.RunID); err != nil {
		return p.retry(j, ReasonStore)
	} else if newer {
		return postgres.CommentOutcome{State: postgres.CommentSuperseded, Reason: ReasonNewerRun}
	}
	src, err := p.Store.PullRequestCommentSource(ctx, j.RunID, j.ReportRevision)
	if err != nil {
		return p.retry(j, ReasonStore)
	}
	if src.ReportBody == nil {
		return postgres.CommentOutcome{State: postgres.CommentFailed, Reason: ReasonNoReport}
	}
	r, err := report.Unmarshal(src.ReportBody)
	if err != nil {
		return postgres.CommentOutcome{State: postgres.CommentFailed, Reason: ReasonReportBroken}
	}
	if c == nil || c.Token == "" {
		return postgres.CommentOutcome{State: postgres.CommentFailed, Reason: ReasonNoToken}
	}
	pin := j.Pin
	if *login == "" {
		u, err := c.User(ctx)
		if err != nil {
			return p.gitHubFailure(j, err)
		}
		*login = u.Login
	}
	info, err := c.PullRequest(ctx, pin.Repository, pin.Number)
	if err != nil {
		return p.gitHubFailure(j, err)
	}
	comments, err := c.Comments(ctx, pin.Repository, pin.Number)
	if err != nil {
		return p.gitHubFailure(j, err)
	}
	mine := ownComment(comments, *login, j.ProjectID)
	if mine != nil {
		if pub, ok := ParsePublication(mine.Body); ok && pub.Version > j.Version {
			return postgres.CommentOutcome{State: postgres.CommentSuperseded, Reason: ReasonNewerRun}
		}
	}
	body := Render(Input{ProjectID: j.ProjectID, RunID: j.RunID, Version: j.Version, Pin: pin, Report: &r, CurrentHead: info.Head.SHA})
	var posted github.Comment
	switch {
	case mine != nil && mine.Body == body:
		posted = *mine
	case mine != nil:
		posted, err = c.UpdateComment(ctx, pin.Repository, mine.ID, body)
	default:
		posted, err = c.CreateComment(ctx, pin.Repository, pin.Number, body)
	}
	if err != nil {
		return p.gitHubFailure(j, err)
	}
	return postgres.CommentOutcome{State: postgres.CommentPosted, CommentID: posted.ID, CommentURL: posted.HTMLURL}
}

// ownComment is the project's CODEYE comment written by login: the
// marker alone is not trusted, since anyone may paste it into a comment.
func ownComment(cs []github.Comment, login, projectID string) *github.Comment {
	marker := ProjectMarker(projectID)
	for i := range cs {
		c := &cs[i]
		if c.User != nil && strings.EqualFold(c.User.Login, login) && strings.Contains(c.Body, marker) {
			return c
		}
	}
	return nil
}

// gitHubFailure maps an API error: a refused token, a missing permission
// and a vanished pull request end the comment; a rate limit and an
// unreachable GitHub are retried.
func (p *Poster) gitHubFailure(j postgres.CommentJob, err error) postgres.CommentOutcome {
	kind := github.KindOf(err)
	switch kind {
	case github.KindUnauthorized, github.KindForbidden, github.KindNotFound, github.KindTooLarge:
		return postgres.CommentOutcome{State: postgres.CommentFailed, Reason: kind}
	}
	o := p.retry(j, kind)
	var e *github.Error
	if o.State == postgres.CommentPending && errors.As(err, &e) && e.RetryAfter > 0 {
		if at := p.now().Add(e.RetryAfter); at.After(o.RetryAt) {
			o.RetryAt = at
		}
	}
	return o
}

// retry schedules the next attempt, or fails the comment after MaxAttempts.
func (p *Poster) retry(j postgres.CommentJob, reason string) postgres.CommentOutcome {
	if j.Attempts >= MaxAttempts {
		return postgres.CommentOutcome{State: postgres.CommentFailed, Reason: reason}
	}
	wait := FirstRetry << max(j.Attempts-1, 0)
	if wait > MaxRetry || wait <= 0 {
		wait = MaxRetry
	}
	return postgres.CommentOutcome{State: postgres.CommentPending, Reason: reason, RetryAt: p.now().Add(wait)}
}

// Preview renders the comment the run would post now, without posting:
// the current head is asked from GitHub when c is given (and left out when
// GitHub cannot be reached).
func Preview(ctx context.Context, c *github.Client, src postgres.CommentPreview) (string, string, error) {
	in := Input{ProjectID: src.ProjectID, RunID: src.RunID, Version: src.Version, Pin: src.Pin}
	if src.ReportBody != nil {
		r, err := report.Unmarshal(src.ReportBody)
		if err != nil {
			return "", "", err
		}
		in.Report = &r
	}
	if c != nil {
		if info, err := c.PullRequest(ctx, src.Pin.Repository, src.Pin.Number); err == nil && domain.ValidObjectID(info.Head.SHA) {
			in.CurrentHead = info.Head.SHA
		}
	}
	return Render(in), in.CurrentHead, nil
}
