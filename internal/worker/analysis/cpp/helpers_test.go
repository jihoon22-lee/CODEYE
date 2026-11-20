package cpp

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const (
	fixtureInput    = "../../../../fixtures/cpp/input"
	fixtureExpected = "../../../../fixtures/cpp/expected.json"
	// generatedHeader is what the fixture's configure step would write.
	generatedHeaderPath = "_build/gen/config.h"
	generatedHeader     = "#ifndef RINGBUF_CONFIG_H\n#define RINGBUF_CONFIG_H\n#define RINGBUF_VERSION \"1.0\"\n#endif\n"
)

// testTool returns the operator-installed tool named by env, or skips: CI
// has neither tool, and the adapters never search PATH or download a tool.
func testTool(t *testing.T, env, what string) string {
	t.Helper()
	p := os.Getenv(env)
	if p == "" {
		t.Skipf("set %s to the %s binary to run real-tool tests", env, what)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not found: %v", what, err)
	}
	return p
}

func newCppcheck(t *testing.T) *Cppcheck {
	t.Helper()
	a, err := NewCppcheck(Config{Executable: testTool(t, "CODEYE_TEST_CPPCHECK", "cppcheck (2.22.x)")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newClangTidy(t *testing.T, batch int) *ClangTidy {
	t.Helper()
	a, err := NewClangTidy(Config{Executable: testTool(t, "CODEYE_TEST_CLANG_TIDY", "clang-tidy (23.1.x)"), BatchSize: batch})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// fixtureFiles reads fixtures/cpp/input as a file map.
func fixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(fixtureInput, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixtureInput, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type fixture = packtest.Fixture

// newFixture acquires a snapshot of files and clones an attempt
// workspace like the worker.
func newFixture(t *testing.T, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	return packtest.NewFixture(t, "", files, scope)
}

// recordingRunner wraps the real executor, records specs and may alter
// results.
type recordingRunner struct {
	inner  executor.Runner
	mutate func(spec executor.Spec, r *executor.Result)
	specs  []executor.Spec
}

func (m *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	m.specs = append(m.specs, spec)
	r := m.inner.Run(ctx, spec)
	if m.mutate != nil {
		m.mutate(spec, &r)
	}
	return r
}

// readEnvelope reads and validates the task's observations artifact.
func readEnvelope(t *testing.T, job analysis.Job, res analysis.TaskResult) (obsdoc.Document, []byte) {
	t.Helper()
	return packtest.ReadEnvelope(t, job, res, "CODEYE_CPP_DUMP")
}

type planner interface {
	Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error)
	Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult
}

func runTask(t *testing.T, a planner, job analysis.Job, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	tasks, err := a.Plan(context.Background(), job)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("plan: %v %+v", err, tasks)
	}
	return a.Run(context.Background(), job, tasks[0].Spec, runner)
}

// captureStreams writes the check execution's streams to
// CODEYE_CPP_CAPTURE/<name>.{stdout,stderr,exit} (docs/ANALYZERS.md#실제-도구-시험과-출력-캡처): the replay
// tests run the parsers on these committed bytes in CI.
func captureStreams(t *testing.T, name string, res analysis.TaskResult, exec string) {
	t.Helper()
	dir := os.Getenv("CODEYE_CPP_CAPTURE")
	if dir == "" {
		return
	}
	for _, e := range res.Executions {
		if e.Name != exec {
			continue
		}
		code := "none"
		if e.Result.ExitCode != nil {
			code = strconv.Itoa(*e.Result.ExitCode)
		}
		for suffix, b := range map[string][]byte{".stdout": e.Result.Stdout.Data, ".stderr": e.Result.Stderr.Data, ".exit": []byte(code + "\n")} {
			if err := os.WriteFile(filepath.Join(dir, name+suffix), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	t.Fatalf("execution %s not found", exec)
}
