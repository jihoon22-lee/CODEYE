package github

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// ErrNotGitHubProject indicates a project without an approved GitHub API.
var ErrNotGitHubProject = errors.New("pull_request_unsupported_source")

// PinForProject is the one resolution path of a pull-request run (the web
// API's run creation and `codeye up URL/pull/N` both use it): the project's
// source must have an approved GitHub API; the pull request is resolved with
// c and the run's comment choice is ref.Comment, or remembered (the
// project's comment_on_pull_request) when the request leaves it out.
func PinForProject(ctx context.Context, c *Client, source json.RawMessage, ref domain.PullRequestRef, remembered bool, now time.Time) (domain.PullRequest, error) {
	src, err := domain.ParseSource(source)
	if err != nil || src.Kind != domain.SourceGit {
		return domain.PullRequest{}, ErrNotGitHubProject
	}
	host, err := domain.RepositoryHost(src.RepositoryURL)
	if err != nil || c == nil {
		return domain.PullRequest{}, ErrNotGitHubProject
	}
	p, err := Resolve(ctx, c, host, domain.RepositoryPath(src.RepositoryURL), ref.Number, now)
	if err != nil {
		return domain.PullRequest{}, err
	}
	p.Comment = remembered
	if ref.Comment != nil {
		p.Comment = *ref.Comment
	}
	return p, nil
}
