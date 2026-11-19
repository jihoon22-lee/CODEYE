package report

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// sliceExample reads examples/report-slices.json (the builder's output for
// fixtures/modules/slices/input under call ceiling 15,
// internal/worker/review/report_slices_example_test.go) as a product report.
func sliceExample(t *testing.T) Report {
	t.Helper()
	raw, err := os.ReadFile("../../examples/report-slices.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["demo"] = false
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TC-169 (docs/REPORT.md#모듈별-검토-장부): the ledger's rows add up per
// module, its totals stay below the files sent to the model, the slice
// findings sit in the code review partition and every call names its task.
func TestLedgerSums(t *testing.T) {
	r := sliceExample(t)
	l := r.CodeReview.Ledger
	if l == nil || len(l.Modules) == 0 {
		t.Fatal("example without ledger rows")
	}
	sent, notSent := ledgerTotals(l)
	eligible := 0
	for _, m := range l.Modules {
		eligible += m.FilesEligible
		if m.FilesSentToModel+m.FilesWithheld+m.FilesUnreadable+m.FilesBudgetExhausted+m.FilesNotPlanned+m.FilesNotSent != m.FilesEligible {
			t.Fatalf("module %+v", m)
		}
	}
	if sent+notSent != eligible || sent > *r.Coverage.LLMExposedFiles {
		t.Fatalf("sent %d not sent %d eligible %d exposed %d", sent, notSent, eligible, *r.Coverage.LLMExposedFiles)
	}
	slice := 0
	for _, id := range r.CodeReview.FindingIDs {
		if sliceFindingID.MatchString(id) {
			slice++
		}
	}
	if slice == 0 {
		t.Fatal("no slice finding in the code review partition")
	}
	for _, c := range r.Coverage.ReviewCalls {
		if c.TaskKey == "" {
			t.Fatalf("call %+v without task", c)
		}
	}
	if *l.CallsUsed > *l.MaxCalls || *l.CallsUsed != len(r.Coverage.ReviewCalls) {
		t.Fatalf("calls %d of %d, review_calls %d", *l.CallsUsed, *l.MaxCalls, len(r.Coverage.ReviewCalls))
	}
	// The code-free projection keeps the ledger unchanged and validates.
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(r.CodeReview.Ledger)
	b, _ := json.Marshal(free.CodeReview.Ledger)
	if string(a) != string(b) {
		t.Fatal("code-free projection changed the ledger")
	}
	// Markdown and HTML show the ledger numbers.
	md := string(RenderMarkdown(r, "0", Korean))
	html, err := RenderHTML(r, "0", Korean)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{Korean.Label("ledger.heading"), "review.investigate.s004", Korean.Label("count.code_review.files_sent_to_model")} {
		if !strings.Contains(md, mdText(want)) && !strings.Contains(md, want) || !strings.Contains(string(html), want) {
			t.Fatalf("%q missing from the rendering", want)
		}
	}
}

// Every ledger and slice rule rejects a report that breaks it.
func TestLedgerValidation(t *testing.T) {
	base := sliceExample(t)
	module := func(r *Report, id string) *LedgerModule {
		for i := range r.CodeReview.Ledger.Modules {
			if m := &r.CodeReview.Ledger.Modules[i]; m.ModuleID == id {
				return m
			}
		}
		t.Fatalf("no module %s", id)
		return nil
	}
	cases := map[string]func(r *Report){
		"files that do not add up": func(r *Report) { module(r, "go:store").FilesSentToModel++ },
		"completed beyond sent": func(r *Report) {
			m := module(r, "go:api")
			m.FilesWithCompletedReview, m.FilesEligible = 4, 4
			m.FilesNotSent = 1
		},
		"status not following the rows":  func(r *Report) { r.CodeReview.Ledger.Status = LedgerComplete },
		"overview_only with a run slice": func(r *Report) { r.CodeReview.Ledger.Status = LedgerOverviewOnly },
		"ledger missing":                 func(r *Report) { r.CodeReview.Ledger = nil },
		"ledger in a builder-5 revision": func(r *Report) { r.Builder.Version = "5" },
		"slice entry differs from its task": func(r *Report) {
			module(r, "go:store").Slices[0].Status = "failed"
		},
		"budget-exhausted files without such a slice": func(r *Report) {
			m := module(r, "go:api")
			m.FilesSentToModel, m.FilesBudgetExhausted = 2, 1
		},
		"fewer calls than listed": func(r *Report) { n := 1; r.CodeReview.Ledger.CallsUsed = &n },
		"request bytes differ":    func(r *Report) { n := 1; r.CodeReview.Ledger.RequestBytesSent = &n },
		"scope differs from its task": func(r *Report) {
			s := "failed"
			r.CodeReview.Ledger.Scope.TaskStatus = &s
		},
		"invalid ceiling": func(r *Report) { n := 9; r.CodeReview.Ledger.MaxCalls = &n },
		"call naming another role's task": func(r *Report) {
			r.Coverage.ReviewCalls[0].TaskKey = "review.verify.s001"
		},
		"call naming an unknown task": func(r *Report) {
			r.Coverage.ReviewCalls[len(r.Coverage.ReviewCalls)-1].TaskKey = "review.verify.s031"
		},
		"modules out of order": func(r *Report) {
			ms := r.CodeReview.Ledger.Modules
			ms[0], ms[1] = ms[1], ms[0]
		},
		"slice finding of an unplanned slice": func(r *Report) {
			for i, f := range r.Findings {
				if sliceFindingID.MatchString(f.ID) {
					id := "review_s031_" + strings.SplitN(f.ID, "_", 3)[2]
					r.Findings[i].ID = id
					for j, x := range r.CodeReview.FindingIDs {
						if x == f.ID {
							r.CodeReview.FindingIDs[j] = id
						}
					}
					return
				}
			}
		},
		"overview headline not from the ledger": func(r *Report) {
			for i := range r.Overview.Sections {
				if r.Overview.Sections[i].Key == SectionCodeReview {
					r.Overview.Sections[i].Counts["files_sent_to_model"]++
				}
			}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := clone(t, base)
			edit(&r)
			if err := ValidateReport(r); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	// A builder-5 revision (no slices, no ledger, no task_key) stays valid.
	old := exampleReport(t)
	old.Builder.Version = "5"
	for i := range old.Coverage.ReviewCalls {
		old.Coverage.ReviewCalls[i].TaskKey = ""
	}
	if err := ValidateReport(old); err != nil {
		t.Fatalf("builder-5 report: %v", err)
	}
	old.Coverage.ReviewCalls[0].TaskKey = "review.environment"
	if err := ValidateReport(old); err == nil {
		t.Fatal("builder-5 report with a call task_key accepted")
	}
}
