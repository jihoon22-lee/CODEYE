package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

func testPullRequestPin(head string, comment bool) domain.PullRequest {
	return domain.PullRequest{Host: "github.com", Repository: "acme/widget", Number: 7, URL: "https://github.com/acme/widget/pull/7", Title: "Fix", State: "open",
		BaseRef: "main", BaseSHA: strings.Repeat("1", 40), HeadRef: "fix", HeadSHA: head, HeadRepository: "fork/widget",
		ResolvedAt: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), ChangedFiles: 2,
		Files: []domain.PullRequestFile{{Path: "a.go", Status: "modified", LinesKnown: true, Lines: [][2]int{{3, 4}}}, {Path: "gone.go", Status: "removed"}}, Comment: comment}
}

const prBody = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"},"pull_request":{"number":7}}`

// A pull-request run stores its pin once: the request and pin must agree,
// the pin must be of the project's repository, the claimed job carries the
// pull request without the file list's lines, the pin cannot change, the
// same key replays the run and a retry keeps the pin.
func TestPullRequestRunPinAndJob(t *testing.T) {
	s := testStore(t)
	ctx, user, root, prov := registered(t, s)
	p, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: domain.Source{Kind: domain.SourceGit, ProviderID: prov.ID, RepositoryURL: "https://github.com/Acme/widget"}})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("2", 40)
	pin := testPullRequestPin(head, false)
	// Request and pin must go together and name the same number.
	if _, err := s.EnqueuePullRequest(ctx, user, p.ID, "x1", json.RawMessage(`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`), pin); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("pin without a pull_request request: %v", err)
	}
	if _, err := s.Enqueue(ctx, user, p.ID, "x2", json.RawMessage(prBody)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("pull_request request without a pin: %v", err)
	}
	other := pin
	other.Number, other.URL = 8, "https://github.com/acme/widget/pull/8"
	if _, err := s.EnqueuePullRequest(ctx, user, p.ID, "x3", json.RawMessage(prBody), other); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("number mismatch: %v", err)
	}
	foreign := pin
	foreign.Repository, foreign.URL = "acme/other", "https://github.com/acme/other/pull/7"
	if _, err := s.EnqueuePullRequest(ctx, user, p.ID, "x4", json.RawMessage(prBody), foreign); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("pin of another repository: %v", err)
	}
	r, err := s.EnqueuePullRequest(ctx, user, p.ID, "pr-1", json.RawMessage(prBody), pin)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.EnqueuePullRequest(ctx, user, p.ID, "pr-1", json.RawMessage(prBody), testPullRequestPin(strings.Repeat("3", 40), false))
	if err != nil || again.ID != r.ID {
		t.Fatalf("replay %v %v", again.ID, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE runs SET pull_request=jsonb_set(pull_request,'{head_sha}','\"3333333333333333333333333333333333333333\"') WHERE id=$1", r.ID); err == nil {
		t.Fatal("the pin changed after creation")
	}
	v, err := s.GetRunView(ctx, user, r.ID)
	if err != nil || v.PullRequest == nil || v.PullRequest.Pin.HeadSHA != head || v.PullRequest.Comment != nil {
		t.Fatalf("run view %+v %v", v.PullRequest, err)
	}

	e := &nodeEnv{t: t, s: s, ctx: ctx, user: user, root: root, project: p}
	n := e.enroll("git")
	e.beat(n, hb(n.workers, "idle", []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.GitSourceCapability("github.com")}))
	c := e.claimOK(n, n.workers[0], "k")
	j := c.Job.PullRequest
	if c.Lease.RunID != r.ID || j == nil || j.Number != 7 || j.HeadSHA != head || j.BaseSHA != pin.BaseSHA || len(j.Changed) != 1 || j.Changed[0] != "a.go" || c.Job.Source.Branch != "" {
		t.Fatalf("claimed job %+v source %+v", j, c.Job.Source)
	}
	if _, err := s.CompleteAttempt(ctx, n.id, owner(n, c), Completion{Outcome: "failed", Cleanup: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	retried, how, err := s.RetryRun(ctx, user, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kept []byte
	if err := s.pool.QueryRow(ctx, "SELECT pull_request FROM runs WHERE id=$1", retried.ID).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if got, err := domain.ParsePullRequest(kept); err != nil || got.HeadSHA != head {
		t.Fatalf("retry (%s) pin %s %v", how, kept, err)
	}

}

// The comment outbox: a run that asked for a comment queues one row when
// its report is published; due rows are leased; a newer posted run of the
// same pull request supersedes an older one; outcomes are recorded.
func TestPullRequestCommentOutbox(t *testing.T) {
	s := testStore(t)
	ctx, user, _, prov := registered(t, s)
	p, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: domain.Source{Kind: domain.SourceGit, ProviderID: prov.ID, RepositoryURL: "https://github.com/acme/widget"}})
	if err != nil {
		t.Fatal(err)
	}
	older, err := s.EnqueuePullRequest(ctx, user, p.ID, "c1", json.RawMessage(prBody), testPullRequestPin(strings.Repeat("2", 40), true))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.EnqueuePullRequest(ctx, user, p.ID, "c2", json.RawMessage(prBody), testPullRequestPin(strings.Repeat("3", 40), true))
	if err != nil {
		t.Fatal(err)
	}
	silent, err := s.EnqueuePullRequest(ctx, user, p.ID, "c3", json.RawMessage(prBody), testPullRequestPin(strings.Repeat("4", 40), false))
	if err != nil {
		t.Fatal(err)
	}
	queue := func(run string, revision int) {
		t.Helper()
		if _, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) { return true, queuePullRequestComment(ctx, tx, run, revision) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []string{older.ID, newer.ID, silent.ID} {
		queue(r, 1)
	}
	var rows int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM pull_request_comments").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("%d comment rows (the run without a comment queued one?) %v", rows, err)
	}
	jobs, err := s.DuePullRequestComments(ctx, 10, time.Minute)
	if err != nil || len(jobs) != 2 || jobs[0].Attempts != 1 || jobs[0].Pin.Number != 7 || jobs[0].ProjectID != p.ID {
		t.Fatalf("due %+v %v", jobs, err)
	}
	if again, _ := s.DuePullRequestComments(ctx, 10, time.Minute); len(again) != 0 {
		t.Fatal("leased rows taken again")
	}
	versions := map[string]int64{}
	for _, j := range jobs {
		versions[j.RunID] = j.Version
	}
	if versions[newer.ID] <= versions[older.ID] {
		t.Fatalf("versions %v", versions)
	}
	if err := s.FinishPullRequestComment(ctx, newer.ID, CommentOutcome{State: CommentPosted, CommentID: 99, CommentURL: "https://github.com/acme/widget/pull/7#issuecomment-99"}); err != nil {
		t.Fatal(err)
	}
	if superseded, err := s.NewerPullRequestCommentPosted(ctx, older.ID); err != nil || !superseded {
		t.Fatalf("older run not superseded: %v %v", superseded, err)
	}
	if superseded, _ := s.NewerPullRequestCommentPosted(ctx, newer.ID); superseded {
		t.Fatal("the newest run is superseded")
	}
	retryAt := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	if err := s.FinishPullRequestComment(ctx, older.ID, CommentOutcome{State: CommentPending, Reason: "github_rate_limited", RetryAt: retryAt}); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetRunView(ctx, user, older.ID)
	if c := v.PullRequest.Comment; err != nil || c == nil || c.State != CommentPending || *c.Reason != "github_rate_limited" || !c.NextAttemptAt.Equal(retryAt) || c.Attempts != 1 {
		t.Fatalf("pending view %+v %v", v.PullRequest.Comment, err)
	}
	if err := s.FinishPullRequestComment(ctx, older.ID, CommentOutcome{State: CommentSuperseded, Reason: "newer_run_posted"}); err != nil {
		t.Fatal(err)
	}
	// A later report revision re-arms a posted row, never a superseded one.
	queue(older.ID, 2)
	queue(newer.ID, 2)
	for run, want := range map[string]string{older.ID: CommentSuperseded, newer.ID: CommentPending} {
		var state string
		if err := s.pool.QueryRow(ctx, "SELECT state FROM pull_request_comments WHERE run_id=$1", run).Scan(&state); err != nil || state != want {
			t.Fatalf("%s after a new revision: %s %v", run, state, err)
		}
	}
	if err := s.FinishPullRequestComment(ctx, older.ID, CommentOutcome{State: CommentFailed, Reason: "x"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("finishing an ended row: %v", err)
	}
	if _, err := s.PullRequestCommentPreview(ctx, user, older.ID, 0); err != nil {
		t.Fatal(err)
	}
	branchRun, err := s.Enqueue(ctx, user, p.ID, "b1", json.RawMessage(`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PullRequestCommentPreview(ctx, user, branchRun.ID, 0); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("preview of a branch run: %v", err)
	}
}
