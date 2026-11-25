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
