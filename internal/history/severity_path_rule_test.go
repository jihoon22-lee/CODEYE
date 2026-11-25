package history

import (
	"slices"
	"testing"
)

// TC-301: a tool finding whose severity differs only because the two
// reports applied another path rule (or one before the path rule) is not
// changed. The same fingerprint persists and the difference is one
// tool-context difference, recorded as the severity table with its path
// rule. With the same table and path rule a severity difference is still a
// change.
func TestPathRuleDifferenceIsToolContextNotAFindingChange(t *testing.T) {
	const now = "codeye-go-vet-default/1+paths.2"
	medium := vetFinding("f1", "a_test.go", 5, msg)
	medium.Severity = "medium"
	base := vetRun("base", "succeeded", "go1.27.4", []string{"a_test.go"}, medium)
	lowered := vetFinding("f1", "a_test.go", 5, msg)
	lowered.Severity = "low"
	cur := vetRun("cur", "succeeded", "go1.27.4", []string{"a_test.go"}, lowered)
	cur.Report.Coverage.Tools[0].SeverityTable = now
	for _, baseTable := range []string{"codeye-go-vet-default/1", "codeye-go-vet-default/1+paths.1"} {
		b := base
		b.Report.Coverage.Tools = slices.Clone(base.Report.Coverage.Tools)
		b.Report.Coverage.Tools[0].SeverityTable = baseTable
		c := Compare(cur, b)
		e := only(t, c, StatePersisting)
		if len(e.Changes) != 0 || c.Counts[StateChanged] != 0 {
			t.Fatalf("%s: entry %+v counts %v", baseTable, e, c.Counts)
		}
		i := slices.IndexFunc(c.Differences, func(d Difference) bool { return d.Key == "tool:go.vet:severity_table" })
		if i < 0 || *c.Differences[i].Baseline != baseTable || *c.Differences[i].Current != now {
			t.Fatalf("%s: differences %+v", baseTable, c.Differences)
		}
	}
	b := base
	b.Report.Coverage.Tools = slices.Clone(base.Report.Coverage.Tools)
	b.Report.Coverage.Tools[0].SeverityTable = now
	if e := only(t, Compare(cur, b), StateChanged); !slices.Equal(e.Changes, []string{"severity"}) {
		t.Fatalf("same table and path rule: %+v", e)
	}
}
