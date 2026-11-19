package history

import (
	"encoding/json"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// A builder-4 baseline against a current-builder run compares exactly like
// two builder-4 revisions: the analysis sections builder 5 adds and the
// review slices and ledger builder 6 reads and the dependency records
// builder 7 reads are not read by the comparison
// (slice calls share the role and pack digest of the whole-run role), and
// the fp2 and rule-context rules hold for every version. Only the
// builder_version difference row tells them apart.
func TestBuilderFourBaselineAgainstCurrentBuilder(t *testing.T) {
	gone := vetFinding("f2", "b.go", 9, msg)
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, vetFinding("f1", "a.go", 5, msg), gone)
	base.Report.Builder.Version = "4"
	old := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, vetFinding("f1", "a.go", 5, msg), vetFinding("f3", "c.go", 2, msg))
	old.Report.Builder.Version = "4"
	cur := old
	cur.Report.Builder.Version = report.BuilderVersion
	cur.Report.Overview = &report.Overview{Sections: []report.OverviewSection{}, Attention: []report.Attention{}}
	cur.Report.StaticAnalysis = &report.StaticAnalysisSection{Languages: []report.StaticLanguage{}}
	if report.BuilderVersion != "7" {
		t.Fatalf("builder version %s: revisit this test", report.BuilderVersion)
	}

	want, got := Compare(old, base), Compare(cur, base)
	var extra []Difference
	for _, d := range got.Differences {
		if d.Key == "builder_version" {
			if d.Baseline == nil || *d.Baseline != "4" || d.Current == nil || *d.Current != report.BuilderVersion {
				t.Fatalf("builder_version difference %+v", d)
			}
			continue
		}
		extra = append(extra, d)
	}
	for _, d := range want.Differences {
		if d.Key == "builder_version" {
			t.Fatalf("same builder reported as a difference: %+v", d)
		}
	}
	if len(extra) != len(want.Differences) {
		t.Fatalf("differences %+v, want %+v plus builder_version", got.Differences, want.Differences)
	}
	got.Differences, want.Differences = nil, nil
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatalf("builder %s compares differently:\n4-4 %s\n4-current %s", report.BuilderVersion, a, b)
	}
	if !got.Comparable || got.Counts["resolved_candidate"]+got.Counts["new"]+got.Counts["persisting"] == 0 {
		t.Fatalf("comparison %+v", got)
	}
}
