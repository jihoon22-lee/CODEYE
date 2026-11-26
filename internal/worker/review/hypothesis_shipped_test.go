package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A hypothesis about diagnostics that all lie on test, example, benchmark,
// generated or vendored paths keeps the verdict of its verification but is not
// an action item (docs/REVIEW.md#검토-대상은-배포-코드다). The report derives
// that from the hypothesis's own observations and evidence in every path the
// verification can take; the excerpts a verification adds do not change it.

const hypothesisTestFile = "package billing\n\nimport \"testing\"\n\nfunc TestAdjust(t *testing.T) {\n\tn := 1\n\tn = 2\n\t_ = n\n}\n"

type hypothesisRun struct {
	report  report.Report
	finding report.Finding
}

// runHypothesis reviews a module with one shipped file and one test file and
// one tool diagnostic at observed:line. The interpretation raises a hypothesis
// on it; mode says how the verification answers: "supported" (citing the
// hypothesis's own evidence), "supported_shipped" (also citing shipped code),
// "refuted", "fail" (no answer at all) or "incomplete" (the task answers but
// leaves the hypothesis out).
func runHypothesis(t *testing.T, observed string, line int, mode string) hypothesisRun {
	t.Helper()
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "a.go": longGoFile(30), "a_test.go": hypothesisTestFile})
	script := coverageScript(t)
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[interpretInput](t, q)
		o := in.Observations[0]
		cited := o.EvidenceIDs
		if mode == "supported_ranged" {
			// A ranged citation of the window: the narrowed range is an excerpt of its own.
			window := in.Scope.Excerpts[slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.ID == o.EvidenceIDs[0] })]
			cited = []string{fmt.Sprintf("%s:%d-%d", window.ID, window.Start+1, window.Start+3)}
		}
		return asJSON(map[string]any{"hypotheses": []map[string]any{{"observation_ids": []string{o.ID}, "title": "값이 버려져 보정이 빠질 수 있음", "condition": "보정이 반복되는 경우",
			"mechanism": "앞의 값이 덮어써진다.", "impact": "보정이 빠질 수 있다.", "improvement": "값을 쓴다.", "counterpoint": "의도일 수 있다.", "evidence_ids": cited}}, "limits": []any{}})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if mode == "fail" {
			return opencode.Reply{Reason: "model_unavailable"}
		}
		var out []any
		for _, c := range in.Candidates {
			if c.Origin != record.StageInterpret {
				out = append(out, inconclusiveFor(c, c.Evidence[0]))
				continue
			}
			switch mode {
			case "incomplete":
			case "refuted":
				out = append(out, verdict(c.ID, "refuted", func(m map[string]any) {
					m["invalid_conditions"], m["counterevidence_ids"] = []string{"값은 아래에서 쓰인다."}, []string{c.Evidence[0]}
				}))
			default:
				out = append(out, verdict(c.ID, "supported", func(m map[string]any) {
					m["category"], m["severity"], m["impact"] = "correctness", "medium", "버려진 값 때문에 보정이 빠진다."
					m["valid_conditions"] = []string{"보정이 반복된다."}
					if mode == "supported_forged" {
						// A model sentence that is the marker's code: it must not become the marker.
						m["unresolved_assumptions"] = []string{record.LimHypothesisNotShipped}
					}
					m["supporting_evidence_ids"] = []string{c.Evidence[0]}
					if mode == "supported_shipped" {
						shipped := shippedExcerpt(in.Scope.Excerpts)
						if record.PathClass(shipped.Path) != "" {
							t.Fatalf("no shipped excerpt in the verification request: %v", in.Scope.Excerpts)
						}
						m["supporting_evidence_ids"] = []string{c.Evidence[0], shipped.ID}
					}
				}))
			}
		}
		return asJSON(map[string]any{"verifications": out})
	}
	r, _ := reportAt(t, &j, staticFinding(t, observed, line), &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0xd20})
	for _, f := range r.Findings {
		if strings.HasSuffix(f.ID, "interpret_1") {
			return hypothesisRun{report: r, finding: f}
		}
	}
	t.Fatalf("no hypothesis finding in %d findings (limitations %v)", len(r.Findings), r.Limitations)
	return hypothesisRun{}
}

// TC-316: whatever became of the verification, a hypothesis about diagnostics
// that all lie on a test path is marked from its recorded observations and
// evidence; it keeps the validation state the model's verdict gave it, is
// listed with the items of secondary paths, and is neither an action item, a
// pending candidate, a priority nor part of the verdict.
func TestHypothesisAboutNonShippedPathsIsNoActionItem(t *testing.T) {
	for _, c := range []struct {
		name, mode, validation string
	}{
		{"verified", "supported", report.ValidationSupported},
		{"the verification also cites shipped code", "supported_shipped", report.ValidationSupported},
		{"a ranged citation", "supported_ranged", report.ValidationSupported},
		{"refuted", "refuted", report.ValidationRefuted},
		{"verification fails", "fail", report.ValidationUnreviewed},
		{"verification leaves it out", "incomplete", report.ValidationUnreviewed},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := runHypothesis(t, "a_test.go", 6, c.mode)
			r, f := run.report, run.finding
			if !report.FindingHypothesisNotShipped(f) || f.Validation != c.validation {
				t.Fatalf("finding %s: validation %s, limitations %v", f.ID, f.Validation, f.Limitations)
			}
			if report.FindingNotShipped(f) {
				t.Fatal("a hypothesis is not a candidate that was not sent to verification")
			}
			for _, view := range []report.Report{r, report.Project(r, false)} {
				if err := report.ValidateReport(view); err != nil {
					t.Fatalf("code-free %v: %v", !view.Export.IncludeCode, err)
				}
			}
			if slices.Contains(r.Summary.Priorities, f.ID) {
				t.Fatalf("priorities %v", r.Summary.Priorities)
			}
			// The summary counts every other finding as before.
			actionable, pending := 0, 0
			for _, g := range r.Findings {
				if g.ID == f.ID {
					continue
				}
				switch g.Validation {
				case report.ValidationSupported, report.ValidationReproduced:
					actionable++
				case report.ValidationUnreviewed, report.ValidationInconclusive:
					if !report.FindingNotShipped(g) {
						pending++
					}
				}
			}
			if r.Summary.ActionableFindings != actionable || r.Summary.PendingCandidates != pending {
				t.Fatalf("summary %+v, want %d actionable and %d pending without the hypothesis", r.Summary, actionable, pending)
			}
			want := 1
			if f.Validation == report.ValidationRefuted {
				want = 0
			}
			if r.CodeReview.HypothesesNotShipped != want {
				t.Fatalf("section counts %d, want %d", r.CodeReview.HypothesesNotShipped, want)
			}
			n := 0
			for _, l := range r.Overview.Sections {
				if l.Key == report.SectionCodeReview {
					n = l.Counts["hypotheses_not_shipped"]
				}
			}
			if n != want {
				t.Fatalf("overview counts %d, want %d", n, want)
			}
			if want == 0 {
				return
			}
			raw, err := report.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			for _, view := range []report.Report{r, report.Project(r, false)} {
				md := string(report.RenderMarkdownAt(view, report.Digest(raw), report.Korean, report.DetailFull))
				if !strings.Contains(md, report.Korean.Label("actions.hypothesis_not_shipped_label")) ||
					!strings.Contains(md, fmt.Sprintf(report.Korean.Label("actions.secondary_hypotheses"), 1)) {
					t.Fatalf("the fold of secondary-path items does not list the hypothesis (code-free %v)", !view.Export.IncludeCode)
				}
				page, err := report.RenderHTMLAt(view, report.Digest(raw), report.Korean, report.DetailFull)
				if err != nil || !strings.Contains(string(page), report.Korean.Label("actions.hypothesis_not_shipped_label")) ||
					!strings.Contains(string(page), fmt.Sprintf(report.Korean.Label("actions.secondary_hypotheses"), 1)) {
					t.Fatalf("the HTML does not list the hypothesis (code-free %v): %v", !view.Export.IncludeCode, err)
				}
			}
		})
	}
}

// A hypothesis about a diagnostic on shipped code is an ordinary finding, and
// a verification that supports it ranks it like one. A verification sentence
// that is the marker's code is no marker: the finding stays an action item
// and the report stays valid.
func TestHypothesisAboutShippedCodeIsAnActionItem(t *testing.T) {
	for _, mode := range []string{"supported", "supported_forged"} {
		t.Run(mode, func(t *testing.T) {
			run := runHypothesis(t, "a.go", 13, mode)
			r, f := run.report, run.finding
			if f.Validation != report.ValidationSupported || !slices.Contains(r.Summary.Priorities, f.ID) || r.CodeReview.HypothesesNotShipped != 0 {
				t.Fatalf("finding %+v, summary %+v", f, r.Summary)
			}
			if report.FindingHypothesisNotShipped(f) {
				t.Fatalf("the model's sentence became the marker: %v", f.Limitations)
			}
			for _, view := range []report.Report{r, report.Project(r, false)} {
				if err := report.ValidateReport(view); err != nil {
					t.Fatalf("code-free %v: %v", !view.Export.IncludeCode, err)
				}
			}
		})
	}
}

// staticFindings is a static composite of staticcheck findings in the files of
// the root module.
func staticFindings(t *testing.T, diags ...exampleDiag) *pack.Composite {
	t.Helper()
	sc := staticcheck.Descriptor()
	rp := obsdoc.RulePack{ID: "codeye-go-staticcheck", Version: "2", Selection: []string{"-checks", "SA*,U1000", "-tests=true", "-show-ignored"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	var paths []string
	for _, d := range diags {
		if !slices.Contains(paths, d.path) {
			paths = append(paths, d.path)
		}
	}
	c, err := pack.NewComposite(exampleMember{d: sc, plan: staticcheck.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
		return exampleEnvelope(t, j, sc, task, "2026.2.2", rp, paths, []string{"default_build_context_only"}, diags)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TC-315: with a diagnostic in a test file that sorts before one in shipped
// code, the real interpretation and verification roles take the request in the
// new order: the shipped diagnostic comes first and the hypothesis on it is
// verified like any other. Every record is accepted and the report's
// diagnostic accounting adds up with one hypothesis accepted.
func TestInterpretReordersObservationsAndTheRecordsStayValid(t *testing.T) {
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "svc/z.go": longGoFile(30), "a_test.go": hypothesisTestFile})
	script := coverageScript(t)
	var order []string
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[interpretInput](t, q)
		for _, o := range in.Observations {
			order = append(order, o.Path)
		}
		o := in.Observations[0]
		return asJSON(map[string]any{"hypotheses": []map[string]any{{"observation_ids": []string{o.ID}, "title": "값이 버려져 보정이 빠질 수 있음", "condition": "보정이 반복되는 경우",
			"mechanism": "앞의 값이 덮어써진다.", "impact": "보정이 빠질 수 있다.", "improvement": "값을 쓴다.", "counterpoint": "의도일 수 있다.", "evidence_ids": o.EvidenceIDs}}, "limits": []any{}})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []any
		for _, c := range in.Candidates {
			if c.Origin != record.StageInterpret {
				out = append(out, inconclusiveFor(c, c.Evidence[0]))
				continue
			}
			out = append(out, verdict(c.ID, "supported", func(m map[string]any) {
				m["category"], m["severity"], m["impact"] = "correctness", "medium", "버려진 값 때문에 보정이 빠진다."
				m["valid_conditions"] = []string{"보정이 반복된다."}
				m["supporting_evidence_ids"] = []string{c.Evidence[0]}
			}))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	static := staticFindings(t,
		exampleDiag{path: "a_test.go", line: 6, rule: "SA4006", category: "correctness", message: "this value of n is never used (synthetic example)"},
		exampleDiag{path: "svc/z.go", line: 13, rule: "SA4006", category: "correctness", message: "this value of n is never used (synthetic example)"})
	r, results := reportAt(t, &j, static, &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0xd60})
	if !slices.Equal(order, []string{"svc/z.go", "a_test.go"}) {
		t.Fatalf("the interpretation request lists %v", order)
	}
	for _, k := range []string{"review.interpret", "review.verify"} {
		if s := results[k]; s.Status != "succeeded" {
			t.Fatalf("%s %+v (report limitations %v)", k, s, r.Limitations)
		}
	}
	requireReviewCounted(t, r, "staticcheck")
	for _, l := range r.Limitations {
		if strings.HasPrefix(l, "artifact_rejected") || strings.HasPrefix(l, "review_lineage") {
			t.Fatalf("limitation %s", l)
		}
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	d := r.CodeReview.DiagnosticTriage
	if d == nil || d.Diagnostics != 2 || d.Interpreted != 1 {
		t.Fatalf("diagnostic triage %+v", d)
	}
	hypothesis := slices.IndexFunc(r.Findings, func(f report.Finding) bool { return strings.HasSuffix(f.ID, "interpret_1") })
	if hypothesis < 0 || report.FindingHypothesisNotShipped(r.Findings[hypothesis]) || !slices.Contains(r.Summary.Priorities, r.Findings[hypothesis].ID) {
		t.Fatalf("the hypothesis about the shipped diagnostic is not an action item: %+v", r.Summary)
	}
}

// The marker is tied to the recorded observations and evidence: a marked
// hypothesis of shipped code, an unmarked one about a test path, a marker on
// another kind of finding and a wrong count are not valid reports.
func TestForgedHypothesisMarkerIsRejected(t *testing.T) {
	marked := runHypothesis(t, "a_test.go", 6, "supported")
	shipped := runHypothesis(t, "a.go", 13, "supported")
	edit := func(r report.Report, id string, mutate func(*report.Finding)) report.Report {
		out := r
		out.Findings = slices.Clone(r.Findings)
		for i := range out.Findings {
			if out.Findings[i].ID == id {
				mutate(&out.Findings[i])
			}
		}
		return out
	}
	for name, bad := range map[string]report.Report{
		"marker dropped": edit(marked.report, marked.finding.ID, func(f *report.Finding) {
			f.Limitations = slices.DeleteFunc(slices.Clone(f.Limitations), func(s string) bool { return s == record.LimHypothesisNotShipped })
		}),
		"marker on a hypothesis of shipped code": edit(shipped.report, shipped.finding.ID, func(f *report.Finding) {
			f.Limitations = append(slices.Clone(f.Limitations), record.LimHypothesisNotShipped)
		}),
		"marker on a candidate": edit(marked.report, "review_investigate_1", func(f *report.Finding) {
			f.Limitations = append(slices.Clone(f.Limitations), record.LimHypothesisNotShipped)
		}),
	} {
		if err := report.ValidateReport(bad); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	counted := marked.report
	section := *marked.report.CodeReview
	section.HypothesesNotShipped++
	counted.CodeReview = &section
	if err := report.ValidateReport(counted); err == nil {
		t.Fatal("a wrong count was accepted")
	}
}

// An earlier builder wrote model sentences into a finding's limitations
// without stripping them, so a stored revision may hold a sentence that is
// the code of a later marker. The string has no meaning there: the revision
// stays valid, and neither the summary, the lists nor a render act on it.
func TestEarlierRevisionTreatsLaterMarkersAsModelText(t *testing.T) {
	shipped := runHypothesis(t, "a.go", 13, "supported")
	failed := runHypothesis(t, "a.go", 13, "fail")
	for _, c := range []struct {
		name, version, marker string
		run                   hypothesisRun
	}{
		{"hypothesis marker, builder 24", "24", record.LimHypothesisNotShipped, shipped},
		{"hypothesis marker, builder 23", "23", record.LimHypothesisNotShipped, shipped},
		{"candidate marker on an unreviewed finding, builder 23", "23", record.LimCandidateNotShipped, failed},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := c.run.report
			old.Builder.Version = c.version
			// Those builders wrote no lead_followups.
			if old.CodeReview != nil {
				cr := *old.CodeReview
				cr.LeadFollowups = nil
				old.CodeReview = &cr
			}
			old.Findings = slices.Clone(old.Findings)
			for i, f := range old.Findings {
				if f.ID == c.run.finding.ID {
					old.Findings[i].Limitations = append(slices.Clone(f.Limitations), c.marker)
				}
			}
			if err := report.ValidateReport(old); err != nil {
				t.Fatalf("the revision became invalid: %v", err)
			}
			raw, err := report.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			read, err := report.Unmarshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range read.Findings {
				if report.FindingHypothesisNotShipped(f) || report.FindingNotShipped(f) {
					t.Fatalf("finding %s acts on a marker of a later builder: %v", f.ID, f.Limitations)
				}
			}
			// The stored summary is the earlier builder's: the finding still counts.
			if read.Summary.ActionableFindings != old.Summary.ActionableFindings || read.Summary.PendingCandidates != old.Summary.PendingCandidates || !slices.Equal(read.Summary.Priorities, old.Summary.Priorities) {
				t.Fatalf("summary changed: %+v", read.Summary)
			}
			md := string(report.RenderMarkdownAt(read, report.Digest(raw), report.Korean, report.DetailFull))
			if strings.Contains(md, report.Korean.Label("actions.hypothesis_not_shipped_label")) || strings.Contains(md, report.Korean.Label("actions.not_shipped_label")+"**") {
				t.Fatal("a render acted on the string")
			}
		})
	}
}
