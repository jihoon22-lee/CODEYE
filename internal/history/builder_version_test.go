package history

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// Analysis summaries and additional evidence can change between revisions
// that still compare the same findings by fp2 and rule context.
func TestEarlierBuilderBaselinesAgainstCurrentBuilder(t *testing.T) {
	if report.BuilderVersion != "20" {
		t.Fatalf("builder version %s: revisit this test", report.BuilderVersion)
	}
	for _, version := range []string{"4", "12", "13", "14", "15", "16", "17", "18", "19"} {
		t.Run(version, func(t *testing.T) { checkHistoricalBaseline(t, version) })
	}
}

func checkHistoricalBaseline(t *testing.T, version string) {
	t.Helper()
	gone := vetFinding("f2", "b.go", 9, msg)
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, vetFinding("f1", "a.go", 5, msg), gone)
	base.Report.Builder.Version = version
	old := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go", "b.go"}, vetFinding("f1", "a.go", 5, msg), vetFinding("f3", "c.go", 2, msg))
	old.Report.Builder.Version = version
	cur := old
	cur.Report.Builder.Version = report.BuilderVersion
	cur.Report.Overview = &report.Overview{Sections: []report.OverviewSection{}, Attention: []report.Attention{}}
	cur.Report.StaticAnalysis = &report.StaticAnalysisSection{Languages: []report.StaticLanguage{}}
	cur.Report.PullRequest = &report.PullRequestSection{Number: 7, Items: []report.PullRequestItem{}}
	cur.Report.Documents = &report.DocumentsSection{Comparisons: []report.DocComparison{{Status: "match", Statement: "same behavior", Followup: true}}}
	cur.Report.Gaps = []report.Gap{{Section: report.SectionDocuments, Code: record.LimDocumentScopeItemsOmitted}}
	cur.Report.CodeReview = &report.CodeReviewSection{
		Strengths: []record.Strength{{Title: "guarded return"}},
		Ledger: &report.ReviewLedger{Files: []report.LedgerFile{{Path: "a.go", ModuleID: "go:.", TotalLines: 12,
			SentLines: 4, ReviewedLines: 4, Investigation: "accepted"}}},
	}
	cur.Report.Findings = slices.Clone(old.Report.Findings)
	cur.Report.Findings[0].Evidence = []report.Evidence{{Kind: "code", Excerpt: &sourceexcerpt.Excerpt{StartLine: 5, EndLine: 5, Text: "return nil"}}}
	want, got := Compare(old, base), Compare(cur, base)
	var extra []Difference
	builderChanged := false
	for _, d := range got.Differences {
		if d.Key == "builder_version" {
			if builderChanged || d.Baseline == nil || *d.Baseline != version || d.Current == nil || *d.Current != report.BuilderVersion {
				t.Fatalf("builder_version difference %+v", d)
			}
			builderChanged = true
			continue
		}
		extra = append(extra, d)
	}
	for _, d := range want.Differences {
		if d.Key == "builder_version" {
			t.Fatalf("same builder reported as a difference: %+v", d)
		}
	}
	if !builderChanged || len(extra) != len(want.Differences) {
		t.Fatalf("differences %+v, want %+v plus builder_version", got.Differences, want.Differences)
	}
	got.Differences, want.Differences = nil, nil
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatalf("builder %s compares differently against builder %s:\nsame builder %s\ncurrent builder %s", report.BuilderVersion, version, a, b)
	}
	if !got.Comparable || got.Counts["resolved_candidate"]+got.Counts["new"]+got.Counts["persisting"] == 0 {
		t.Fatalf("comparison %+v", got)
	}
}
