package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestWithTestsReportRequiresAcceptedExecution(t *testing.T) {
	for _, tc := range []struct {
		name, taskStatus, want string
		exit                   *int
		unsupported            bool
	}{
		{name: "verified zero exit", taskStatus: "succeeded", exit: new(0), want: TestPassed},
		{name: "reported success without execution", taskStatus: "succeeded", want: TestNotRun},
		{name: "policy skipped", taskStatus: "skipped_policy", want: TestNotRun},
		{name: "verified failure", taskStatus: "failed", exit: new(1), want: TestFailed},
		{name: "one test unavailable", taskStatus: "succeeded", exit: new(0), unsupported: true, want: TestPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specs := []taskSpec{goVet(t), {key: "test.go", status: tc.taskStatus, required: true}}
			if tc.unsupported {
				specs = append(specs, taskSpec{key: "test.python", status: "unsupported", required: true})
			}
			f := newFixture(t, specs...)
			f.in.Run.Profile = "with-tests"
			for i := 1; i < len(f.in.Tasks); i++ {
				f.in.Tasks[i].Stage = string(domain.StageTest)
			}
			if tc.exit != nil {
				var manifest analysis.ResultManifest
				if err := json.Unmarshal(f.in.Bodies[f.in.ResultManifest.ID], &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Tasks[1].Executions = []analysis.ManifestExecution{{
					Name: "test.run", Status: executor.StatusExited, ExitCode: tc.exit, Cleanup: executor.CleanupConfirmed,
					Command: analysis.ManifestCommand{Argv: []string{"go", "test", "./..."}, Dir: ".", EnvKeys: []string{}},
				}}
				f.setManifest(t, manifest)
			}
			r := build(t, f.in, DefaultRegistry())
			for _, language := range r.StaticAnalysis.Languages {
				for _, tool := range language.Tools {
					if testTaskKey(tool.TaskKey) {
						t.Fatalf("test execution leaked into static analysis: %s", tool.TaskKey)
					}
				}
			}
			if r.Coverage.TestStatus != tc.want || has(r.Limitations, LimArtifactMissing+":test.go") || has(r.Coverage.Notes, NoteNoTestsPlanned) {
				t.Fatalf("test coverage = %s, limitations = %v, notes = %v", r.Coverage.TestStatus, r.Limitations, r.Coverage.Notes)
			}
			if has(r.Limitations, LimTestExecutionUnverified) != (tc.taskStatus == "succeeded" && tc.exit == nil) {
				t.Fatalf("test limitations = %v", r.Limitations)
			}
			if tc.want != TestPassed && r.Status == StatusCompleted {
				t.Fatal("a run with incomplete tests received a completed report")
			}
			projected := Project(r, false)
			if err := ValidateReport(projected); err != nil {
				t.Fatal(err)
			}
			digest := strings.Repeat("0", 64)
			for _, level := range []string{DetailSummary, DetailFull} {
				html, err := RenderHTMLAt(projected, digest, Korean, level)
				md := RenderMarkdownAt(projected, digest, Korean, level)
				want := Korean.Label("tests.summary." + tc.want)
				if tc.taskStatus == "skipped_policy" {
					want = Korean.Label("tests.not_started")
				}
				if err != nil || !strings.Contains(string(html), want) || !strings.Contains(string(md), want) {
					t.Fatalf("missing test result in %s exports (%v)", level, err)
				}
			}
		})
	}
}
