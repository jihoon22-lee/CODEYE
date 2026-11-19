package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/history"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// W23 comparison inputs and finding reviews over real PostgreSQL (docs/REPORT.md).
// Report bodies here are minimal structured reports (the real builder path
// is exercised by the HTTP tests). Skips (NOT executed) without
// CODEYE_TEST_DATABASE_URL.

const vetMsg = `fmt.Printf format %d has arg "x" of wrong type string`

func w23Finding(id, path string, line int) report.Finding {
	fp, b := report.ToolFingerprint("go.vet", "printf", path, "", vetMsg)
	return report.Finding{ID: id, Fingerprint: fp, FingerprintBasis: &b, Severity: "low", Validation: "unreviewed", Category: "correctness", Disposition: "open",
		Locations: []report.Location{{Path: path, StartLine: line, EndLine: line}}}
}

// w23Body is a structured report whose go-vet task ended with status.
func w23Body(t *testing.T, run, builder, vetStatus string, processed []string, findings ...report.Finding) []byte {
	t.Helper()
	v, n := "go1.27.4", len(processed)
	tc := report.ToolCoverage{TaskKey: "go-vet", Analyzer: "go.vet", ToolVersion: &v, ExitClass: "success_with_findings", ProcessedFiles: &n, ProcessedPaths: processed}
	if vetStatus != "succeeded" {
		tc.ExitClass, tc.ProcessedFiles, tc.ProcessedPaths = "execution_error", nil, nil
	}
	if findings == nil {
		findings = []report.Finding{}
	}
	r := report.Report{RunID: run, Builder: report.Builder{Version: builder}, Status: "partial", Findings: findings, Tasks: []report.Task{{Key: "go-vet", Status: vetStatus}},
		Coverage: report.Coverage{Tools: []report.ToolCoverage{tc}, AIReview: report.AIReview{Status: report.AIReviewNotPerformed}}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runWithReport runs one go-vet attempt to finalizing and publishes body.
func (e *w23) runWithReport(project, key, request, builder string, body func(run string) []byte) string {
	e.t.Helper()
	if _, err := e.s.Enqueue(e.ctx, e.user, project, key, json.RawMessage(request)); err != nil {
		e.t.Fatal(err)
	}
	e.idle(e.node)
	o, run := e.startRun(e.node, e.node.workers[0])
	tasks := e.plan(e.node, o, true, taskDecl{"go-vet", string(domain.StageStaticAnalysis)})
	e.move(e.node, o, tasks["go-vet"], "running", "")
	e.move(e.node, o, tasks["go-vet"], "succeeded", "")
	e.mustEvent(e.node, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "finalizing"})
	e.complete(e.node, o, "succeeded")
	b := body(run)
	if _, _, err := e.s.PublishReport(e.ctx, ReportPublication{ReportID: domain.NewID(), RunID: run, AttemptID: o.AttemptID, Epoch: o.Epoch, InputDigest: domain.Digest(b),
		Revision: 1, SchemaVersion: "0.1", BuilderVersion: builder, Status: "partial", QualityVerdict: "inconclusive", Body: b, ExpectRunStatus: domain.RunFinalizing, ResultManifestID: e.resultManifest(o)}); err != nil {
		e.t.Fatal(err)
	}
	return run
}

type w23 struct {
	*w06
	node testNode
}

func newW23(t *testing.T) *w23 {
	e := &w23{w06: newW15(t)}
	e.node = e.enroll("a", e.root.ID)
	return e
}

func (e *w23) compare(actor, run string, baseline string) (ComparisonData, history.Comparison) {
	e.t.Helper()
	d, err := e.s.LoadComparison(e.ctx, actor, run, 0, baseline, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	side := func(c ComparisonSide) history.Side {
		var r report.Report
		if err := json.Unmarshal(c.Body, &r); err != nil {
			e.t.Fatal(err)
		}
		return history.Side{RunID: c.RunID, Revision: c.Revision, CreatedAt: c.CreatedAt, Profile: c.Profile, Scope: c.Scope, Source: c.Source, Report: r}
	}
	if d.Baseline == nil {
		return d, history.NoBaseline()
	}
	return d, history.Compare(side(d.Current), side(*d.Baseline))
}

func stateOf(t *testing.T, c history.Comparison, state string) history.Entry {
	t.Helper()
	for _, x := range c.Entries {
		if x.State == state {
			return x
		}
	}
	t.Fatalf("no %s entry: %+v", state, c.Entries)
	return history.Entry{}
}

const pathsScope = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"paths","paths":["cmd"]}}`

func TestW23DefaultBaselineAndTC14(t *testing.T) {
	e := newW23(t)
	p := e.project.ID
	if FingerprintBuilderSince != report.FingerprintSince {
		t.Fatal("builder version thresholds disagree")
	}
	clean := func(run string) []byte { return w23Body(t, run, "3", "succeeded", []string{"a.go"}) }
	found := func(run string) []byte {
		return w23Body(t, run, "3", "succeeded", []string{"a.go"}, w23Finding("f-"+run[:8], "a.go", 5))
	}
	legacy := e.runWithReport(p, "legacy", deepReview, "2", func(run string) []byte {
		return w23Body(t, run, "2", "succeeded", []string{"a.go"}, w23Finding("f-"+run[:8], "a.go", 5))
	})
	first := e.runWithReport(p, "first", deepReview, "3", found)
	// The pre-W23 revision is never a default baseline.
	if ref, err := e.s.DefaultBaseline(e.ctx, e.user, first); err != nil || ref != nil {
		t.Fatalf("legacy chosen as baseline: %+v %v", ref, err)
	}
	// TC-14: this run's go vet failed, so the vanished finding is unverified.
	failed := e.runWithReport(p, "failed", deepReview, "3", func(run string) []byte { return w23Body(t, run, "3", "failed", nil) })
	d, c := e.compare(e.user, failed, "")
	if d.Selection != BaselineDefault || d.Baseline.RunID != first || !c.Comparable {
		t.Fatalf("baseline %+v", d.Baseline)
	}
	if a := stateOf(t, c, history.StateAbsentUnverified); *a.Reason != history.ReasonCheckFailed || c.Counts[history.StateResolvedObserved] != 0 {
		t.Fatalf("tool failure resolved: %+v", c)
	}
	// Scope reduction: the run's own request (paths=cmd) does not cover a.go.
	// It is not a default baseline candidate of a full run either.
	narrow := e.runWithReport(p, "narrow", pathsScope, "3", func(run string) []byte { return w23Body(t, run, "3", "succeeded", []string{}) })
	if _, c := e.compare(e.user, narrow, first); *stateOf(t, c, history.StateAbsentUnverified).Reason != history.ReasonScopeReduced {
		t.Fatal("scope reduction resolved the finding")
	}
	// A real successful re-check of the file without the finding. The
	// failed run is the newer default baseline candidate but has no finding;
	// compare explicitly against the run that had it.
	fixed := e.runWithReport(p, "fixed", deepReview, "3", clean)
	if ref, _ := e.s.DefaultBaseline(e.ctx, e.user, fixed); ref == nil || ref.RunID != failed {
		t.Fatalf("default baseline of fixed run %+v (want the previous full run)", ref)
	}
	if _, c := e.compare(e.user, fixed, first); c.Counts[history.StateResolvedObserved] != 1 || stateOf(t, c, history.StateResolvedObserved).Reason != nil {
		t.Fatalf("resolved %+v", c.Counts)
	}
	// Explicit legacy baseline: not comparable, nothing is new or resolved.
	if _, c := e.compare(e.user, fixed, legacy); c.Comparable || *c.NotComparableReason != history.NotComparableScheme || len(c.Entries) != 0 {
		t.Fatalf("legacy compared %+v", c)
	}
	// Another project's run is never a baseline (not revealed).
	other := e.secondProject("other")
	foreign := e.runWithReport(other.ID, "foreign", deepReview, "3", found)
	if _, err := e.s.LoadComparison(e.ctx, e.user, fixed, 0, foreign, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign baseline %v", err)
	}
	if _, err := e.s.LoadComparison(e.ctx, e.user, fixed, 0, fixed, 0); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("self baseline %v", err)
	}
	// Runs of another project are never default baselines either.
	if ref, _ := e.s.DefaultBaseline(e.ctx, e.user, foreign); ref != nil {
		t.Fatalf("baseline across projects %+v", ref)
	}
	stranger, _ := e.s.CreateUserNamed(e.ctx, "")
	if _, err := e.s.LoadComparison(e.ctx, stranger, fixed, 0, "", 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member comparison %v", err)
	}
}

func TestW23FindingReviews(t *testing.T) {
	e := newW23(t)
	p := e.project.ID
	f := w23Finding("f1", "a.go", 5)
	run := e.runWithReport(p, "r1", deepReview, "3", func(run string) []byte { return w23Body(t, run, "3", "succeeded", []string{"a.go"}, f) })
	verify := func(body []byte) error {
		var r report.Report
		if json.Unmarshal(body, &r) != nil {
			return domain.ErrInvalid
		}
		for _, x := range r.Findings {
			if x.ID == "f1" && x.Fingerprint == f.Fingerprint {
				return nil
			}
		}
		return domain.ErrInvalid
	}
	req := func(disposition, key string, expected int64) ReviewRequest {
		return ReviewRequest{Fingerprint: f.Fingerprint, Disposition: disposition, Reason: "검토 사유", RunID: run, ReportRevision: 1, FindingID: "f1",
			Expected: expected, Key: key, Digest: domain.Digest([]byte(disposition + key)), Verify: verify}
	}
	viewer, _ := e.s.CreateUserNamed(e.ctx, "")
	runner, _ := e.s.CreateUserNamed(e.ctx, "")
	stranger, _ := e.s.CreateUserNamed(e.ctx, "")
	if err := e.s.SetMember(e.ctx, e.user, p, viewer, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetMember(e.ctx, e.user, p, runner, domain.RoleRunner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.SetFindingReview(e.ctx, viewer, p, req("false_positive", "k0", 0)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("viewer set a disposition: %v", err)
	}
	if _, _, err := e.s.SetFindingReview(e.ctx, stranger, p, req("false_positive", "k0", 0)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member: %v", err)
	}
	first, created, err := e.s.SetFindingReview(e.ctx, runner, p, req("false_positive", "k1", 0))
	if err != nil || !created || first.Version != 1 || first.ActorID != runner || first.FindingID != "f1" {
		t.Fatalf("first review %+v %v", first, err)
	}
	// Same key and body replays; another body is an idempotency conflict.
	if again, created, err := e.s.SetFindingReview(e.ctx, runner, p, req("false_positive", "k1", 0)); err != nil || created || again.ID != first.ID {
		t.Fatalf("replay %+v %v", again, err)
	}
	if _, _, err := e.s.SetFindingReview(e.ctx, runner, p, req("suppressed", "k1", 0)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("key reuse %v", err)
	}
	// If-Match: a stale version conflicts.
	if _, _, err := e.s.SetFindingReview(e.ctx, e.user, p, req("suppressed", "k2", 0)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale version %v", err)
	}
	past := time.Now().Add(-time.Hour)
	bad := req("accepted_risk", "k3", 1)
	bad.ExpiresAt = &past
	if _, _, err := e.s.SetFindingReview(e.ctx, e.user, p, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("past expiry %v", err)
	}
	bad = req("accepted_risk", "k3", 1)
	bad.Reason = "   "
	if _, _, err := e.s.SetFindingReview(e.ctx, e.user, p, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty reason %v", err)
	}
	bad = req("accepted_risk", "k3", 1)
	bad.FindingID = "not-in-report"
	bad.Verify = func([]byte) error { return domain.ErrInvalid }
	if _, _, err := e.s.SetFindingReview(e.ctx, e.user, p, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown finding %v", err)
	}
	soon := time.Now().Add(1500 * time.Millisecond)
	timed := req("accepted_risk", "k4", 1)
	timed.ExpiresAt = &soon
	second, _, err := e.s.SetFindingReview(e.ctx, e.user, p, timed)
	if err != nil || second.Version != 2 {
		t.Fatalf("second %+v %v", second, err)
	}
	// The trail is append-only and versioned; the database refuses edits.
	if _, err := e.s.pool.Exec(e.ctx, "UPDATE finding_reviews SET disposition='open' WHERE id=$1", first.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("review row was mutable: %v", err)
	}
	if _, err := e.s.pool.Exec(e.ctx, "DELETE FROM finding_reviews WHERE id=$1", first.ID); err == nil {
		t.Fatal("review row was deletable")
	}
	if _, err := e.s.pool.Exec(e.ctx, `INSERT INTO finding_reviews(id,project_id,fingerprint,version,disposition,reason,actor_id,run_id,report_revision,finding_id,idempotency_key,request_digest)
		VALUES($1,$2,$3,5,'open','x',$4,$5,1,'f1','gap',$6)`, domain.NewID(), p, f.Fingerprint, e.user, run, strings.Repeat("a", 64)); err == nil {
		t.Fatal("version gap accepted")
	}
	page, err := e.s.ListFindingReviews(e.ctx, viewer, p, f.Fingerprint, "", 50)
	if err != nil || page.CanReview || len(page.Items) != 2 || page.Items[0].Version != 2 || page.Items[1].ActorID != runner || page.Items[0].ActorID != e.user {
		t.Fatalf("history %+v %v", page, err)
	}
	if page, err := e.s.ListFindingReviews(e.ctx, runner, p, "", "", 50); err != nil || !page.CanReview || len(page.Items) != 1 || page.Items[0].Version != 2 {
		t.Fatalf("latest %+v %v", page, err)
	}
	// Publication loads the latest review per fingerprint for the builder.
	pub, err := e.s.LoadPublication(e.ctx, run)
	if err != nil || len(pub.Reviews) != 1 || pub.Reviews[0].Version != 2 || pub.Reviews[0].ExpiresAt == nil {
		t.Fatalf("publication reviews %+v %v", pub.Reviews, err)
	}
	// Expiry: the stored row stays; the current view reads it as open.
	time.Sleep(time.Until(soon) + 100*time.Millisecond)
	d, c := e.compare(viewer, run, "")
	if d.CanReview || len(d.Reviews) != 1 {
		t.Fatalf("comparison data %+v", d)
	}
	var cur report.Report
	_ = json.Unmarshal(d.Current.Body, &cur)
	reviews := map[string]history.Review{}
	for _, r := range d.Reviews {
		reviews[r.Fingerprint] = history.Review{Fingerprint: r.Fingerprint, Version: r.Version, Disposition: r.Disposition, ExpiresAt: r.ExpiresAt, RunID: r.RunID, ReportRevision: r.ReportRevision, FindingID: r.FindingID}
	}
	o := history.ApplyReviews(&c, history.Side{RunID: run, Revision: 1, Report: cur}, reviews, d.Now)
	if o.Findings[0].ReviewMatch != history.MatchExpired || o.Findings[0].CurrentDisposition != "open" || o.Summary.Current.Hidden != 0 {
		t.Fatalf("expired review still applied: %+v", o.Findings)
	}
	if n := e.count("SELECT count(*) FROM finding_reviews WHERE project_id=$1", p); n != 2 {
		t.Fatalf("audit rows %d", n)
	}
}
