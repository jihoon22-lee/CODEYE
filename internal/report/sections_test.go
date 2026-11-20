package report

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/progress"
)

// exampleReport reads examples/report.json (the builder's output for
// fixtures/modules/input, internal/worker/review/report_example_test.go)
// as a product report: the stored example only differs in its demo flag.
func exampleReport(t *testing.T) Report {
	t.Helper()
	return storedExample(t, "../../examples/report.json")
}

// storedExample reads a stored example report (demo: true) as a product
// report.
func storedExample(t *testing.T, path string) Report {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["demo"] != true {
		t.Fatal("the stored example is not marked demo")
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

// clone deep-copies a report through its JSON form.
func clone(t *testing.T, r Report) Report {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out Report
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func staticTool(t *testing.T, r *Report, key string) *StaticTool {
	t.Helper()
	for i := range r.StaticAnalysis.Languages {
		for j := range r.StaticAnalysis.Languages[i].Tools {
			if x := &r.StaticAnalysis.Languages[i].Tools[j]; x.TaskKey == key {
				return x
			}
		}
	}
	t.Fatalf("no static row %s", key)
	return nil
}

// Every observation is in exactly one of static_analysis, metrics and
// secrets; every finding in exactly one of static_analysis and code_review;
// and the section counts add up to the summary.
func TestExampleSectionsPartition(t *testing.T) {
	r := exampleReport(t)
	where := map[string][]string{}
	for _, l := range r.StaticAnalysis.Languages {
		for _, x := range l.Tools {
			for _, id := range x.ObservationIDs {
				where[id] = append(where[id], "static_analysis")
			}
		}
	}
	for _, g := range r.Metrics.Metrics {
		for _, id := range g.ObservationIDs {
			where[id] = append(where[id], "metrics")
		}
	}
	for _, d := range r.Secrets.Detections {
		where[d.ObservationID] = append(where[d.ObservationID], "secrets")
	}
	if len(where) != len(r.Observations) {
		t.Fatalf("%d observations placed, %d in the report", len(where), len(r.Observations))
	}
	for _, o := range r.Observations {
		if len(where[o.ID]) != 1 {
			t.Fatalf("observation %s in %v", o.ID, where[o.ID])
		}
	}
	static := StaticFindingIDs(r)
	if len(static)+len(r.CodeReview.FindingIDs) != len(r.Findings) {
		t.Fatalf("%d static and %d review findings, %d in the report", len(static), len(r.CodeReview.FindingIDs), len(r.Findings))
	}
	for _, id := range r.CodeReview.FindingIDs {
		if slices.Contains(static, id) {
			t.Fatalf("finding %s in both sections", id)
		}
	}
	var counts []int
	for _, s := range r.Overview.Sections {
		if s.Key == SectionStaticAnalysis || s.Key == SectionCodeReview {
			counts = append(counts, s.Counts["findings"])
		}
	}
	if len(counts) != 2 || counts[0]+counts[1] != r.Summary.TotalFindings {
		t.Fatalf("section finding counts %v, summary %d", counts, r.Summary.TotalFindings)
	}
	if !slices.ContainsFunc(r.Overview.Attention, func(a Attention) bool { return a.Code == "secrets_detected" }) {
		t.Fatalf("attention %+v", r.Overview.Attention)
	}
}

// ValidateReport rejects a section that drops, duplicates or moves an
// observation or finding, or counts that no longer add up.
func TestSectionsPartitionRejected(t *testing.T) {
	base := exampleReport(t)
	sc := staticTool(t, &base, "staticcheck").ObservationIDs[0]
	scFinding := staticTool(t, &base, "staticcheck").FindingIDs[0]
	cases := map[string]func(r *Report){
		"observation dropped": func(r *Report) { x := staticTool(t, r, "staticcheck"); x.ObservationIDs = []string{} },
		"observation also a metric": func(r *Report) {
			r.Metrics.Metrics[0].ObservationIDs = append(r.Metrics.Metrics[0].ObservationIDs, sc)
		},
		"observation moved to another tool": func(r *Report) {
			x := staticTool(t, r, "staticcheck")
			x.ObservationIDs = []string{}
			y := staticTool(t, r, "staticcheck.m01")
			y.ObservationIDs = append(y.ObservationIDs, sc)
		},
		"secret detection of a diagnostic": func(r *Report) { r.Secrets.Detections[0].ObservationID = sc },
		"metric observation listed as a diagnostic": func(r *Report) {
			x := staticTool(t, r, "lizard")
			x.ObservationIDs = append(x.ObservationIDs, r.Metrics.Metrics[0].ObservationIDs...)
		},
		"tool finding counted as model finding": func(r *Report) {
			r.CodeReview.FindingIDs = append(r.CodeReview.FindingIDs, scFinding)
		},
		"model finding dropped": func(r *Report) { r.CodeReview.FindingIDs = r.CodeReview.FindingIDs[1:] },
		"tool finding dropped":  func(r *Report) { x := staticTool(t, r, "staticcheck"); x.FindingIDs = []string{} },
		"overview count invented": func(r *Report) {
			for i := range r.Overview.Sections {
				if r.Overview.Sections[i].Key == SectionCodeReview {
					r.Overview.Sections[i].Counts["findings"]++
				}
			}
		},
		"rule tally invented":       func(r *Report) { x := staticTool(t, r, "staticcheck"); x.Rules[0].Count++ },
		"metric group max invented": func(r *Report) { r.Metrics.Metrics[0].MaxValue++ },
		"gap removed":               func(r *Report) { r.Gaps = r.Gaps[1:] },
		"attention removed":         func(r *Report) { r.Overview.Attention = r.Overview.Attention[1:] },
		"unknown section state":     func(r *Report) { r.Metrics.Stage.State = "done" },
		"section block missing":     func(r *Report) { r.Secrets = nil },
		"sections on an older builder": func(r *Report) {
			r.Builder.Version = "4"
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
}

// The code-free projection keeps the section structure and replaces every
// model statement with the omission label. Code-like and secret-like text
// planted in a contract, a conflict and a question never reaches it, and a
// code-free report that still carries such text does not validate.
func TestCodeFreeSections(t *testing.T) {
	full := exampleReport(t)
	codeLike := "func Charge(pay func() error) error { return pay() }"
	secretLike := "api_key = \"" + "sk-" + "abcdefghijklmnop12" + "\""
	pendingLocation := "private/config.go:357–372"
	planted := clone(t, full)
	planted.Documents.Contracts[0].Statement = codeLike
	planted.Documents.Conflicts[0].Summary = secretLike
	planted.Documents.Conflicts[0].Impact = codeLike
	planted.Documents.Questions[0].Question = codeLike + " " + secretLike
	planted.Documents.Comparisons[0].Reason = pendingLocation + "에서 구현 여부를 확인할 수 있다."
	p := Project(planted, false)
	if err := ValidateReport(p); err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{codeLike, "sk-" + "abcdefghijklmnop12", "func Charge", pendingLocation, full.Documents.Contracts[1].Statement, full.Environment.Notes[0].AffectedChecks, full.CodeReview.Limits[0].Statement} {
		if bytes.Contains(b, []byte(s)) {
			t.Fatalf("code-free projection carries %q", s)
		}
	}
	for _, x := range p.Documents.Contracts {
		if x.Statement != textOmitted || len(x.EvidenceIDs) == 0 {
			t.Fatalf("contract %+v", x)
		}
	}
	if p.Documents.Questions[0].Question != textOmitted || p.Documents.Conflicts[0].Summary != textOmitted || p.Environment.Notes[0].StillCheckable != textOmitted || p.CodeReview.Limits[0].Statement != textOmitted {
		t.Fatal("model statement kept in the code-free projection")
	}
	// The projection does not change the shared report.
	if planted.Documents.Contracts[0].Statement != codeLike {
		t.Fatal("projection changed its input")
	}
	// Structure stays: same counts, same evidence, same requirements.
	if !slices.EqualFunc(p.Overview.Sections, full.Overview.Sections, func(a, b OverviewSection) bool { return a.Key == b.Key && len(a.Counts) == len(b.Counts) }) ||
		len(p.Documents.Evidence) != len(full.Documents.Evidence) || len(p.Environment.Requirements) != len(full.Environment.Requirements) {
		t.Fatal("code-free projection lost section structure")
	}
	for _, q := range p.Environment.Requirements {
		if q.Constraint == nil || !keepConstraint(*q.Constraint) {
			t.Fatalf("version constraint %+v withheld", q)
		}
	}
	for name, edit := range map[string]func(*Report){
		"contract":        func(r *Report) { r.Documents.Contracts[0].Statement = codeLike },
		"conflict":        func(r *Report) { r.Documents.Conflicts[0].Summary = secretLike },
		"question":        func(r *Report) { r.Documents.Questions[0].Question = codeLike },
		"environment":     func(r *Report) { r.Environment.Notes[0].NeededObservation = codeLike },
		"interpret limit": func(r *Report) { r.CodeReview.Limits[0].Statement = codeLike },
		"constraint":      func(r *Report) { c := "see " + codeLike; r.Environment.Requirements[0].Constraint = &c },
	} {
		t.Run("code-free "+name, func(t *testing.T) {
			bad := clone(t, p)
			edit(&bad)
			if err := ValidateReport(bad); err == nil {
				t.Fatal("code-free report with model text accepted")
			}
		})
	}
	// The full export never holds secret-like model text either.
	bad := clone(t, full)
	bad.Documents.Contracts[0].Statement = secretLike
	if err := ValidateReport(bad); err == nil {
		t.Fatal("secret-like contract text accepted")
	}
}

// Without review tasks the environment and documents sections still show the
// discovery documents and say why no model interpretation exists; the
// verdict and summary are the builder-4 values.
func TestSectionsWithoutModelReview(t *testing.T) {
	f := newFixture(t, goVet(t))
	addDiscovery(t, f, nil, nil)
	r := build(t, f.in, mustRegistry(t))
	for _, m := range []ModelInterpretation{r.Environment.Model, r.Documents.Model} {
		if m.State != ModelNotPerformed || m.Reason == nil || *m.Reason != "review_tasks_not_planned" {
			t.Fatalf("model %+v", m)
		}
	}
	if r.Environment.Discovery != DiscoveryAccepted || len(r.Environment.Modules) == 0 || len(r.Environment.Requirements) == 0 || len(r.Environment.Notes) != 0 {
		t.Fatalf("environment %+v", r.Environment)
	}
	if r.Documents.Discovery != DiscoveryAccepted || len(r.Documents.Modules) == 0 || len(r.Documents.Contracts)+len(r.Documents.Conflicts)+len(r.Documents.Questions) != 0 {
		t.Fatalf("documents %+v", r.Documents)
	}
	if len(r.CodeReview.Roles) != 0 || len(r.CodeReview.FindingIDs) != 0 {
		t.Fatalf("code review %+v", r.CodeReview)
	}
	gaps := map[string]bool{}
	for _, g := range r.Gaps {
		gaps[g.Section+"/"+g.Code] = true
	}
	for _, want := range []string{"environment/model_not_performed", "documents/model_not_performed", "code_review/review_tasks_not_planned"} {
		if !gaps[want] {
			t.Fatalf("gaps %v lack %s", r.Gaps, want)
		}
	}
	if !slices.ContainsFunc(r.Overview.Attention, func(a Attention) bool { return a.Code == "model_review_not_performed" }) {
		t.Fatalf("attention %+v", r.Overview.Attention)
	}
	if r.QualityVerdict != VerdictInconclusive || r.Coverage.AIReview.Status != AIReviewNotPerformed {
		t.Fatalf("verdict %s, ai review %s", r.QualityVerdict, r.Coverage.AIReview.Status)
	}
	md := string(RenderMarkdown(r, strings.Repeat("0", 64), Korean))
	if want := mdText(tf(Korean, "model.not_performed", reasonText(Korean, "review_tasks_not_planned"))); strings.Count(md, want) != 2 {
		t.Fatalf("markdown does not say why the model interpretation is absent:\n%s", md)
	}
}

// Tasks without a stored stage (runs published before stages were recorded)
// leave every section state not_recorded instead of guessing it from keys.
func TestSectionStageNotRecorded(t *testing.T) {
	f := newFixture(t, goVet(t))
	r := build(t, f.in, mustRegistry(t))
	for _, st := range []SectionStage{r.Environment.Stage, r.Documents.Stage, r.StaticAnalysis.Stage, r.Metrics.Stage, r.Secrets.Stage, r.CodeReview.Stage} {
		if st.State != StateNotRecorded {
			t.Fatalf("stage %+v", st)
		}
	}
	// With the stage stored the state follows the run screen's classifier.
	for i := range f.in.Tasks {
		f.in.Tasks[i].Stage = "static_analysis"
	}
	r = build(t, f.in, mustRegistry(t))
	tasks := []progress.Task{{Stage: "static_analysis", Status: "succeeded"}}
	if want := progress.TaskStage("static_analysis", tasks, true); r.StaticAnalysis.Stage.State != string(want.State) {
		t.Fatalf("static stage %+v, progress %+v", r.StaticAnalysis.Stage, want)
	}
}

// A published builder-1 revision (examples/report-go-vet.json) stays as
// stored: it validates, carries no section, and the Markdown and HTML
// renderers show the legacy sections under the current identity header.
func TestOldRevisionRendersFlat(t *testing.T) {
	b, err := os.ReadFile("../../examples/report-go-vet.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Builder.Version != "1" || r.hasSections() {
		t.Fatalf("builder %s sections %v", r.Builder.Version, r.hasSections())
	}
	d := Digest(b)
	md := RenderMarkdown(r, d, Korean)
	html, err := RenderHTML(r, d, Korean)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]byte{"testdata/report-go-vet.md": md, "testdata/report-go-vet.html": html} {
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: the flat rendering of an older revision changed", name)
		}
	}
}

func mustRegistry(t *testing.T) *Registry {
	t.Helper()
	return DefaultRegistry()
}

// The example's JSON, Markdown and HTML show the same section numbers, and
// every observation and finding appears once in the HTML (full and
// code-free export).
func TestExampleFormatsShowSameNumbers(t *testing.T) {
	full := exampleReport(t)
	for _, include := range []bool{true, false} {
		r := Project(full, include)
		b, err := Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		html, err := RenderHTML(r, Digest(b), Korean)
		if err != nil {
			t.Fatal(err)
		}
		md := string(RenderMarkdown(r, Digest(b), Korean))
		h := string(html)
		if strings.Count(h, `data-observation="`) != len(r.Observations) || strings.Count(h, `data-finding="`) != len(r.Findings) || strings.Count(md, "\n#### ") != len(r.Findings) {
			t.Fatalf("include_code=%v: rendered lists differ from the report", include)
		}
		for _, s := range r.Overview.Sections {
			for k, n := range s.Counts {
				if s.Key == SectionGaps {
					continue
				}
				if !strings.Contains(h, `data-count="`+s.Key+"."+k+`">`+strconv.Itoa(n)+"<") {
					t.Fatalf("html lacks %s.%s=%d", s.Key, k, n)
				}
				if !strings.Contains(md, "| "+mdText(Korean.Label("count."+s.Key+"."+k))+" | "+strconv.Itoa(n)+" |") {
					t.Fatalf("markdown lacks %s.%s=%d", s.Key, k, n)
				}
			}
		}
		for _, sec := range []string{"environment", "documents", "static_analysis", "metrics", "secrets", "code_review", "gaps"} {
			if !strings.Contains(h, `<section id="`+sec+`" data-section="`+sec+`"`) || !strings.Contains(md, "## "+mdText(Korean.Label("section."+sec))+"\n") {
				t.Fatalf("section %s not rendered", sec)
			}
		}
		statement := full.Documents.Contracts[0].Statement
		if !include && (strings.Contains(h, statement) || strings.Contains(md, mdText(statement))) {
			t.Fatal("code-free rendering carries a model statement")
		}
		// A task that did not run never reads as zero diagnostics.
		if !strings.Contains(md, "| `jscpd` |") || !strings.Contains(md, mdText(Korean.Label("not_run"))) {
			t.Fatal("not-run tool row missing its state")
		}
	}
}
