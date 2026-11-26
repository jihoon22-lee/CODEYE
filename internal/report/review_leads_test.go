package report

import (
	"slices"
	"testing"
)

// The lead follow-up counters (code_review.lead_followups) follow from the
// task rows, the AI review items and coverage.review_calls; a report whose
// counters say otherwise is rejected.
func TestLeadFollowupsValidation(t *testing.T) {
	base := sliceExample(t)
	if base.CodeReview == nil || base.CodeReview.LeadFollowups == nil {
		t.Fatal("the slice example has no lead_followups")
	}
	if err := ValidateReport(base); err != nil {
		t.Fatal(err)
	}
	lf := func(r *Report) *LeadFollowups { return r.CodeReview.LeadFollowups }
	cases := map[string]func(r *Report){
		"planned tasks off":          func(r *Report) { lf(r).TasksPlanned++ },
		"not-run reason off":         func(r *Report) { lf(r).TasksNotRun["no_leads"]++ },
		"run without a task row":     func(r *Report) { lf(r).TasksRun++ },
		"accepted without an item":   func(r *Report) { lf(r).TasksAccepted++ },
		"calls not in review_calls":  func(r *Report) { lf(r).Calls++ },
		"unknown unresolved reason":  func(r *Report) { lf(r).LeadsUnresolved["lead_guessed"] = 1; lf(r).LeadsAsked++ },
		"resolved beyond asked":      func(r *Report) { lf(r).LeadsResolved++ },
		"unknown disposition":        func(r *Report) { lf(r).Dispositions["merged"] = 1 },
		"new lines without a record": func(r *Report) { lf(r).NewExcerpts, lf(r).NewLines = 1, 4 },
		"object missing":             func(r *Report) { r.CodeReview.LeadFollowups = nil },
		"object without follow-up tasks": func(r *Report) {
			r.Tasks = slices.DeleteFunc(r.Tasks, func(t Task) bool { return isLeadsTask(t.Key) })
		},
		// The report schema ties only 0.9 records and leads calls to builder
		// 26; the object itself is checked here.
		"object in an earlier builder": func(r *Report) { r.Builder.Version = "25" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := clone(t, base)
			edit(&r)
			if err := validateLeadFollowups(r); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
