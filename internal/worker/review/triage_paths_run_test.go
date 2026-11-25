package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// pathAdmissionTree is a Go module of test files that sort before the
// shipped files (app/aNN_test.go, three functions each, each with a
// high-severity SA5011 on its first body line) and a few shipped files
// (svc/sNN.go, two functions each, the same diagnostic).
func pathAdmissionTree(testFiles, shipped int) (map[string]string, []string, []exampleDiag) {
	tree := map[string]string{"go.mod": "module example.test/paths\n\ngo 1.27\n"}
	var names []string
	var diags []exampleDiag
	add := func(name string, fns int) {
		var b strings.Builder
		b.WriteString("package paths\n\n")
		for k := range fns {
			fmt.Fprintf(&b, "func F%d(x *int, y int) int {\n\tif *x > y {\n\t\ty = *x + %d\n\t}\n\treturn y\n}\n", k, k)
			diags = append(diags, exampleDiag{path: name, line: 4 + 6*k, rule: "SA5011", category: "correctness", message: "possible nil pointer dereference (synthetic)"})
		}
		tree[name] = b.String()
		names = append(names, name)
	}
	// triageVet reports a go vet diagnostic on a.go line 4.
	tree["a.go"] = "package paths\n\nfunc A(x *int) int {\n\treturn *x\n}\n"
	names = append(names, "a.go")
	for i := range testFiles {
		add(fmt.Sprintf("app/a%02d_test.go", i+1), 3)
	}
	for i := range shipped {
		add(fmt.Sprintf("svc/s%02d.go", i+1), 2)
	}
	return tree, names, diags
}

// TC-302: at the evaluation ceiling of 20 calls, with many diagnostics in
// test files that sort first and a few on shipped code, the triage slots the
// call ceiling pays for classify the shipped code first; the test diagnostics
// they had no room for stay in the report as not triaged with the plan's
// limitation, their findings carry the lowered severity and reason, the
// counts come from the observations, and the model request still carries the
// table's default severity.
func TestTriageAtATightCeilingClassifiesShippedCodeFirst(t *testing.T) {
	tree, files, diags := pathAdmissionTree(40, 4)
	var sent []triageGroupInput
	run := runAdmission(t, tree, files, diags, 20, func(in triageInput) { sent = append(sent, in.Groups...) })
	r := run.report
	d := r.CodeReview.DiagnosticTriage
	if d == nil || run.results["review.triage.t001"].Status != "succeeded" {
		t.Fatalf("first triage slot: %+v", run.results["review.triage.t001"])
	}
	byID := map[string]report.Observation{}
	for _, o := range r.Observations {
		byID[o.ID] = o
	}
	triaged := map[string]bool{}
	for _, g := range d.Groups {
		for _, id := range g.ObservationIDs {
			triaged[id] = true
		}
	}
	shipped, test := 0, 0
	for _, o := range r.Observations {
		if o.Tool != "go.staticcheck" {
			continue
		}
		if strings.HasSuffix(o.Location.Path, "_test.go") {
			test++
			if o.PathClass != "test" {
				t.Fatalf("test file observation has class %q", o.PathClass)
			}
			continue
		}
		shipped++
		if o.PathClass != "" || !triaged[o.ID] {
			t.Fatalf("shipped diagnostic %s at %s: class %q, triaged %v", o.ID, o.Location.Path, o.PathClass, triaged[o.ID])
		}
	}
	if shipped != 8 || test != 120 {
		t.Fatalf("diagnostics: %d shipped, %d in test files", shipped, test)
	}
	if d.Triaged >= d.Diagnostics || !slices.Contains(d.Limitations, limDiagnosticsOutside) && run.results["review.triage.t002"].Status != "budget_exhausted" {
		t.Fatalf("triaged %d of %d, limitations %v, slot 2 %s", d.Triaged, d.Diagnostics, d.Limitations, run.results["review.triage.t002"].Status)
	}
	// What the request told the model is the severity the report gives the
	// findings: the table's value on shipped code, lowered on a test path,
	// with the path class named.
	var firstPaths []string
	for _, g := range sent[:min(len(sent), 6)] {
		firstPaths = append(firstPaths, g.Path)
	}
	if len(sent) < 5 || !slices.Equal(firstPaths[:4], []string{"svc/s01.go", "svc/s02.go", "svc/s03.go", "svc/s04.go"}) {
		t.Fatalf("first request groups %v", firstPaths)
	}
	for _, g := range sent {
		wantSeverity, wantClass := "high", ""
		if record.PathClass(g.Path) != "" {
			wantSeverity, wantClass = "low", record.PathClass(g.Path)
		}
		if g.DefaultSeverity != wantSeverity || g.PathClass != wantClass {
			t.Fatalf("group %s at %s has default severity %s and path class %q", g.ID, g.Path, g.DefaultSeverity, g.PathClass)
		}
	}
	// The findings: lowered on test paths with their reason, unchanged on shipped code.
	high, lowered := severity.Result{Family: "null_dereference"}, severity.Result{Family: "null_dereference", Lowered: true}
	for _, f := range r.Findings {
		if len(f.ObservationIDs) == 0 || !strings.HasPrefix(f.Title, "go.staticcheck SA5011") {
			continue
		}
		if strings.HasSuffix(f.Locations[0].Path, "_test.go") {
			if f.Severity != "low" || f.SeverityReason != severity.Reason(lowered) {
				t.Fatalf("test finding %s: %s %q", f.Locations[0].Path, f.Severity, f.SeverityReason)
			}
		} else if f.Severity != "high" || f.SeverityReason != severity.Reason(high) {
			t.Fatalf("shipped finding %s: %s %q", f.Locations[0].Path, f.Severity, f.SeverityReason)
		}
	}
	// The overview counts the diagnostics off shipped code from the observations.
	notShipped := 0
	for _, o := range byID {
		if o.PathClass != "" && !o.Suppressed && o.Metric == nil {
			notShipped++
		}
	}
	found := false
	for _, s := range r.Overview.Sections {
		if s.Key == report.SectionCodeReview {
			found = s.Counts["diagnostics_not_shipped"] == notShipped && notShipped >= test
		}
	}
	if !found {
		t.Fatalf("overview count of diagnostics off shipped code, want %d", notShipped)
	}
	md := strings.ReplaceAll(string(report.RenderMarkdownAt(r, "", report.Korean, report.DetailSummary)), `\`, "")
	if !strings.Contains(md, fmt.Sprintf("경로별: 시험·예제 등 배포 코드가 아닌 경로 %d건", notShipped)) {
		t.Fatal("the fold does not say how many diagnostics are off shipped code")
	}
}
