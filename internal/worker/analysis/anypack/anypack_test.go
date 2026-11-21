package anypack

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func TestScopeRoots(t *testing.T) {
	if !slices.Equal(Roots(domain.Scope{Kind: "full"}), []string{"."}) {
		t.Fatal("full scope")
	}
	got := Roots(domain.Scope{Kind: "paths", Paths: []string{"src/", "./src/a", "lib", "lib", "docs/x"}})
	if !slices.Equal(got, []string{"docs/x", "lib", "src"}) {
		t.Fatalf("roots %v", got)
	}
	if !slices.Equal(Roots(domain.Scope{Kind: "paths", Paths: []string{"src", "."}}), []string{"."}) {
		t.Fatal("dot covers all")
	}
	s := domain.Scope{Kind: "paths", Paths: []string{"src"}}
	if !InScope("src/a.go", s) || InScope("srcx/a.go", s) || !InScope("src", s) {
		t.Fatal("InScope")
	}
	for p, want := range map[string]bool{"vendor/a.go": true, "a/node_modules/b.js": true, ".github/x.py": true, "a/testdata/b.py": true, "src/vendored.go": false, ".env.py": false} {
		if UnderDataDir(p) != want {
			t.Fatalf("UnderDataDir(%q)", p)
		}
	}
}

// The first recorded failure wins, in the execution precedence order:
// uncertain cleanup, cancellation, timeout, then processes that never ran.
func TestOutcomePrecedence(t *testing.T) {
	code := 0
	ok := &executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed}
	timeout := &executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupConfirmed}
	uncertain := &executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupUncertain}
	var o Outcome
	o.Executions(false, ok, timeout, uncertain)
	if o.Reason != ReasonCleanupUncertain {
		t.Fatalf("precedence %+v", o)
	}
	var c Outcome
	c.Executions(true, timeout)
	if c.Status != domain.TaskCancelled {
		t.Fatalf("cancelled %+v", c)
	}
	doc := obsdoc.Document{Coverage: obsdoc.Coverage{ProcessedPaths: []string{"a.go"}}}
	var f Outcome
	f.Fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	f.Apply(&doc, true)
	if doc.Coverage.ProcessedPaths != nil || !doc.Outcome.PartialParse || doc.Outcome.TaskStatus != domain.TaskFailed {
		t.Fatalf("failed outcome %+v %+v", doc.Outcome, doc.Coverage)
	}
	var s Outcome
	m := obsdoc.Document{Observations: []obsdoc.Observation{{Metric: &obsdoc.Metric{Name: "x", Value: 2, Threshold: 1, Unit: "count"}}}}
	s.Apply(&m, true)
	if m.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings {
		t.Fatalf("metrics made findings: %+v", m.Outcome)
	}
}

func jobOf(entries []source.Entry) analysis.Job {
	var snap source.Snapshot
	snap.Manifest.Entries = entries
	return analysis.Job{Scope: domain.Scope{Kind: "full"}, Snapshot: snap}
}

func TestFilesSelection(t *testing.T) {
	size := int64(1)
	job := jobOf([]source.Entry{
		{Path: "b.go", Kind: "file", SHA256: "x", Size: &size},
		{Path: "a.go", Kind: "file", SHA256: "x", Size: &size},
		{Path: "vendor/v.go", Kind: "file", SHA256: "x", Size: &size},
		{Path: "x.go", Kind: "file", SHA256: "x", Size: &size, ExcludedReason: "secret_file_policy"},
		{Path: "dir", Kind: "directory", SHA256: "", Size: &size},
	})
	if got := Paths(Files(job, nil, true)); !slices.Equal(got, []string{"a.go", "b.go"}) {
		t.Fatalf("files %v", got)
	}
	if ExcludedDataDirFiles(job, nil) != 1 {
		t.Fatal("excluded count")
	}
}

func TestStructureFilesExcludeNamedThirdPartyDirectories(t *testing.T) {
	size := int64(1)
	job := jobOf([]source.Entry{
		{Path: "src/first.go", Kind: "file", SHA256: "x", Size: &size},
		{Path: "assets/third-party/bundle.js", Kind: "file", SHA256: "x", Size: &size},
		{Path: "Third_Party/lib.go", Kind: "file", SHA256: "x", Size: &size},
		{Path: "vendor/thirdparty/lib.go", Kind: "file", SHA256: "x", Size: &size},
		{Path: "src/thirdparty.go", Kind: "file", SHA256: "x", Size: &size},
	})
	files, excluded := StructureFiles(job, nil)
	if !slices.Equal(Paths(files), []string{"src/first.go", "src/thirdparty.go"}) || excluded != 2 || ExcludedDataDirFiles(job, nil) != 1 {
		t.Fatalf("selected %v, third-party exclusions %d", Paths(files), excluded)
	}
}
