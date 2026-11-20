package report

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

func testPin(files ...domain.PullRequestFile) *domain.PullRequest {
	return &domain.PullRequest{Host: "github.com", Repository: "acme/widget", Number: 7, URL: "https://github.com/acme/widget/pull/7", Title: "Fix", State: "open",
		BaseRef: "main", BaseSHA: strings.Repeat("1", 40), HeadRef: "fix", HeadSHA: strings.Repeat("2", 40), HeadRepository: "fork/widget",
		ResolvedAt: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), ChangedFiles: len(files), Files: files}
}

// A pull-request run's report separates the findings and diagnostics on
// the changed lines, in changed files without line information and
// elsewhere; a run without a pull request has no section and the input
// digest it always had.
func TestPullRequestSectionClassifiesTheChanges(t *testing.T) {
	f := newFixture(t, goVet(t))
	plain := build(t, f.in, DefaultRegistry())
	if plain.PullRequest != nil || has(plain.Coverage.Notes, NotePullRequestFocus) {
		t.Fatal("a branch run has a pull_request section")
	}
	if len(plain.Observations) < 3 {
		t.Fatalf("fixture has %d observations", len(plain.Observations))
	}
	// One diagnostic's line is changed; another file is changed without
	// lines; a third file is changed on other lines.
	first, other := plain.Observations[0].Location, (*ObservationLocation)(nil)
	for _, o := range plain.Observations {
		if o.Location != nil && o.Location.Path != first.Path {
			other = o.Location
			break
		}
	}
	if other == nil {
		t.Fatal("fixture needs diagnostics in two files")
	}
	files := []domain.PullRequestFile{
		{Path: first.Path, Status: "modified", LinesKnown: true, Lines: [][2]int{{first.StartLine, first.StartLine}}},
		{Path: other.Path, Status: "modified"},
		{Path: "gone.go", Status: "removed"},
	}
	f.in.PullRequest = testPin(files...)
	head := strings.Repeat("2", 40)
	f.in.Snapshot.SourceSHA = &head
	r := build(t, f.in, DefaultRegistry())
	if r.Builder.InputDigest == plain.Builder.InputDigest {
		t.Fatal("the pin is not part of the input digest")
	}
	s := r.PullRequest
	if s == nil || !has(r.Coverage.Notes, NotePullRequestFocus) {
		t.Fatal("no pull_request section")
	}
	if s.Number != 7 || s.FilesRemoved != 1 || s.Review.Reviewable != 2 || s.LineBasis != LineBasisMixed || s.FilesWithoutLines != 1 || !s.HeadAnalyzed {
		t.Fatalf("section %+v", s)
	}
	if s.Review.SentToModel != nil {
		t.Fatalf("no review was planned: %+v", s.Review)
	}
	o := s.Observations
	if o.ChangedLines < 1 || o.ChangedFile < 1 || o.InChanges()+o.PreExisting() != r.Summary.RawObservations-r.Summary.MetricObservations {
		t.Fatalf("observation counts %+v", o)
	}
	if s.Findings.InChanges()+s.Findings.PreExisting() != len(r.Findings) || len(s.Items) != s.Findings.InChanges()+o.InChanges() {
		t.Fatalf("finding counts %+v items %d", s.Findings, len(s.Items))
	}
	for _, it := range s.Items {
		if it.Scope != ScopeChangedLines && it.Scope != ScopeChangedFile {
			t.Fatalf("item outside the changes %+v", it)
		}
	}
	if code := Project(r, false); code.PullRequest == nil || len(code.PullRequest.Items) != len(s.Items) {
		t.Fatal("the code-free export drops the section")
	}
	body, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	// examples/report-pull-request.json is a report like this one (its
	// fixture IDs are random, so CODEYE_UPDATE_EXAMPLES=1 rewrites it and
	// otherwise it must still be a valid report with the section;
	// schemas/report.schema.json checks it in scripts/validate_bundle.py).
	const example = "../../examples/report-pull-request.json"
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		if err := os.WriteFile(example, append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := Unmarshal(bytes.TrimSpace(have)); err != nil || stored.PullRequest == nil || stored.Builder.Version != BuilderVersion {
		t.Fatalf("%s is not a current pull-request report (%v); rerun with CODEYE_UPDATE_EXAMPLES=1", example, err)
	}
	md := string(RenderMarkdownAt(r, "d", Korean, DetailSummary))
	for _, want := range []string{`pull request \#7`, "2222222", "변경 부분", "같은 깊이로 검토하지 않았을 수 있다", "파일 1개의 변경 줄을 주지 않아"} {
		if !strings.Contains(md, want) {
			t.Fatalf("summary lacks %q", want)
		}
	}
	if strings.Contains(string(RenderMarkdownAt(plain, "d", Korean, DetailSummary)), `pull request \#`) {
		t.Fatal("a branch run's summary names a pull request")
	}
}

// With model calls the section counts the changed files the review records
// show as sent and lists the rest; without calls it does not count at all.
func TestPullRequestSectionCountsSentFilesOnlyWithModelCalls(t *testing.T) {
	states, base := exampleStages(t)
	r, _, _ := compose(states, base)
	sent := sentPaths(states, reviewRecordKeys(states))
	var seen string
	for p := range sent {
		if seen == "" || p < seen {
			seen = p
		}
	}
	if seen == "" || len(r.Coverage.ReviewCalls) == 0 {
		t.Fatal("the role records sent nothing")
	}
	pin := testPin(domain.PullRequestFile{Path: seen, Status: "modified"}, domain.PullRequestFile{Path: "never/sent.go", Status: "added"})
	if !modelRequested(r.Coverage.ReviewCalls) || modelRequested([]ReviewCall{{Role: "investigate", Outcome: "model_not_configured"}}) {
		t.Fatal("modelRequested does not follow the request digests")
	}
	s := pullRequestSection(&r, pin, map[string]string{seen: "x"}, sent, modelRequested(r.Coverage.ReviewCalls))
	if s.Review.SentToModel == nil || *s.Review.SentToModel != 1 || s.Review.NotSentTotal != 1 || len(s.Review.NotSent) != 1 || s.Review.NotSent[0] != "never/sent.go" {
		t.Fatalf("review %+v", s.Review)
	}
	if s := pullRequestSection(&r, pin, nil, sent, false); s.Review.SentToModel != nil || s.Review.NotSentTotal != 0 || len(s.Review.NotSent) != 0 {
		t.Fatalf("no calls: review %+v", s.Review)
	}
}
