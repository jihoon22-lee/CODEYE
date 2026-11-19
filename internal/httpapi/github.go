package httpapi

import (
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
)

// gitHubCredentialFile is where `codeye up` stores the GitHub token under
// the state directory (personal.Layout.GitHubSecret).
func (p *Personal) gitHubCredentialFile() string {
	if p == nil || p.StateDir == "" {
		return ""
	}
	return filepath.Join(p.StateDir, "secrets", "github.json")
}

// gitHub is the GitHub client of personal mode: the token `codeye up` stored
// (read now, so a refreshed or removed token takes effect without a
// restart; anonymous without one) and its account login ("" when unknown).
func (p *Personal) gitHub() (*github.Client, string) {
	base := p.GitHubAPI
	if base == "" {
		base = github.APIBase()
	}
	cred, err := github.ReadCredential(p.gitHubCredentialFile())
	if err != nil || cred == nil || cred.Host != github.Host {
		return github.New(base, ""), ""
	}
	return github.New(base, cred.Token), cred.Login
}

// pinPullRequest resolves the run request's pull request for the project
// through the GitHub API (github.PinForProject). ok is false when the error
// response was written: server mode (the operator's providers have no
// token discovery), a project that is not a github.com repository, a pull
// request GitHub does not show this token, or an unreachable or
// rate-limited API.
func (s *Server) pinPullRequest(q *request, project string, ref domain.PullRequestRef) (domain.PullRequest, bool) {
	if s.personal == nil {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "PULL_REQUEST_UNSUPPORTED", "pull-request analysis is available in personal mode (codeye up); this controller has no GitHub connection", false)
		return domain.PullRequest{}, false
	}
	ctx, cancel := q.ctx()
	defer cancel()
	p, err := s.store.GetProjectSummary(ctx, q.session.UserID, project)
	if err != nil {
		s.storeError(q, err)
		return domain.PullRequest{}, false
	}
	client, _ := s.personal.gitHub()
	pin, err := github.PinForProject(ctx, client, p.Source, ref, p.Analysis.CommentOnPullRequest, time.Now())
	if err == nil {
		return pin, true
	}
	switch kind := github.KindOf(err); {
	case errors.Is(err, github.ErrNotGitHubProject):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "PULL_REQUEST_UNSUPPORTED", "pull requests can be analyzed for github.com repository projects only", false)
	case kind == github.KindNotFound:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "PULL_REQUEST_NOT_FOUND", "GitHub shows no such pull request to this installation (a private repository needs a GitHub token: gh auth login, then codeye up)", false)
	case kind == github.KindUnauthorized:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "GITHUB_TOKEN_REFUSED", "GitHub refused the stored token; sign in again (gh auth login) and run codeye up", false)
	case kind == github.KindForbidden:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "GITHUB_FORBIDDEN", "the GitHub token may not read this pull request", false)
	case kind == github.KindRateLimited:
		writeError(q.w, q.r, http.StatusServiceUnavailable, "GITHUB_RATE_LIMITED", "the GitHub API rate limit is exhausted; try again later (a GitHub token raises the limit)", true)
	default:
		writeError(q.w, q.r, http.StatusBadGateway, "GITHUB_UNAVAILABLE", "the GitHub API could not be reached or answered unexpectedly", true)
	}
	return domain.PullRequest{}, false
}
