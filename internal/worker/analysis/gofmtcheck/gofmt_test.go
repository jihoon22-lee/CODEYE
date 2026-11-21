package gofmtcheck

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestReadOnlyGoFmtCoverage(t *testing.T) {
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	a, err := New(filepath.Join(runtime.GOROOT(), "bin", goName))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body   string
		status       domain.TaskStatus
		exit         string
		observations int
	}{
		{"clean", "package p\n", domain.TaskSucceeded, obsdoc.ExitSuccessWithoutFindings, 0},
		{"mismatch", "package p\nfunc f( ) { }\n", domain.TaskSucceeded, obsdoc.ExitSuccessWithFindings, 1},
		{"syntax error", "package p\nfunc f( { }\n", domain.TaskFailed, obsdoc.ExitExecutionError, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := packtest.NewFixture(t, "", map[string]string{
				"-sample.go":          tc.body,
				"go.mod":              "module example.test/format\n\ngo 1.22\nrequire example.test/unavailable v1.0.0\n",
				"testdata/ignored.go": "not valid Go source\n",
			}, domain.Scope{})
			job := fixture.Job
			before := packtest.TreeHash(t, job.Snapshot.WorkspaceDir)
			res := packtest.RunTask(t, a, job, Name, &executor.Executor{})
			doc, _ := packtest.ReadEnvelope(t, job, res, "CODEYE_TEST_DUMP_GOFMT")
			if res.Status != tc.status || doc.Outcome.ExitClass != tc.exit || len(doc.Observations) != tc.observations || doc.Coverage.FilesInScope == nil || *doc.Coverage.FilesInScope != 1 || doc.Coverage.Units["excluded_data_directory"] != 1 {
				t.Fatalf("result = %+v outcome = %+v coverage = %+v", res.Status, doc.Outcome, doc.Coverage)
			}
			if tc.status == domain.TaskSucceeded && len(doc.Coverage.ProcessedPaths) != 1 || tc.status != domain.TaskSucceeded && doc.Coverage.ProcessedPaths != nil {
				t.Fatalf("processed = %v", doc.Coverage.ProcessedPaths)
			}
			if len(doc.Observations) > 0 && (doc.Observations[0].Location == nil || doc.Observations[0].Location.Path != "-sample.go" || doc.Observations[0].RuleID != Rule) {
				t.Fatalf("observation = %+v", doc.Observations[0])
			}
			if before != packtest.TreeHash(t, job.Snapshot.WorkspaceDir) {
				t.Fatal("format check modified the workspace")
			}
		})
	}
}
