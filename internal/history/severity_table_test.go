package history

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// A tool finding whose default severity comes from a severity table the
// baseline did not apply (an earlier builder, or another table version) is
// not changed: the same fingerprint persists, and the table difference is
// one tool-context difference of the comparison, not a per-finding change.
func TestSeverityTableDifferenceIsToolContextNotAFindingChange(t *testing.T) {
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a.go"}, vetFinding("f1", "a.go", 5, msg))
	base.Report.Builder.Version = "21"
	high := vetFinding("f1", "a.go", 5, msg)
	high.Severity = "high"
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a.go"}, high)
	cur.Report.Coverage.Tools[0].SeverityTable = "go.vet/1"
	for _, tc := range []struct {
		name, baseTable string
	}{{"no table", ""}, {"other table", "go.vet/0"}} {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			b.Report.Coverage.Tools = slices.Clone(base.Report.Coverage.Tools)
			b.Report.Coverage.Tools[0].SeverityTable = tc.baseTable
			c := Compare(cur, b)
			e := only(t, c, StatePersisting)
			if len(e.Changes) != 0 || c.Counts[StateChanged] != 0 {
				t.Fatalf("entry %+v counts %v", e, c.Counts)
			}
			i := slices.IndexFunc(c.Differences, func(d Difference) bool { return d.Key == "tool:go.vet:severity_table" })
			want := tc.baseTable
			if want == "" {
				want = "none"
			}
			if i < 0 || *c.Differences[i].Baseline != want || *c.Differences[i].Current != "go.vet/1" {
				t.Fatalf("differences %+v", c.Differences)
			}
		})
	}
	// The same table on both sides: a severity difference is a change.
	b := base
	b.Report.Coverage.Tools = slices.Clone(base.Report.Coverage.Tools)
	b.Report.Coverage.Tools[0].SeverityTable = "go.vet/1"
	if e := only(t, Compare(cur, b), StateChanged); !slices.Equal(e.Changes, []string{"severity"}) {
		t.Fatalf("same table: %+v", e)
	}
	// A model finding's severity difference is always a change.
	mb, mc := base, cur
	mb.Report.Coverage.Tools = slices.Clone(base.Report.Coverage.Tools)
	fb, fc := vetFinding("m", "a.go", 5, msg), vetFinding("m", "a.go", 5, msg)
	fb.FingerprintBasis.Producer, fc.FingerprintBasis.Producer = report.ProducerModel, report.ProducerModel
	fc.Severity = "high"
	mb.Report.Findings, mc.Report.Findings = []report.Finding{fb}, []report.Finding{fc}
	if e := only(t, Compare(mc, mb), StateChanged); !slices.Equal(e.Changes, []string{"severity"}) {
		t.Fatalf("model finding: %+v", e)
	}
}
