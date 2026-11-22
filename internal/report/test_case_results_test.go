package report

import (
	"bytes"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

func TestCaseResultsRequireMatchingExecutionAndHideNames(t *testing.T) {
	f := newTestFailureFixture(t)
	errout := ArtifactRef{ID: domain.NewID(), Role: analysis.RoleStderr, MediaType: "text/plain", RunID: f.in.Run.ID, AttemptID: f.in.Attempt.ID, SHA256: domain.Digest(nil)}
	f.in.Artifacts = append(f.in.Artifacts, errout)
	f.manifest.Tasks[0].Executions[1].StderrArtifactID = &errout.ID
	doc := resultdoc.Document{SchemaVersion: resultdoc.Version, RunID: f.in.Run.ID, AttemptID: f.in.Attempt.ID, Snapshot: f.in.Snapshot.ManifestDigest, TaskKey: "test.go", Runner: "go",
		StdoutSHA256: f.log.SHA256, StderrSHA256: errout.SHA256, Counts: resultdoc.Counts{Failed: 1}, Cases: []resultdoc.Case{{Name: "TestDistinctCase", Path: testFailureFixturePath, Line: 3, Status: resultdoc.Failed}}}
	setSidecar := func() {
		raw := canonical(t, doc)
		data := ArtifactRef{ID: domain.NewID(), RunID: doc.RunID, AttemptID: doc.AttemptID, Role: "other", MediaType: resultdoc.MediaType, SHA256: domain.Digest(raw), SizeBytes: int64(len(raw))}
		f.in.Artifacts = append(f.in.Artifacts, data)
		f.in.Bodies[data.ID] = raw
		f.manifest.Tasks[0].Artifacts = []analysis.ManifestArtifact{{ArtifactID: data.ID, Role: data.Role, MediaType: data.MediaType, SHA256: data.SHA256, SizeBytes: data.SizeBytes}}
		f.setManifest(t, f.manifest)
	}
	setSidecar()
	report, err := Build(f.in, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	row := testExecutions(report.Executions)["test.go"].TestCases
	if row == nil || row.Counts.Failed != 1 || len(row.Cases) != 1 {
		t.Fatalf("cases: %+v", row)
	}
	projected := Project(report, false)
	out, err := Marshal(projected)
	if err != nil || bytes.Contains(out, []byte("TestDistinctCase")) || testExecutions(projected.Executions)["test.go"].TestCases.Counts.Failed != 1 {
		t.Fatalf("invalid code-free projection: %v", err)
	}
	if len(row.Cases) != 1 {
		t.Fatal("redaction mutated stored report")
	}
	doc.StderrSHA256 = domain.Digest([]byte("unrelated stream"))
	setSidecar()
	rejected, err := Build(f.in, DefaultRegistry())
	if err != nil || testExecutions(rejected.Executions)["test.go"].TestCases != nil {
		t.Fatalf("mismatched stream accepted: %v", err)
	}
}

func TestDocComparisonIncludesOnlyCitedTestLocations(t *testing.T) {
	counts := resultdoc.Counts{Passed: 1, Failed: 1}
	e := Execution{TestCases: &TestCaseResults{Runner: "pytest", Counts: counts, Cases: []resultdoc.Case{
		{Name: "test_save", Status: resultdoc.Failed, Path: "tests/store.py", Line: 14},
		{Name: "test_read", Status: resultdoc.Passed, Path: "tests/store.py", Line: 40},
	}}}
	v := secView{testEvidence: testLines([]Execution{e})}
	notes := []SectionEvidence{{ID: "one", Kind: "test", Path: "tests/store.py", StartLine: 13, EndLine: 20}, {ID: "two", Kind: "test", Path: "tests/store.py", StartLine: 14, EndLine: 16},
		{ID: "unrelated", Kind: "test", Path: "tests/store.py", StartLine: 40, EndLine: 42}}
	got := v.citedTestCounts([]string{"one", "two"}, notes)
	if got.Failed != 1 || got.Passed != 0 {
		t.Fatalf("matched wrong test or counted overlapping cites twice: %+v", got)
	}
}

func TestReportedFailedCaseTakesPrecedenceOverZeroExit(t *testing.T) {
	e := Execution{Name: "test.run", Status: "exited", Cleanup: "confirmed", ExitCode: new(0), TestCases: &TestCaseResults{Counts: resultdoc.Counts{Failed: 1}}}
	if testResult(Task{Status: "failed", Reason: new("test_command_failed")}, e) != TestFailed {
		t.Fatal("failed assertion with successful wrapper exit was lost")
	}
}
