package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/prcomment"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// platformGitHub resolves an approved host to the installation’s GitHub client.
func (s *Server) platformGitHub(ctx context.Context, host string) (*github.Client, string) {
	provider, err := s.store.ProviderForHost(ctx, host)
	if err != nil {
		return nil, ""
	}
	return s.inst.platform().Client(provider.Host, provider.APIURL)
}

// pinPullRequest checks the project role before contacting the approved host.
func (s *Server) pinPullRequest(q *request, project string, ref domain.PullRequestRef) (domain.PullRequest, bool) {
	ctx, cancel := q.ctx()
	defer cancel()
	p, err := s.store.GetProjectSummary(ctx, q.session.UserID, project)
	if err != nil {
		s.storeError(q, err)
		return domain.PullRequest{}, false
	}
	if p.Role == string(domain.RoleViewer) {
		s.storeError(q, domain.ErrForbidden)
		return domain.PullRequest{}, false
	}
	src, _ := domain.ParseSource(p.Source)
	host, _ := domain.RepositoryHost(src.RepositoryURL)
	client, _ := s.platformGitHub(ctx, host)
	pin, err := github.PinForProject(ctx, client, p.Source, ref, p.Analysis.CommentOnPullRequest, time.Now())
	if err == nil {
		return pin, true
	}
	switch kind := github.KindOf(err); {
	case errors.Is(err, github.ErrNotGitHubProject):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "PULL_REQUEST_UNSUPPORTED", "pull requests require a GitHub API on an approved host", false)
	case kind == github.KindNotFound:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "PULL_REQUEST_NOT_FOUND", "GitHub shows no such pull request to the platform account. Grant that account access to private repositories.", false)
	case kind == github.KindUnauthorized:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "GITHUB_TOKEN_REFUSED", "GitHub refused the platform credential", false)
	case kind == github.KindForbidden:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "GITHUB_FORBIDDEN", "the platform GitHub account may not read this pull request", false)
	case kind == github.KindRateLimited:
		writeError(q.w, q.r, http.StatusServiceUnavailable, "GITHUB_RATE_LIMITED", "the GitHub API rate limit is exhausted; try again later", true)
	default:
		writeError(q.w, q.r, http.StatusBadGateway, "GITHUB_UNAVAILABLE", "the GitHub API could not be reached or answered unexpectedly", true)
	}
	return domain.PullRequest{}, false
}

// postComments works through the due pull-request comments once. A failure
// is logged and left for the next pass; it never touches
// the run or its report.
func (s *Server) postComments(ctx context.Context) {
	if s.commenter == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := s.commenter.Run(cctx); err != nil && ctx.Err() == nil {
		s.log.Warn("pull request comments failed", "error_class", errorClass(err))
	}
}

// pullRequestView is the pull_request block of GET /runs/{id}: the pinned
// pull request and the state of its comment.
type pullRequestView struct {
	Number         int                     `json:"number"`
	URL            string                  `json:"url"`
	Repository     string                  `json:"repository"`
	Title          string                  `json:"title"`
	BaseRef        string                  `json:"base_ref"`
	BaseSHA        string                  `json:"base_sha"`
	HeadRef        string                  `json:"head_ref"`
	HeadSHA        string                  `json:"head_sha"`
	HeadRepository string                  `json:"head_repository"`
	ChangedFiles   int                     `json:"changed_files"`
	ResolvedAt     time.Time               `json:"resolved_at"`
	Comment        pullRequestCommentState `json:"comment"`
}

// pullRequestCommentState: requested is the run's choice; state is
// not_requested, waiting_for_report, or the comment row's state (pending,
// posted, superseded, failed).
type pullRequestCommentState struct {
	Requested     bool       `json:"requested"`
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	Reason        *string    `json:"reason"`
	URL           *string    `json:"url"`
	UpdatedAt     *time.Time `json:"updated_at"`
}

func pullRequestViewOf(p *postgres.RunPullRequest) *pullRequestView {
	if p == nil {
		return nil
	}
	pin := p.Pin
	v := &pullRequestView{Number: pin.Number, URL: pin.URL, Repository: pin.Host + "/" + pin.Repository, Title: pin.Title, BaseRef: pin.BaseRef, BaseSHA: pin.BaseSHA,
		HeadRef: pin.HeadRef, HeadSHA: pin.HeadSHA, HeadRepository: pin.HeadRepository, ChangedFiles: pin.ChangedFiles, ResolvedAt: pin.ResolvedAt.UTC(),
		Comment: pullRequestCommentState{Requested: pin.Comment, State: "not_requested"}}
	if pin.Comment {
		v.Comment.State = "waiting_for_report"
	}
	if c := p.Comment; c != nil {
		updated := c.UpdatedAt
		v.Comment.State, v.Comment.Attempts, v.Comment.Reason, v.Comment.URL, v.Comment.UpdatedAt = c.State, c.Attempts, c.Reason, c.CommentURL, &updated
		if c.State == postgres.CommentPending {
			next := c.NextAttemptAt
			v.Comment.NextAttemptAt = &next
		}
	}
	return v
}

// previewPullRequestComment (GET /runs/{id}/pull-request-comment) renders
// the comment the run posts, or would post, without posting anything: the
// body, the analyzed and current head, and the comment state.
func (s *Server) previewPullRequestComment(q *request) {
	revision := 0
	if raw := q.r.URL.Query().Get("revision"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "revision must be a positive integer", false)
			return
		}
		revision = n
	}
	ctx, cancel := q.ctx()
	defer cancel()
	src, err := s.store.PullRequestCommentPreview(ctx, q.session.UserID, q.params[0], revision)
	if errors.Is(err, domain.ErrUnsupported) {
		writeError(q.w, q.r, http.StatusNotFound, "NOT_A_PULL_REQUEST_RUN", "this run does not analyze a pull request", false)
		return
	}
	if err != nil {
		s.storeError(q, err)
		return
	}
	if revision > 0 && src.ReportBody == nil {
		writeError(q.w, q.r, http.StatusNotFound, "NOT_FOUND", "no such report revision", false)
		return
	}
	client, _ := s.platformGitHub(ctx, src.Pin.Host)
	body, current, err := prcomment.Preview(ctx, client, src)
	if err != nil {
		s.storeError(q, err)
		return
	}
	var rev *int
	if src.ReportBody != nil {
		rev = &src.ReportRevision
	}
	var currentHead *string
	if current != "" {
		currentHead = &current
	}
	writeJSON(q.w, http.StatusOK, map[string]any{
		"run_id": src.RunID, "report_revision": rev, "format": "markdown", "body": body,
		"head_sha": src.Pin.HeadSHA, "current_head_sha": currentHead, "head_moved": current != "" && current != src.Pin.HeadSHA,
		"comment": pullRequestViewOf(&postgres.RunPullRequest{Pin: src.Pin, Comment: src.Comment}).Comment,
	})
}
