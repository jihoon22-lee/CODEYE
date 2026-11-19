package python

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real-tool tests need the operator-installed tools named by the
// environment; CI has none and the adapters never search PATH or download.
func testTool(t *testing.T, env string) string {
	t.Helper()
	p := os.Getenv(env)
	if p == "" {
		t.Skipf("set %s to run real-tool tests", env)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not found: %v", env, err)
	}
	return p
}

// testNode is the validated node toolchain handle for pyright.
func testNode(t *testing.T) *pack.Toolchain {
	t.Helper()
	p := testTool(t, "CODEYE_TEST_NODE")
	return &pack.Toolchain{Name: "node", Executable: p, Dir: filepath.Dir(p), Version: "test"}
}

func newRuffT(t *testing.T) *Ruff {
	t.Helper()
	a, err := NewRuff(Config{Executable: testTool(t, "CODEYE_TEST_RUFF")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newMypyT(t *testing.T) *Mypy {
	t.Helper()
	a, err := NewMypy(Config{Executable: testTool(t, "CODEYE_TEST_MYPY")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newPyrightT(t *testing.T) *Pyright {
	t.Helper()
	exe := testTool(t, "CODEYE_TEST_PYRIGHT")
	a, err := NewPyright(Config{Executable: exe, Node: testNode(t)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newBanditT(t *testing.T) *Bandit {
	t.Helper()
	a, err := NewBandit(Config{Executable: testTool(t, "CODEYE_TEST_BANDIT")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type fixture = packtest.Fixture

// newFixture acquires a snapshot of files and clones an attempt
// workspace like the worker.
func newFixture(t *testing.T, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	return packtest.NewFixture(t, "", files, scope)
}

// recordingRunner wraps a runner, records every spec and lets a test alter
// one result.
type recordingRunner struct {
	inner   executor.Runner
	mutate  func(spec executor.Spec, r *executor.Result)
	specs   []executor.Spec
	results []executor.Result
}

func (m *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	m.specs = append(m.specs, spec)
	r := m.inner.Run(ctx, spec)
	if m.mutate != nil {
		m.mutate(spec, &r)
	}
	m.results = append(m.results, r)
	return r
}

type planner interface {
	pack.Analyzer
}

// readEnvelope reads and validates the task's observations artifact.
func readEnvelope(t *testing.T, job analysis.Job, res analysis.TaskResult) (obsdoc.Document, []byte) {
	t.Helper()
	return packtest.ReadEnvelope(t, job, res, "CODEYE_PYTHON_DUMP")
}

func rulesOf(d obsdoc.Document, suppressed bool) map[string]int {
	out := map[string]int{}
	for _, o := range d.Observations {
		if o.Suppressed == suppressed {
			out[o.RuleID]++
		}
	}
	return out
}

func hasLimit(d obsdoc.Document, code string) bool {
	for _, l := range d.Limitations {
		if l == code {
			return true
		}
	}
	return false
}

// checkUnparsable is the outcome of a tree with unparsable files: the task
// succeeds on the files the tool could analyze (processed), every unparsable
// file is a syntax_error unit and never processed, the task records
// unparsable_files_not_analyzed and no tool error, and, where the tool
// reports a position (syntaxRule != ""), each unparsable file has exactly
// one syntax observation.
func checkUnparsable(t *testing.T, r realRun, syntaxRule string, processed []string, unparsable ...string) {
	t.Helper()
	if r.res.Status != domain.TaskSucceeded || !r.doc.Parse.OK || len(r.doc.ToolErrors) != 0 || !hasLimit(r.doc, LimUnparsable) ||
		r.doc.Coverage.Units[UnitSyntaxError] != len(unparsable) {
		t.Fatalf("outcome %s/%s units %+v limitations %v tool errors %+v\n%s", r.res.Status, r.res.Reason, r.doc.Coverage.Units, r.doc.Limitations, r.doc.ToolErrors, r.raw)
	}
	wantProcessed(t, r.doc, processed...)
	perFile := map[string]int{}
	for _, o := range r.doc.Observations {
		if syntaxRule != "" && o.RuleID == syntaxRule && o.Path != nil {
			perFile[*o.Path]++
			if o.Category != "correctness" || o.Suppressed || o.LocationState != obsdoc.LocationResolved {
				t.Fatalf("syntax observation %+v", o)
			}
		}
	}
	want := 0
	if syntaxRule != "" {
		want = 1
	}
	for _, f := range unparsable {
		if perFile[f] != want {
			t.Fatalf("%s: %d syntax observations, want %d (%v)", f, perFile[f], want, perFile)
		}
	}
}
