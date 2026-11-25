package report

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// TC-301: a tool diagnostic on a test, example, benchmark, generated or
// vendored path gets the family's lowered severity with the fixed reason that
// says why, the observation records the class of its path, the tool row
// records the table with its path rule, and the report refuses a class that
// its location does not give or a class in a revision before builder 23. The
// raw tool grade stays untouched.
func TestToolFindingOnATestPathIsLowered(t *testing.T) {
	f := analyzerFixture(t, nil, goVet(t), staticcheckTask(t))
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	classes := map[string]string{}
	lowered, shipped := 0, 0
	for _, o := range r.Observations {
		if o.Location == nil {
			continue
		}
		if want := record.PathClass(o.Location.Path); o.PathClass != want {
			t.Fatalf("observation %s at %s has class %q, want %q", o.ID, o.Location.Path, o.PathClass, want)
		}
		classes[o.ID] = o.PathClass
	}
	for _, x := range r.Findings {
		if len(x.ObservationIDs) == 0 {
			continue
		}
		fam := ""
		for _, fm := range severity.Families() {
			if x.SeverityReason == severity.Reason(severity.Result{Family: fm.Name}) || x.SeverityReason == severity.Reason(severity.Result{Family: fm.Name, Lowered: true}) {
				fam = fm.Name
			}
		}
		if fam == "" {
			continue
		}
		fm, _ := severity.FamilyOf(fam)
		if classes[x.ObservationIDs[0]] != "" {
			wantSev := fm.Lowered
			if severity.Rank(fm.Lowered) >= severity.Rank(fm.Severity) {
				wantSev = fm.Severity
			}
			if x.Severity != wantSev {
				t.Fatalf("finding on %s: severity %s, want %s: %+v", x.Locations[0].Path, x.Severity, wantSev, x)
			}
			if severity.Rank(fm.Lowered) < severity.Rank(fm.Severity) {
				lowered++
				if x.SeverityReason != severity.Reason(severity.Result{Family: fam, Lowered: true}) {
					t.Fatalf("lowered finding without its reason: %q", x.SeverityReason)
				}
			}
		} else {
			shipped++
			if x.Severity != fm.Severity || x.SeverityReason != severity.Reason(severity.Result{Family: fam}) {
				t.Fatalf("finding on shipped code %s: %+v", x.Locations[0].Path, x)
			}
		}
	}
	if lowered == 0 || shipped == 0 {
		t.Fatalf("fixture must hold lowered (%d) and shipped (%d) findings", lowered, shipped)
	}
	for _, tc := range r.Coverage.Tools {
		if tc.SeverityTable != "" && !strings.HasSuffix(tc.SeverityTable, "+"+severity.PathRule) {
			t.Fatalf("tool row %s records table %q without the path rule", tc.TaskKey, tc.SeverityTable)
		}
	}

	// The code-free export keeps the fixed reasons and the classes.
	free := Project(r, false)
	for i, x := range free.Findings {
		if len(x.ObservationIDs) > 0 && x.SeverityReason != r.Findings[i].SeverityReason {
			t.Fatalf("code-free export replaced the severity reason %q", r.Findings[i].SeverityReason)
		}
	}
	for i, o := range free.Observations {
		if o.PathClass != r.Observations[i].PathClass {
			t.Fatal("code-free export lost a path class")
		}
	}

	// A class its location does not give, and a class before builder 23.
	bad := clone(t, r)
	for i, o := range bad.Observations {
		if o.Location != nil && o.PathClass == "" {
			bad.Observations[i].PathClass = record.PathTest
			break
		}
	}
	if err := ValidateReport(bad); err == nil || !strings.Contains(err.Error(), "path class") {
		t.Fatalf("wrong class accepted: %v", err)
	}
	old := clone(t, r)
	old.Builder.Version = "22"
	if err := ValidateReport(old); err == nil || !strings.Contains(err.Error(), "path class") {
		t.Fatalf("class in a builder 22 report accepted: %v", err)
	}
}

// TC-303: the overview of a report with a diagnostic triage counts the
// diagnostics on paths that are not shipped code from the observations'
// stored class, only from builder 23 on.
func TestOverviewCountsDiagnosticsOffShippedCode(t *testing.T) {
	f := analyzerFixture(t, nil, goVet(t), staticcheckTask(t))
	r := build(t, f.in, DefaultRegistry())
	off := 0
	for _, o := range r.Observations {
		if o.PathClass != "" && !o.Suppressed && o.Metric == nil {
			off++
		}
	}
	if off == 0 {
		t.Fatal("fixture holds no diagnostic off shipped code")
	}
	r.CodeReview = &CodeReviewSection{DiagnosticTriage: &DiagnosticTriage{}}
	count := func(r Report) (int, bool) {
		for _, s := range overviewOf(r).Sections {
			if s.Key == SectionCodeReview {
				n, ok := s.Counts["diagnostics_not_shipped"]
				return n, ok
			}
		}
		return 0, false
	}
	if n, ok := count(r); !ok || n != off {
		t.Fatalf("overview counts %d (present %v), want %d", n, ok, off)
	}
	r.Builder.Version = "22"
	if _, ok := count(r); ok {
		t.Fatal("builder 22 overview has the count")
	}
}
