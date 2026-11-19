package prcomment

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/github/githubtest"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

const (
	token   = "tok-poster-123456"
	project = "11111111-2222-4333-8444-555555555555"
	runA    = "aaaaaaaa-0000-4000-8000-000000000001"
	runB    = "bbbbbbbb-0000-4000-8000-000000000002"
)

var (
	headA = strings.Repeat("a", 40)
	headB = strings.Repeat("b", 40)
	base  = strings.Repeat("1", 40)
)

// prReport is the example go vet report with a pull-request section whose
// changed line holds the finding and its diagnostic. The finding's text
// tries a mention, an issue reference, HTML, the marker and a code span.
func prReport(t *testing.T, head string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../examples/report-go-vet.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := report.Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	f := &r.Findings[0]
	f.Title = "printf 형식 오류 @octocat #12 <b>x</b> `fmt.Printf(\"%d\", s)`"
	f.Impact = "출력이 깨진다. " + ProjectMarker(project) + "\n```go\nsecret := 1\n```"
	o := r.Observations[0]
	sent := 1
	r.PullRequest = &report.PullRequestSection{Number: 7, URL: "https://github.com/acme/widget/pull/7", Repository: "github.com/acme/widget", BaseRef: "main", HeadRef: "fix",
		BaseSHA: base, HeadSHA: head, HeadAnalyzed: true, ChangedFiles: 1, FilesListed: 1, LineBasis: report.LineBasisLines,
		Review:       report.PullRequestReview{Reviewable: 1, InSnapshot: 1, SentToModel: &sent, NotSent: []string{}},
		Findings:     report.PullRequestCounts{ChangedLines: 1},
		Observations: report.PullRequestCounts{ChangedLines: 1},
		Items: []report.PullRequestItem{{Kind: "finding", ID: f.ID, Scope: report.ScopeChangedLines, Path: "example.go", Line: 5},
			{Kind: "observation", ID: o.ID, Scope: report.ScopeChangedLines, Path: "example.go", Line: 5}}}
	body, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func pin(head string, comment bool) domain.PullRequest {
	return domain.PullRequest{Host: "github.com", Repository: "acme/widget", Number: 7, URL: "https://github.com/acme/widget/pull/7", State: "open", BaseRef: "main", BaseSHA: base,
		HeadRef: "fix", HeadSHA: head, ResolvedAt: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), Comment: comment}
}

// memStore is the poster's database in memory.
type memStore struct {
	due      []postgres.CommentJob
	sources  map[string]postgres.CommentPreview
	posted   map[string]bool
	versions map[string]int64
	outcomes map[string]postgres.CommentOutcome
}

func (m *memStore) DuePullRequestComments(context.Context, int, time.Duration) ([]postgres.CommentJob, error) {
	d := m.due
	m.due = nil
	return d, nil
}

func (m *memStore) PullRequestCommentSource(_ context.Context, run string, _ int) (postgres.CommentPreview, error) {
	return m.sources[run], nil
}

func (m *memStore) NewerPullRequestCommentPosted(_ context.Context, run string) (bool, error) {
	for r, ok := range m.posted {
		if ok && r != run && m.versions[r] > m.versions[run] {
			return true, nil
		}
	}
	return false, nil
}

func (m *memStore) FinishPullRequestComment(_ context.Context, run string, o postgres.CommentOutcome) error {
	m.outcomes[run] = o
	if o.State == postgres.CommentPosted {
		m.posted[run] = true
	}
	return nil
}

func (m *memStore) queue(t *testing.T, run, head string, version int64, attempts int) {
	m.versions[run] = version
	m.sources[run] = postgres.CommentPreview{RunID: run, ProjectID: project, Pin: pin(head, true), Version: version, ReportRevision: 1, ReportBody: prReport(t, head)}
	m.due = append(m.due, postgres.CommentJob{RunID: run, ProjectID: project, Pin: pin(head, true), Version: version, Attempts: attempts, ReportRevision: 1})
}

// One comment per project and pull request: the first run creates it, the
// run after a push updates the same comment, an older run never overwrites
// a newer head's comment, a comment that only copies the marker is not
// taken over, the preview writes nothing, and the body has no code, no
// mention and no second marker.
func TestPosterKeepsOneCommentAndNeverGoesBack(t *testing.T) {
	fake := githubtest.New(token, "codeye-bot")
	defer fake.Close()
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 7, Title: "Fix", State: "open", BaseRef: "main", BaseSHA: base, HeadRef: "fix", HeadSHA: headA})
	// Someone pasted the marker into their own comment.
	fake.AddComment("acme/widget", 7, "mallory", ProjectMarker(project)+"\nnot CODEYE")
	m := &memStore{sources: map[string]postgres.CommentPreview{}, posted: map[string]bool{}, versions: map[string]int64{}, outcomes: map[string]postgres.CommentOutcome{}}
	p := &Poster{Store: m, GitHub: func() (*github.Client, string) { return github.New(fake.URL, token), "" }}
	ctx := context.Background()

	// Preview first: nothing is written.
	body, current, err := Preview(ctx, github.New(fake.URL, token), m.preview(t, runA, headA, 100))
	if err != nil || current != headA || fake.Writes() != 0 {
		t.Fatalf("preview: %v %q writes %d", err, current, fake.Writes())
	}
	for _, bad := range []string{"@octocat", "#12", "<b>", "fmt.Printf", "secret := 1", "```"} {
		if strings.Contains(body, bad) {
			t.Fatalf("comment carries %q:\n%s", bad, body)
		}
	}
	t.Logf("preview:\n%s", body)
	if strings.Count(body, ProjectMarker(project)) != 1 || strings.Count(body, "`example.go:5`") != 1 || !strings.Contains(body, "`aaaaaaa`") {
		t.Fatalf("comment body:\n%s", body)
	}

	m.queue(t, runA, headA, 100, 1)
	if n, err := p.Run(ctx); err != nil || n != 1 || m.outcomes[runA].State != postgres.CommentPosted {
		t.Fatalf("first post: %d %v %+v", n, err, m.outcomes[runA])
	}
	cs := fake.Comments("acme/widget", 7)
	if len(cs) != 2 || cs[1].Author != "codeye-bot" || !strings.Contains(cs[1].Body, "version=100") || cs[0].Body != ProjectMarker(project)+"\nnot CODEYE" {
		t.Fatalf("comments after the first post %+v", cs)
	}

	// A push: the next run updates the same comment.
	fake.SetHead("acme/widget", 7, headB)
	m.queue(t, runB, headB, 200, 1)
	if _, err := p.Run(ctx); err != nil || m.outcomes[runB].State != postgres.CommentPosted || m.outcomes[runB].CommentID != cs[1].ID {
		t.Fatalf("update: %v %+v", err, m.outcomes[runB])
	}
	cs = fake.Comments("acme/widget", 7)
	if len(cs) != 2 || !strings.Contains(cs[1].Body, "version=200") || !strings.Contains(cs[1].Body, "`bbbbbbb`") {
		t.Fatalf("comments after the push %+v", cs)
	}

	// The older run's report again (a later revision): superseded, the
	// comment keeps the newer head. Once by the database, once by the
	// marker on GitHub when the database does not know.
	writes := fake.Writes()
	m.queue(t, runA, headA, 100, 1)
	if _, err := p.Run(ctx); err != nil || m.outcomes[runA].State != postgres.CommentSuperseded {
		t.Fatalf("older run: %v %+v", err, m.outcomes[runA])
	}
	delete(m.posted, runB)
	m.queue(t, runA, headA, 100, 1)
	if _, err := p.Run(ctx); err != nil || m.outcomes[runA].State != postgres.CommentSuperseded || fake.Writes() != writes {
		t.Fatalf("older run by marker: %v %+v writes %d→%d", err, m.outcomes[runA], writes, fake.Writes())
	}

	// The current head moved past the analyzed one: the comment says so.
	fake.SetHead("acme/widget", 7, strings.Repeat("c", 40))
	body, _, _ = Preview(ctx, github.New(fake.URL, token), m.preview(t, runB, headB, 200))
	if !strings.Contains(body, "`ccccccc`") {
		t.Fatalf("moved head not noted:\n%s", body)
	}
}

func (m *memStore) preview(t *testing.T, run, head string, version int64) postgres.CommentPreview {
	return postgres.CommentPreview{RunID: run, ProjectID: project, Pin: pin(head, true), Version: version, ReportRevision: 1, ReportBody: prReport(t, head)}
}

// A rate limit is retried later (and the analysis is untouched); a refused
// token or a missing permission ends the comment as failed; no token at
// all fails without calling GitHub.
func TestPosterFailuresAreRecorded(t *testing.T) {
	fake := githubtest.New(token, "codeye-bot")
	defer fake.Close()
	fake.AddPullRequest("acme/widget", githubtest.PullRequest{Number: 7, Title: "Fix", State: "open", BaseRef: "main", BaseSHA: base, HeadRef: "fix", HeadSHA: headA})
	m := &memStore{sources: map[string]postgres.CommentPreview{}, posted: map[string]bool{}, versions: map[string]int64{}, outcomes: map[string]postgres.CommentOutcome{}}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	p := &Poster{Store: m, GitHub: func() (*github.Client, string) { return github.New(fake.URL, token), "codeye-bot" }, Now: func() time.Time { return now }}
	ctx := context.Background()

	fake.FailPrefix, fake.FailStatus = "/repos/acme/widget/issues", 429
	m.queue(t, runA, headA, 100, 2)
	if _, err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if o := m.outcomes[runA]; o.State != postgres.CommentPending || o.Reason != github.KindRateLimited || !o.RetryAt.After(now) {
		t.Fatalf("rate limited: %+v", o)
	}
	m.queue(t, runA, headA, 100, MaxAttempts)
	if _, _ = p.Run(ctx); m.outcomes[runA].State != postgres.CommentFailed {
		t.Fatalf("after the last attempt: %+v", m.outcomes[runA])
	}

	fake.FailStatus, fake.FailBody = 403, `{"message":"Resource not accessible by integration"}`
	m.queue(t, runA, headA, 100, 1)
	if _, _ = p.Run(ctx); m.outcomes[runA].State != postgres.CommentFailed || m.outcomes[runA].Reason != github.KindForbidden {
		t.Fatalf("forbidden: %+v", m.outcomes[runA])
	}
	fake.FailPrefix = ""
	before := len(fake.Requests())
	p.GitHub = func() (*github.Client, string) { return github.New(fake.URL, ""), "" }
	m.queue(t, runA, headA, 100, 1)
	if _, _ = p.Run(ctx); m.outcomes[runA].Reason != ReasonNoToken || len(fake.Requests()) != before {
		t.Fatalf("no token: %+v", m.outcomes[runA])
	}
	if fake.Writes() != 0 {
		t.Fatalf("%d writes", fake.Writes())
	}
}

func TestPublicationMarkerRoundTrip(t *testing.T) {
	body := Render(Input{ProjectID: project, RunID: runA, Version: 1763596800000000, Pin: pin(headA, true)})
	p, ok := ParsePublication(body)
	if !ok || p.RunID != runA || p.Version != 1763596800000000 || p.Head != headA {
		t.Fatalf("%+v %v\n%s", p, ok, body)
	}
}
