package history

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// A builder-4 baseline against a current-builder run compares exactly like
// two builder-4 revisions: the analysis sections builder 5 adds and the
// review slices and ledger builder 6 reads and the dependency records
// builder 7 reads, the review priority classes builder 8 reads, the
// pull_request section builder 9 writes and the code review section's pack
// field builder 10 drops are not read by the comparison (slice calls share
// the role and pack digest of the whole-run role). The document comparison,
// followup marker and source excerpts also belong to the report presentation.
// The fp2 and rule-context rules hold for every version. Only the builder_version
// difference row tells them apart.
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
	cur.Report.PullRequest = &report.PullRequestSection{Number: 7, Items: []report.PullRequestItem{}}
	cur.Report.Documents = &report.DocumentsSection{Comparisons: []report.DocComparison{{Status: "match", Statement: "same behavior", Followup: true}}}
	cur.Report.CodeReview = &report.CodeReviewSection{Strengths: []record.Strength{{Title: "guarded return"}}}
	cur.Report.Findings = slices.Clone(old.Report.Findings)
	cur.Report.Findings[0].Evidence = []report.Evidence{{Kind: "code", Excerpt: &sourceexcerpt.Excerpt{StartLine: 5, EndLine: 5, Text: "return nil"}}}
	if report.BuilderVersion != "12" {
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
