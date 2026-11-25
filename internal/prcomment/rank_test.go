package prcomment

import (
	"fmt"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// Unreviewed tool findings never push a confirmed finding out of the
// comment: the validation class orders first (reproduced, supported, a
// triage problem not yet verified, other unconfirmed), severity within it.
func TestChangedItemsRankValidationBeforeSeverity(t *testing.T) {
	r := &report.Report{}
	s := &report.PullRequestSection{}
	add := func(id, severity, validation string) {
		r.Findings = append(r.Findings, report.Finding{ID: id, Severity: severity, Validation: validation, Disposition: "open"})
		s.Items = append(s.Items, report.PullRequestItem{Kind: "finding", ID: id, Path: "a.go", Line: 1})
	}
	for i := range 5 {
		add(fmt.Sprintf("static_%d", i), "high", report.ValidationUnreviewed)
	}
	add("review_t001_0001", "medium", report.ValidationUnreviewed)
	add("review_s001_0001", "medium", report.ValidationSupported)
	add("review_s002_0001", "low", report.ValidationReproduced)
	findings, _ := changedItems(r, s)
	var got []string
	for _, f := range findings[:maxFindings] {
		got = append(got, f.f.ID)
	}
	want := []string{"review_s002_0001", "review_s001_0001", "review_t001_0001", "static_0", "static_1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
}

// A model candidate that was not sent to verification (its evidence is not
// shipped code) is listed in the report, never in the comment's short list.
func TestChangedItemsLeaveOutCandidatesNotSentToVerification(t *testing.T) {
	r := &report.Report{}
	s := &report.PullRequestSection{}
	add := func(id string, limitations ...string) {
		r.Findings = append(r.Findings, report.Finding{ID: id, Severity: "high", Validation: report.ValidationUnreviewed, Disposition: "open", Limitations: limitations})
		s.Items = append(s.Items, report.PullRequestItem{Kind: "finding", ID: id, Path: "a_test.go", Line: 1})
	}
	add("review_s001_investigate_1", "candidate_not_shipped")
	add("review_s001_investigate_2")
	findings, _ := changedItems(r, s)
	if len(findings) != 1 || findings[0].f.ID != "review_s001_investigate_2" {
		t.Fatalf("listed %+v", findings)
	}
}

// A hypothesis about diagnostics outside shipped code keeps the validation its
// verification gave it but is never in the comment's short list.
func TestChangedItemsLeaveOutHypothesesAboutNonShippedPaths(t *testing.T) {
	r := &report.Report{}
	s := &report.PullRequestSection{}
	add := func(id string, limitations ...string) {
		r.Findings = append(r.Findings, report.Finding{ID: id, Severity: "high", Validation: report.ValidationSupported, Disposition: "open", Limitations: limitations})
		s.Items = append(s.Items, report.PullRequestItem{Kind: "finding", ID: id, Path: "a_test.go", Line: 1})
	}
	add("review_interpret_1", "hypothesis_not_shipped")
	add("review_interpret_2")
	findings, _ := changedItems(r, s)
	if len(findings) != 1 || findings[0].f.ID != "review_interpret_2" {
		t.Fatalf("listed %+v", findings)
	}
}
