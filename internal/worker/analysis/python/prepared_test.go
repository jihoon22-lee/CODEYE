package python

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// typedPackage is a typed third-party package (py.typed) the project
// misuses: with it prepared, a checker reports the misuse instead of an
// unresolved import.
var typedPackage = map[string]string{
	"fakepkg/__init__.py": "def double(x: int) -> int:\n    return 2 * x\n",
	"fakepkg/py.typed":    "",
}

var preparedProject = map[string]string{
	"pyproject.toml":  "[project]\nname = \"app\"\nrequires-python = \">=3.11\"\ndependencies = [\"fakepkg==1.0\"]\n",
	"app/__init__.py": "",
	"app/use.py":      "import fakepkg\n\n\ndef run() -> int:\n    return fakepkg.double(\"two\")\n",
}

// writePrepared stores a tree and the record of the module's deps.python
// task the way the dependency task leaves them; minor is the interpreter
// minor of the site-packages directory.
func writePrepared(t *testing.T, job analysis.Job, cacheRoot, kind, state, minor string) string {
	t.Helper()
	var tree string
	cache := depdoc.Cache{Kind: kind}
	switch kind {
	case depdoc.CacheShared:
		key := "python/trees/0123456789abcdef0123456789abcdef"
		cache.Key = &key
		tree = filepath.Join(cacheRoot, filepath.FromSlash(key))
	case depdoc.CacheAttempt:
		tree = depdoc.AttemptTree(job.Scratch, "deps.python")
	}
	if tree != "" {
		packtest.WriteTree(t, filepath.Join(tree, SitePackagesRel(minor)), typedPackage)
	}
	status, reason := string(domain.TaskSucceeded), (*string)(nil)
	if state != depdoc.StatePrepared {
		r := "no_binary_distribution"
		status, reason = string(domain.TaskFailed), &r
	}
	one := 1
	rec := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		TaskKey: "deps.python", Module: depdoc.Module{ID: "python:.", Ecosystem: "python", Path: "."}, Manager: depdoc.Manager{Name: "uv"},
		State: state, Reason: reason, TaskStatus: status, Counts: depdoc.Counts{Declared: &one, Prepared: 1}, Cache: cache,
		DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	b, err := depdoc.Marshal(rec, depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: "deps.python"})
	if err != nil {
		t.Fatal(err)
	}
	p := depdoc.Path(job.Scratch, "deps.python")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return tree
}

// fakeChecker answers the version probe and records the check; the check
// itself exits 0 without output.
type fakeChecker struct {
	specs []executor.Spec
	out   string
}

func (f *fakeChecker) run(s executor.Spec) executor.Result {
	f.specs = append(f.specs, s)
	out := f.out
	if slices.Contains(s.Args, "--version") {
		out = "mypy 2.4.1 (compiled: yes)\n"
		if len(s.Args) > 1 {
			out = "pyright 1.1.414\n"
		}
	}
	code := 0
	return executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Stdout: executor.Stream{Data: []byte(out), TotalBytes: int64(len(out))}}
}

// runnerFunc is a runner answering every spec with a function.
type runnerFunc func(executor.Spec) executor.Result

func (f runnerFunc) Run(_ context.Context, s executor.Spec) executor.Result { return f(s) }

func fakeTool(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// mypy and pyright read a tree only through a usable record of this
// attempt; without one their argv, selection and limitations are the ones
// of a node that prepares nothing.
func TestTypeCheckersReadPreparedTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mypy shim links the interpreter (Unix)")
	}
	for name, tc := range map[string]struct {
		kind, state string
		used        bool
		lim         string
	}{
		"no record":   {"", "", false, LimNoThirdParty},
		"shared":      {depdoc.CacheShared, depdoc.StatePrepared, true, LimThirdPartyPrepared},
		"attempt":     {depdoc.CacheAttempt, depdoc.StatePrepared, true, LimThirdPartyPrepared},
		"partial":     {depdoc.CacheShared, depdoc.StatePartial, true, LimThirdPartyPartial},
		"no tree":     {depdoc.CacheNone, depdoc.StatePartial, false, LimNoThirdParty},
		"failed tree": {depdoc.CacheShared, depdoc.StateFailed, false, LimNoThirdParty},
	} {
		t.Run(name, func(t *testing.T) {
			cacheRoot := filepath.Join(t.TempDir(), "deps")
			interpreter := "/usr/bin/python3"
			fx := newFixture(t, preparedProject, domain.Scope{})
			tree := ""
			if tc.kind != "" {
				tree = writePreparedState(t, fx.Job, cacheRoot, tc.kind, tc.state)
			}
			mypy, err := NewMypy(Config{Executable: fakeTool(t, "mypy"), DependencyCacheRoot: cacheRoot, Interpreter: interpreter})
			if err != nil {
				t.Fatal(err)
			}
			f := &fakeChecker{}
			res := packtest.RunTask(t, mypy, fx.Job, "mypy", runnerFunc(f.run))
			doc, _ := readEnvelope(t, fx.Job, res)
			check := f.specs[len(f.specs)-1]
			shim := filepath.Join(fx.Job.Scratch, "analyzers", "mypy", "cfg", "venv", "bin", "python")
			if tc.used {
				if slices.Contains(check.Args, "--no-site-packages") || !slices.Contains(check.Args, "--python-executable="+shim) ||
					!slices.Contains(doc.RulePack.Selection, "--python-executable=codeye-prepared") || !slices.Contains(check.ProtectedRoots, tree) {
					t.Fatalf("argv %v selection %v protected %v", check.Args, doc.RulePack.Selection, check.ProtectedRoots)
				}
				if target, err := os.Readlink(shim); err != nil || target != interpreter {
					t.Fatalf("shim interpreter %q %v", target, err)
				}
				cfg, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(shim)), "pyvenv.cfg"))
				if string(cfg) != "home = /usr/bin\ninclude-system-site-packages = false\n" {
					t.Fatalf("pyvenv.cfg %q", cfg)
				}
			} else if !slices.Equal(doc.RulePack.Selection, MypySelection) || !slices.Contains(check.Args, "--no-site-packages") {
				t.Fatalf("argv %v selection %v", check.Args, doc.RulePack.Selection)
			}
			if !hasLimit(doc, tc.lim) || (tc.used && hasLimit(doc, LimNoThirdParty)) {
				t.Fatalf("limitations %v, want %s", doc.Limitations, tc.lim)
			}
			pyright, err := NewPyright(Config{Executable: fakeJS(t), Node: &pack.Toolchain{Name: "node", Executable: fakeTool(t, "node"), Dir: t.TempDir()}, DependencyCacheRoot: cacheRoot})
			if err != nil {
				t.Fatal(err)
			}
			f = &fakeChecker{out: `{"version":"1.1.414","time":"0","generalDiagnostics":[],"summary":{"filesAnalyzed":2,"errorCount":0,"warningCount":0,"informationCount":0,"timeInSec":0.1}}`}
			res = packtest.RunTask(t, pyright, fx.Job, "pyright", runnerFunc(f.run))
			doc, _ = readEnvelope(t, fx.Job, res)
			var cfg map[string]any
			b, _ := os.ReadFile(filepath.Join(fx.Job.Scratch, "analyzers", "pyright", "cfg", "pyrightconfig.json"))
			if err := json.Unmarshal(b, &cfg); err != nil {
				t.Fatal(err)
			}
			wantPath, wantVenv := "venvs", emptyVenv
			if tc.used {
				wantPath, wantVenv = filepath.Dir(tree), filepath.Base(tree)
			}
			if cfg["venvPath"] != wantPath || cfg["venv"] != wantVenv || !hasLimit(doc, tc.lim) {
				t.Fatalf("pyright config %v limitations %v", cfg, doc.Limitations)
			}
		})
	}
}

func writePreparedState(t *testing.T, job analysis.Job, cacheRoot, kind, state string) string {
	t.Helper()
	if state == depdoc.StateFailed {
		r := "registry_unreachable"
		rec := depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
			TaskKey: "deps.python", Module: depdoc.Module{ID: "python:.", Ecosystem: "python", Path: "."}, Manager: depdoc.Manager{Name: "uv"},
			State: state, Reason: &r, TaskStatus: string(domain.TaskFailed), Cache: depdoc.Cache{Kind: depdoc.CacheNone},
			DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
		b, err := depdoc.Marshal(rec, depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, TaskKey: "deps.python"})
		if err != nil {
			t.Fatal(err)
		}
		p := depdoc.Path(job.Scratch, "deps.python")
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return ""
	}
	return writePrepared(t, job, cacheRoot, kind, state, "3.14")
}

func fakeJS(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pyright", "index.js")
	packtest.WriteTree(t, filepath.Dir(p), map[string]string{"index.js": "// not executed\n"})
	return p
}

// The real checkers on a project that misuses a typed package: without the
// prepared tree the import is a coverage gap; with it each checker reports
// the misuse. mypy asks the real interpreter for the tree's search paths.
func TestRealTypeCheckersWithPreparedTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mypy shim links the interpreter (Unix)")
	}
	py := testTool(t, "CODEYE_TEST_PYTHON")
	probe := t.TempDir()
	v := (&executor.Executor{}).Run(context.Background(), executor.Spec{Executable: py, Args: []string{"-I", "-S", "-c", `import sys; print("%d.%d" % sys.version_info[:2])`},
		WorkspaceRoot: probe, Dir: probe, Env: map[string]string{"PATH": filepath.Dir(py)}, Timeout: 30 * time.Second, StdoutLimit: 1024, StderrLimit: 1024})
	if v.ExitCode == nil || *v.ExitCode != 0 {
		t.Skip("interpreter does not run")
	}
	minor := strings.TrimSpace(string(v.Stdout.Data))
	mypyExe := testTool(t, "CODEYE_TEST_MYPY")
	for _, prepared := range []bool{false, true} {
		cacheRoot := filepath.Join(t.TempDir(), "deps")
		fx := newFixture(t, preparedProject, domain.Scope{})
		if prepared {
			writePrepared(t, fx.Job, cacheRoot, depdoc.CacheShared, depdoc.StatePrepared, minor)
		}
		a, err := NewMypy(Config{Executable: mypyExe, DependencyCacheRoot: cacheRoot, Interpreter: py})
		if err != nil {
			t.Fatal(err)
		}
		res := packtest.RunTask(t, a, fx.Job, "mypy", &executor.Executor{})
		doc, raw := readEnvelope(t, fx.Job, res)
		if res.Status != domain.TaskSucceeded {
			t.Fatalf("prepared=%v: %s/%s\n%s", prepared, res.Status, res.Reason, raw)
		}
		unresolved := doc.Coverage.Units[UnitImportUnresolved]
		misuse := len(findObs(doc, "arg-type"))
		if prepared && (unresolved != 0 || misuse != 1 || !hasLimit(doc, LimThirdPartyPrepared)) || !prepared && (unresolved != 1 || misuse != 0) {
			t.Fatalf("prepared=%v: unresolved %d arg-type %d limitations %v\n%s", prepared, unresolved, misuse, doc.Limitations, raw)
		}
		packtest.AssertNoNodePaths(t, raw, cacheRoot, fx.Job.Scratch, fx.SourceRoot)
	}
}

// pyright reads the prepared tree's site-packages as a virtual environment
// and starts no interpreter.
func TestRealPyrightWithPreparedTree(t *testing.T) {
	exe, node := testTool(t, "CODEYE_TEST_PYRIGHT"), testNode(t)
	for _, prepared := range []bool{false, true} {
		cacheRoot := filepath.Join(t.TempDir(), "deps")
		fx := newFixture(t, preparedProject, domain.Scope{})
		if prepared {
			writePrepared(t, fx.Job, cacheRoot, depdoc.CacheAttempt, depdoc.StatePrepared, "3.12")
		}
		a, err := NewPyright(Config{Executable: exe, Node: node, DependencyCacheRoot: cacheRoot})
		if err != nil {
			t.Fatal(err)
		}
		res := packtest.RunTask(t, a, fx.Job, "pyright", &executor.Executor{})
		doc, raw := readEnvelope(t, fx.Job, res)
		if res.Status != domain.TaskSucceeded {
			t.Fatalf("prepared=%v: %s/%s\n%s", prepared, res.Status, res.Reason, raw)
		}
		unresolved := doc.Coverage.Units[UnitImportUnresolved]
		misuse := len(findObs(doc, "reportArgumentType"))
		if prepared && (unresolved != 0 || misuse != 1 || !hasLimit(doc, LimThirdPartyPrepared)) || !prepared && (unresolved != 1 || misuse != 0) {
			t.Fatalf("prepared=%v: unresolved %d reportArgumentType %d limitations %v\n%s", prepared, unresolved, misuse, doc.Limitations, raw)
		}
		packtest.AssertNoNodePaths(t, raw, cacheRoot, fx.Job.Scratch, fx.SourceRoot)
	}
}
