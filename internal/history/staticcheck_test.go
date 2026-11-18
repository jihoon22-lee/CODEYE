package history

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// W19 findings take part in W23 comparison like go vet's: identity from the
// shared report.ToolFingerprint, inspection from the staticcheck row's
// processed paths. A not-configured staticcheck (not_run, a known zero
// without paths) never resolves a vanished finding.
func scRun(run, status, exit string, processed []string, findings ...report.Finding) Side {
	s := vetRun(run, "succeeded", "go1.27.4", nil)
	tc := report.ToolCoverage{TaskKey: "staticcheck", Analyzer: "go.staticcheck", ToolVersion: strp("2026.2.2 (0.8.2)"), ExitClass: exit, FilesInScope: 1, ProcessedFiles: intp(len(processed)), ProcessedPaths: processed}
	if exit == "not_run" {
		tc.ToolVersion = nil
	}
	s.Report.Coverage.Tools = append(s.Report.Coverage.Tools, tc)
	s.Report.Tasks = append(s.Report.Tasks, report.Task{Key: "staticcheck", Status: status})
	if findings != nil {
		s.Report.Findings = findings
	}
	return s
}

func scFinding(id, path string, line int, message string) report.Finding {
	fp, b := report.ToolFingerprint("go.staticcheck", "SA4006", path, "", message)
	return report.Finding{ID: id, Fingerprint: fp, FingerprintBasis: &b, Severity: "low", Validation: "unreviewed", Category: "correctness", Disposition: "open",
		Locations: []report.Location{{Path: path, StartLine: line, EndLine: line}}}
}

func TestStaticcheckFindingsCompare(t *testing.T) {
	const m = "this value of err is never used"
	base := scRun("base", "succeeded", "success_with_findings", []string{"a.go"}, scFinding("f1", "a.go", 5, m))
	// Moved two lines: the same identity persists.
	c := Compare(scRun("cur", "succeeded", "success_with_findings", []string{"a.go"}, scFinding("f2", "a.go", 7, m)), base)
	if c.Counts[StatePersisting] != 1 {
		t.Fatalf("persisting %v", c.Counts)
	}
	// Re-checked by a successful staticcheck that processed a.go: resolved.
	c = Compare(scRun("cur", "succeeded", "success_without_findings", []string{"a.go"}), base)
	if e := only(t, c, StateResolvedObserved); e.Reason != nil {
		t.Fatalf("resolved %+v", e)
	}
	// Staticcheck not configured on the new node: absent, not resolved.
	c = Compare(scRun("cur", "unsupported", "not_run", nil), base)
	if e := only(t, c, StateAbsentUnverified); reason(e) == "" || c.Counts[StateResolvedObserved] != 0 {
		t.Fatalf("not configured %+v %v", e, c.Counts)
	}
}
