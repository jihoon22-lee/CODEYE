package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/prcomment"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// `codeye up https://github.com/OWNER/REPO/pull/N` (docs/GITHUB.md): the
// pull request is resolved through its host’s GitHub API with the platform
// credential, pinned on a new run and analyzed at its head commit.
// --comment turns the result comment on (and remembers it for the project),
// --comment=false off; --dry-run-comment posts nothing, waits for the report
// and prints the comment the run would post.

// optionalBool is a boolean flag that knows whether it was given.
type optionalBool struct{ set, value bool }

func (b *optionalBool) String() string { return strconv.FormatBool(b.value) }
func (b *optionalBool) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	b.set, b.value = true, v
	return nil
}
func (b *optionalBool) IsBoolFlag() bool { return true }

// pullOptions are the pull-request flags of `codeye up`.
type pullOptions struct {
	comment       optionalBool
	dryRunComment bool
	// wait bounds how long --dry-run-comment waits for the report.
	wait time.Duration
}

// pullRequestTarget recognizes a pull-request address of an approved Git
// host.
func pullRequestTarget(cfg *instance.Config, target string) (*upTarget, bool, error) {
	repo, number, ok := domain.SplitPullRequestURL(target)
	if !ok {
		return nil, false, nil
	}
	t, err := gitTarget(cfg, repo)
	if err != nil {
		return nil, true, err
	}
	t.pullRequest = number
	return t, true, nil
}

// queuePullRequest resolves t's pull request for the project, remembers an
// explicit --comment choice and queues the run.
func queuePullRequest(ctx context.Context, up *running, platform github.Platform, projectID string, t *upTarget, o pullOptions, out io.Writer) (string, error) {
	p, err := up.store.GetProjectSummary(ctx, up.user, projectID)
	if err != nil {
		return "", fmt.Errorf("cannot read the project: %w", err)
	}
	remembered := p.Analysis.CommentOnPullRequest
	if o.comment.set && !o.dryRunComment && o.comment.value != remembered {
		next := p.Analysis
		next.CommentOnPullRequest = o.comment.value
		if _, err := up.store.SetProjectAnalysis(ctx, up.user, projectID, next); err != nil {
			return "", fmt.Errorf("cannot remember the comment choice: %w", err)
		}
		remembered = o.comment.value
		state := "on"
		if !remembered {
			state = "off"
		}
		fmt.Fprintf(out, "pull-request comments are now %s for %q (codeye up --comment=%t changes it)\n", state, p.Name, !remembered)
	}
	ref := domain.PullRequestRef{Number: t.pullRequest}
	if o.dryRunComment {
		off := false
		ref.Comment = &off
	} else if o.comment.set {
		ref.Comment = &o.comment.value
	}
	provider, err := up.store.ProviderForHost(ctx, t.host)
	if err != nil {
		return "", err
	}
	client, _ := platform.Client(provider.Host, provider.APIURL)
	pin, err := github.PinForProject(ctx, client, p.Source, ref, remembered, time.Now())
	if err != nil {
		return "", pullRequestError(err, t)
	}
	body := map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}, "pull_request": ref}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	run, err := up.store.EnqueuePullRequest(ctx, up.user, projectID, "up-"+domain.NewID(), raw, pin)
	if err != nil {
		return "", fmt.Errorf("cannot queue the analysis: %w", err)
	}
	comment := "off"
	switch {
	case o.dryRunComment:
		comment = "dry run (nothing is posted)"
	case pin.Comment:
		comment = "on (one CODEYE comment, updated by later runs)"
	}
	fork := ""
	if pin.HeadRepository != "" && pin.HeadRepository != pin.Repository {
		fork = " from " + pin.HeadRepository
	}
	fmt.Fprintf(out, "queued an analysis of pull request #%d of %s (%s%s, head %s, base %s %s, %d changed files; comment: %s) (run %s)\n",
		pin.Number, pin.Repository, pin.HeadRef, fork, shortSHA(pin.HeadSHA), pin.BaseRef, shortSHA(pin.BaseSHA), pin.ChangedFiles, comment, run.ID)
	if pin.FilesTruncated {
		fmt.Fprintf(out, "GitHub listed %d of the %d changed files; the rest are reviewed like the rest of the repository\n", len(pin.Files), pin.ChangedFiles)
	}
	return run.ID, nil
}

// pullRequestError explains a resolution failure.
func pullRequestError(err error, t *upTarget) error {
	if errors.Is(err, github.ErrNotGitHubProject) {
		return fmt.Errorf("the GitHub API for %s is unavailable on this installation", t.host)
	}
	where := fmt.Sprintf("pull request #%d of %s", t.pullRequest, domain.RepositoryPath(t.repoURL))
	switch github.KindOf(err) {
	case github.KindNotFound:
		return fmt.Errorf("GitHub shows no %s to this installation (grant the platform GitHub account access for a private repository)", where)
	case github.KindUnauthorized:
		return errors.New("GitHub refused the platform credential")
	case github.KindForbidden:
		return fmt.Errorf("the platform GitHub account may not read %s", where)
	case github.KindRateLimited:
		return errors.New("the GitHub API rate limit is exhausted; try again later")
	default:
		return fmt.Errorf("cannot reach the GitHub API to resolve %s: %v", where, err)
	}
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// printCommentPreview waits until the run has a report (or ends without
// one) and prints the comment it would post. Nothing is posted.
func printCommentPreview(ctx context.Context, store *postgres.Store, platform github.Platform, user, runID string, wait time.Duration, out, stderr io.Writer) int {
	fmt.Fprintf(out, "waiting for the report of run %s to preview its pull-request comment (nothing is posted)...\n", runID)
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	// A finished run's report is published right after it ends.
	var ended time.Time
	for {
		v, err := store.GetRunView(ctx, user, runID)
		if err != nil {
			fmt.Fprintln(stderr, "cannot read the run:", err)
			return 1
		}
		if v.LatestReport != nil {
			break
		}
		if v.Status.Terminal() {
			if ended.IsZero() {
				ended = time.Now()
			} else if time.Since(ended) > 2*time.Minute {
				fmt.Fprintf(stderr, "run %s ended %s without a report\n", runID, v.Status)
				return 1
			}
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(stderr, "no report within %s; the comment preview is in the web report later (미리 보기)\n", wait)
			return 1
		case <-time.After(2 * time.Second):
		}
	}
	src, err := store.PullRequestCommentPreview(ctx, user, runID, 0)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load the report:", err)
		return 1
	}
	provider, err := store.ProviderForHost(ctx, src.Pin.Host)
	if err != nil {
		fmt.Fprintln(stderr, "cannot load the GitHub host:", err)
		return 1
	}
	client, _ := platform.Client(provider.Host, provider.APIURL)
	body, _, err := prcomment.Preview(ctx, client, src)
	if err != nil {
		fmt.Fprintln(stderr, "cannot render the comment:", err)
		return 1
	}
	fmt.Fprintf(out, "\n----- pull-request comment preview (not posted) -----\n%s----- end of preview -----\n", body)
	return 0
}
