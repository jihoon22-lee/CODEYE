package history

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// Pure comparison rules (docs/40 §3). The PostgreSQL and HTTP tests run the
// same rules on published revisions; these cover every branch quickly.

var localSource = json.RawMessage(`{"kind":"local","relative_path":".","source_root_id":"11111111-1111-4111-8111-111111111111"}`)

func vetFinding(id, path string, line int, msg string) report.Finding {
	fp, b := report.ToolFingerprint("go.vet", "printf", path, "", msg)
	return report.Finding{ID: id, Fingerprint: fp, FingerprintBasis: &b, Severity: "low", Validation: "unreviewed", Category: "correctness", Disposition: "open",
		Locations: []report.Location{{Path: path, StartLine: line, EndLine: line}}}
}

func strp(s string) *string { return &s }
func intp(n int) *int       { return &n }

// vetRun is a report whose go-vet task ended with status and, when it
// succeeded, processed the given paths with tool version.
func vetRun(run, status, version string, processed []string, findings ...report.Finding) Side {
	tc := report.ToolCoverage{TaskKey: "go-vet", Analyzer: "go.vet", ToolVersion: strp(version), ExitClass: "success_with_findings", FilesInScope: len(processed), ProcessedPaths: processed}
	if status == "succeeded" {
		tc.ProcessedFiles = intp(len(processed))
	} else {
		tc.ExitClass, tc.ProcessedPaths = "execution_error", nil
	}
	if version == "" {
		tc.ToolVersion = nil
	}
	if findings == nil {
		findings = []report.Finding{}
	}
	r := report.Report{RunID: run, Builder: report.Builder{Version: report.BuilderVersion}, Findings: findings, Tasks: []report.Task{{Key: "go-vet", Status: status}},
		Coverage: report.Coverage{Tools: []report.ToolCoverage{tc}, AIReview: report.AIReview{Status: report.AIReviewNotPerformed}}}
	return Side{RunID: run, Revision: 1, Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, Source: localSource, Report: r}
}

func only(t *testing.T, c Comparison, state string) Entry {
	t.Helper()
	for _, e := range c.Entries {
		if e.State == state {
			return e
		}
	}
	t.Fatalf("no %s entry in %+v", state, c.Entries)
	return Entry{}
}

func reason(e Entry) string {
	if e.Reason == nil {
		return ""
	}
	return *e.Reason
}

const msg = "fmt.Printf format %d has arg \"x\" of wrong type string"

// TC-14: a finding hidden by this run's tool failure is not resolved.
func TestTC14MissingCheckIsNeverResolved(t *testing.T) {
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go"}, vetFinding("f1", "a.go", 5, msg))
	for _, tc := range []struct {
		name string
		cur  Side
		want string
	}{
		{"tool failed", vetRun("cur", "failed", "go1.27.4", nil), ReasonCheckFailed},
		{"artifact rejected (no accepted coverage)", func() Side {
			s := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
			s.Report.Coverage.Tools = []report.ToolCoverage{}
			s.Report.Limitations = []string{report.LimArtifactRejected + ":go-vet"}
			return s
		}(), ReasonCheckFailed},
		{"partial parse", func() Side {
			s := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
			s.Report.Coverage.Tools[0].PartialParse = true
			return s
		}(), ReasonCheckFailed},
		{"check not planned", func() Side {
			s := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
			s.Report.Coverage.Tools, s.Report.Tasks = []report.ToolCoverage{}, []report.Task{}
			return s
		}(), ReasonCheckNotRun},
		{"scope reduced", func() Side {
			s := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
			s.Scope = domain.Scope{Kind: "paths", Paths: []string{"cmd"}}
			return s
		}(), ReasonScopeReduced},
		{"file not inspected", vetRun("cur", "succeeded", "go1.27.4", []string{"b.go"}), ReasonLocationNotChecked},
		{"tool version changed", vetRun("cur", "succeeded", "go1.28.0", []string{"a.go"}), ReasonRulesChanged},
		{"tool version unknown", vetRun("cur", "succeeded", "", []string{"a.go"}), ReasonRulesUnknown},
		{"other source variant", func() Side {
			s := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
			s.Source = json.RawMessage(`{"kind":"local","relative_path":"sub","source_root_id":"11111111-1111-4111-8111-111111111111"}`)
			return s
		}(), ReasonSourceVariant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Compare(tc.cur, base)
			e := only(t, c, StateAbsentUnverified)
			if reason(e) != tc.want || c.Counts[StateResolvedObserved] != 0 || c.Counts[StateAbsentUnverified] != 1 {
				t.Fatalf("reason %q want %q counts %v", reason(e), tc.want, c.Counts)
			}
		})
	}
	// A real successful re-check of the same file with the same rules.
	c := Compare(vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"}), base)
	if e := only(t, c, StateResolvedObserved); e.Reason != nil || c.Counts[StateResolvedObserved] != 1 {
		t.Fatalf("resolved %+v", e)
	}
}

func TestPersistingChangedNew(t *testing.T) {
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, vetFinding("f1", "a.go", 5, msg), vetFinding("f2", "b.go", 9, "other diagnostic"))
	moved := vetFinding("g1", "a.go", 12, msg) // same issue, lines shifted
	raised := vetFinding("g2", "b.go", 9, "other diagnostic")
	raised.Severity = "medium"
	fresh := vetFinding("g3", "a.go", 30, "a new diagnostic")
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, moved, raised, fresh)
	c := Compare(cur, base)
	if !c.Comparable || c.Counts[StatePersisting] != 1 || c.Counts[StateChanged] != 1 || c.Counts[StateNew] != 1 {
		t.Fatalf("counts %v", c.Counts)
	}
	if p := only(t, c, StatePersisting); !slices.Equal(p.Changes, []string{"lines"}) || p.Current[0].FindingID != "g1" || p.Baseline[0].FindingID != "f1" {
		t.Fatalf("persisting %+v", p)
	}
	if ch := only(t, c, StateChanged); !slices.Contains(ch.Changes, "severity") {
		t.Fatalf("changed %+v", ch)
	}
	if n := only(t, c, StateNew); n.Reason != nil {
		t.Fatalf("new with reason %q", reason(n))
	}
	// A new entry the baseline could not have seen says so.
	c = Compare(cur, vetRun("base", "failed", "go1.27.4", nil))
	for _, e := range c.Entries {
		if e.State != StateNew || reason(e) != ReasonCheckFailed {
			t.Fatalf("entry %+v", e)
		}
	}
	// Different fingerprint schemes are not compared at all.
	old := base
	old.Report.Builder.Version = "2"
	if c := Compare(cur, old); c.Comparable || *c.NotComparableReason != NotComparableScheme || len(c.Entries) != 0 {
		t.Fatalf("old scheme compared: %+v", c)
	}
}

// A move/rename is a candidate only: the new and the vanished entry are
// linked, never merged or confirmed.
func TestAmbiguousMoveIsNotConfirmed(t *testing.T) {
	base := vetRun("base", "succeeded", "go1.27.4", []string{"old/a.go"}, vetFinding("f1", "old/a.go", 5, msg))
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"new/a.go"}, vetFinding("g1", "new/a.go", 5, msg))
	c := Compare(cur, base)
	n, a := only(t, c, StateNew), only(t, c, StateAbsentUnverified)
	if reason(a) != ReasonLocationNotChecked || !slices.Equal(n.MoveCandidates, []string{a.Fingerprint}) || !slices.Equal(a.MoveCandidates, []string{n.Fingerprint}) {
		t.Fatalf("move candidates new=%+v absent=%+v", n, a)
	}
	if c.Counts[StatePersisting] != 0 || c.Counts[StateResolvedObserved] != 0 {
		t.Fatalf("move auto-confirmed: %v", c.Counts)
	}
	// Two identical diagnostics become one: the group changed, nothing resolved.
	base = vetRun("base", "succeeded", "go1.27.4", []string{"a.go"}, vetFinding("f1", "a.go", 5, msg), vetFinding("f2", "a.go", 9, msg))
	cur = vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"}, vetFinding("g1", "a.go", 5, msg))
	c = Compare(cur, base)
	if e := only(t, c, StateChanged); !e.Ambiguous || !slices.Contains(e.Changes, "count") || len(e.Baseline) != 2 || c.Counts[StateResolvedObserved] != 0 {
		t.Fatalf("ambiguous group %+v", e)
	}
}

func modelFinding(id, path string) report.Finding {
	b := report.FingerprintBasis{Scheme: report.FingerprintScheme, Producer: report.ProducerModel, Analyzer: report.AnalyzerReview, Rule: "candidate:unclassified", Paths: []string{path}, Symbols: []string{}, Context: report.ContextUnavailable}
	return report.Finding{ID: id, Fingerprint: "fp2:" + domain.Digest([]byte(path)), FingerprintBasis: &b, Severity: "info", Validation: "unreviewed", Category: "correctness", Disposition: "open",
		Locations: []report.Location{{Path: path, StartLine: 1, EndLine: 3}}}
}

func TestModelAbsenceIsNotResolution(t *testing.T) {
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go"}, modelFinding("m1", "a.go"))
	calls := []report.ReviewCall{{Role: "investigate", Model: "m", PackDigest: "p"}}
	base.Report.Coverage.AIReview.Status, base.Report.Coverage.ReviewCalls = report.AIReviewPerformed, calls
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"})
	for status, want := range map[string]string{report.AIReviewPerformed: ReasonModelAbsence, report.AIReviewPartial: ReasonModelPartial, report.AIReviewNotPerformed: ReasonModelNotPerformed} {
		cur.Report.Coverage.AIReview.Status, cur.Report.Coverage.ReviewCalls = status, calls
		if e := only(t, Compare(cur, base), StateAbsentUnverified); reason(e) != want {
			t.Fatalf("%s: %q want %q", status, reason(e), want)
		}
	}
	cur.Report.Coverage.AIReview.Status, cur.Report.Coverage.ReviewCalls = report.AIReviewPerformed, []report.ReviewCall{{Role: "investigate", Model: "m2", PackDigest: "p"}}
	if e := only(t, Compare(cur, base), StateAbsentUnverified); reason(e) != ReasonRulesChanged {
		t.Fatalf("model change: %q", reason(e))
	}
}

func TestOverlayKeepsPublicationAndCurrentApart(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	strong := vetFinding("g1", "a.go", 5, msg)
	strong.Validation = "supported"
	weak := modelFinding("m1", "b.go")
	weak.Validation = "supported"
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, strong, weak)
	c := Compare(cur, vetRun("base", "succeeded", "go1.27.4", []string{"a.go", "b.go"}))
	past := now.Add(-time.Minute)
	reviews := map[string]Review{
		strong.Fingerprint: {Fingerprint: strong.Fingerprint, Version: 2, Disposition: "false_positive", Reason: "test", RunID: "base", ReportRevision: 1, FindingID: "f-old"},
		weak.Fingerprint:   {Fingerprint: weak.Fingerprint, Version: 1, Disposition: "suppressed", Reason: "test", RunID: "base", ReportRevision: 1, FindingID: "m-old"},
	}
	o := ApplyReviews(&c, cur, reviews, now)
	byID := map[string]FindingView{}
	for _, f := range o.Findings {
		byID[f.FindingID] = f
	}
	if v := byID["g1"]; v.CurrentDisposition != "false_positive" || v.ReviewMatch != MatchApplied || v.PublicationDisposition != "open" || *v.State != StateNew {
		t.Fatalf("strong %+v", v)
	}
	// A weak identity is only a candidate until confirmed on this finding.
	if v := byID["m1"]; v.CurrentDisposition != "open" || v.ReviewMatch != MatchCandidate {
		t.Fatalf("weak %+v", v)
	}
	if o.Summary.AtPublication.Hidden != 0 || o.Summary.AtPublication.Actionable != 2 || o.Summary.Current.Hidden != 1 || o.Summary.Current.Actionable != 1 || o.Summary.Current.Total != 2 {
		t.Fatalf("summary %+v", o.Summary)
	}
	confirmed := reviews[weak.Fingerprint]
	confirmed.RunID, confirmed.FindingID, confirmed.Version = "cur", "m1", 2
	reviews[weak.Fingerprint] = confirmed
	expired := reviews[strong.Fingerprint]
	expired.ExpiresAt = &past
	reviews[strong.Fingerprint] = expired
	o = ApplyReviews(&c, cur, reviews, now)
	if o.Findings[0].FindingID != "g1" || o.Findings[0].ReviewMatch != MatchExpired || o.Findings[0].CurrentDisposition != "open" || o.Findings[1].ReviewMatch != MatchApplied || o.Findings[1].CurrentDisposition != "suppressed" {
		t.Fatalf("after confirm/expiry %+v", o.Findings)
	}
	if o.Summary.Current.Hidden != 1 || o.Summary.Current.Actionable != 1 {
		t.Fatalf("summary after confirm/expiry %+v", o.Summary.Current)
	}
}

// One review (the latest version) is in effect per fingerprint. In an
// ambiguous group it applies only to the finding a person named; confirming
// a second finding of the group moves the confirmation, so the first is a
// candidate (open) again. docs/40 §9 records this limit.
func TestAmbiguousGroupHasOneConfirmedFinding(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	a, b := vetFinding("a", "a.go", 5, msg), vetFinding("b", "a.go", 9, msg)
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"}, a, b)
	c := Compare(cur, vetRun("base", "succeeded", "go1.27.4", []string{"a.go"}))
	match := func(reviews map[string]Review) []string {
		var out []string
		for _, f := range ApplyReviews(&c, cur, reviews, now).Findings {
			out = append(out, f.FindingID+":"+f.ReviewMatch+":"+f.CurrentDisposition)
		}
		return out
	}
	review := Review{Fingerprint: a.Fingerprint, Version: 1, Disposition: "false_positive", Reason: "test", RunID: "base", ReportRevision: 1, FindingID: "old"}
	if got := match(map[string]Review{a.Fingerprint: review}); !slices.Equal(got, []string{"a:candidate:open", "b:candidate:open"}) {
		t.Fatalf("unconfirmed group %v", got)
	}
	review.RunID, review.FindingID, review.Version = "cur", "a", 2
	if got := match(map[string]Review{a.Fingerprint: review}); !slices.Equal(got, []string{"a:applied:false_positive", "b:candidate:open"}) {
		t.Fatalf("confirmed on a %v", got)
	}
	review.FindingID, review.Version = "b", 3
	if got := match(map[string]Review{a.Fingerprint: review}); !slices.Equal(got, []string{"a:candidate:open", "b:applied:false_positive"}) {
		t.Fatalf("confirmed on b %v", got)
	}
}
