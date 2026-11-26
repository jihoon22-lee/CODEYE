package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// An investigation offered leads publishes its record as schema 0.9 whether
// or not it names a lead, so every report section an investigation record
// feeds must accept that decoder: the workflow reading, the contract probe
// and its follow-up comparison here, and the strengths, the diagnostic
// triage and the module slots in the two example reports
// (TestReportExample, TestReportSlicesExample).
func TestLeadOfferedRecordsFeedTheReportSections(t *testing.T) {
	t.Run("workflow reading", func(t *testing.T) {
		j := workflowReadingPaddedJob(t)
		r, _ := reportAt(t, &j, vetStub{}, workflowReadingCaller(t), smallBudget, &exampleIDs{n: 0xd00})
		if requireLeadOfferedOverview(t, j).WorkflowReading == nil {
			t.Fatal("the overview did not read the workflow")
		}
		requireLeadsRecords(t, r, "review.investigate")
		if len(r.Documents.Comparisons) == 0 || r.Documents.Comparisons[0].RelatedWorkflow == nil {
			t.Fatalf("the related workflow was not carried: %+v", r.Documents.Comparisons)
		}
		requireValidProjections(t, r)
	})
	t.Run("contract probe and follow-up", func(t *testing.T) {
		j, caller := workflowContractRun(t)
		r, results := reportAt(t, &j, vetStub{}, caller, smallBudget, &exampleIDs{n: 0xe00})
		inv := requireLeadOfferedOverview(t, j)
		if inv.ContractProbe == nil || results[record.TaskContract].Status != "succeeded" {
			t.Fatalf("probe %+v, follow-up %+v", inv.ContractProbe, results[record.TaskContract])
		}
		requireLeadsRecords(t, r, "review.investigate")
		first := r.Documents.Comparisons[0]
		if !first.Followup || first.Status != "match" {
			t.Fatalf("the follow-up comparison was not carried: %+v", first)
		}
		requireValidProjections(t, r)
	})
}

// workflowReadingPaddedJob is the workflow-reading fixture with forty long
// files beside it, so the overview at the smallest request leaves files out
// and is offered leads.
func workflowReadingPaddedJob(t *testing.T) analysis.Job {
	t.Helper()
	tree := dirTree(t, workflowReadingFixture)
	line := "// " + strings.Repeat("shelf padding ", 8) + "\n"
	for i := range 40 {
		tree[fmt.Sprintf("archive/pad%02d.go", i)] = fmt.Sprintf("package archive\n\n%sfunc pad%02d() int { return %d }\n", strings.Repeat(line, 60), i, i)
	}
	return treeJob(t, tree)
}

// requireLeadOfferedOverview fails unless the overview record was offered
// leads.
func requireLeadOfferedOverview(t *testing.T, j analysis.Job) record.Record {
	t.Helper()
	inv := readRecord(t, j, record.StageInvestigate)
	if inv.Schema != record.SchemaVersionLeads || inv.LeadLimit != record.MaxLeads {
		t.Fatalf("overview schema %s, lead limit %d", inv.Schema, inv.LeadLimit)
	}
	return inv
}

// requireLeadsRecords fails unless each task key published its review
// record under the lead-offering decoder and the report set no review
// record aside.
func requireLeadsRecords(t *testing.T, r report.Report, keys ...string) {
	t.Helper()
	for _, l := range r.Limitations {
		if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, "artifact_rejected:review.") {
			t.Fatalf("limitation %s in %v", l, r.Limitations)
		}
	}
	want := (report.LeadsReviewDecoder{}).Key().String()
	for _, key := range keys {
		if !slices.ContainsFunc(r.Artifacts, func(a report.Artifact) bool { return deref(a.TaskKey) == key && deref(a.Decoder) == want }) {
			t.Fatalf("%s has no record under %s", key, want)
		}
	}
}

// requireValidProjections validates the report and both of its exports.
func requireValidProjections(t *testing.T, r report.Report) {
	t.Helper()
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	for _, include := range []bool{true, false} {
		if err := report.ValidateReport(report.Project(r, include)); err != nil {
			t.Fatalf("include_code=%v: %v", include, err)
		}
	}
}
