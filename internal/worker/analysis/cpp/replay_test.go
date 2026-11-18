package cpp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Replay tests run in CI without either tool: the fake executables answer
// with the redacted streams captured from cppcheck 2.22.0 and clang-tidy
// 23.1.5 in testdata/ (real_test.go, docs/41 §9). The fixture input and
// expected.json are the ones the captures were taken from; changing either
// requires a new capture.

type recorded struct {
	stdout, stderr []byte
	exit           int
}

func loadCapture(t *testing.T, name string) recorded {
	t.Helper()
	read := func(suffix string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name+suffix))
		if err != nil {
			t.Fatalf("capture %s%s: %v", name, suffix, err)
		}
		return b
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(read(".exit"))))
	if err != nil {
		t.Fatal(err)
	}
	return recorded{stdout: read(".stdout"), stderr: read(".stderr"), exit: code}
}

// replayRunner answers the fake tool: --version from the version capture,
// the n-th check execution from checks[n].
type replayRunner struct {
	fake    string
	version recorded
	checks  []recorded
	mutate  func(n int, r *executor.Result)
	specs   []executor.Spec
	n       int
}

func (r *replayRunner) Run(_ context.Context, spec executor.Spec) executor.Result {
	r.specs = append(r.specs, spec)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: append([]string{spec.Executable}, spec.Args...), Dir: spec.Dir, EnvKeys: keys}}
	rec := r.version
	isCheck := !(len(spec.Args) == 1 && spec.Args[0] == "--version")
	n := -1
	if isCheck {
		n = r.n
		r.n++
		if n >= len(r.checks) {
			return executor.Result{Status: executor.StatusStartFailed, Cleanup: executor.CleanupNotStarted}
		}
		rec = r.checks[n]
	}
	code := rec.exit
	res.ExitCode = &code
	res.Stdout = executor.Stream{Data: append([]byte{}, rec.stdout...), TotalBytes: int64(len(rec.stdout))}
	res.Stderr = executor.Stream{Data: append([]byte{}, rec.stderr...), TotalBytes: int64(len(rec.stderr))}
	if isCheck && r.mutate != nil {
		r.mutate(n, &res)
	}
	return res
}

func fakeTool(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not executed: replayed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// captures lists the check captures of a case: <case>.cppcheck or
// <case>.clang-tidy.<n> for n = 1.. as long as they exist.
func captures(t *testing.T, name, tool string) []recorded {
	t.Helper()
	if tool == CppcheckTool {
		if _, err := os.Stat(filepath.Join("testdata", name+".cppcheck.exit")); err != nil {
			return nil
		}
		return []recorded{loadCapture(t, name+".cppcheck")}
	}
	var out []recorded
	for n := 1; ; n++ {
		base := name + ".clang-tidy." + strconv.Itoa(n)
		if _, err := os.Stat(filepath.Join("testdata", base+".exit")); err != nil {
			return out
		}
		out = append(out, loadCapture(t, base))
	}
}

func replayCppcheck(t *testing.T, files map[string]string, scope domain.Scope, checks []recorded, mutate func(int, *executor.Result)) (analysis.TaskResult, obsdoc.Document, *replayRunner) {
	t.Helper()
	fake := fakeTool(t, "cppcheck")
	a, err := NewCppcheck(Config{Executable: fake})
	if err != nil {
		t.Fatal(err)
	}
	r := &replayRunner{fake: fake, version: loadCapture(t, "version.cppcheck"), checks: checks, mutate: mutate}
	fx := newFixture(t, files, scope)
	res := runTask(t, a, fx.Job, r)
	doc, _ := readEnvelope(t, fx.Job, res)
	return res, doc, r
}

func replayClangTidy(t *testing.T, files map[string]string, scope domain.Scope, batch int, checks []recorded, mutate func(int, *executor.Result)) (analysis.TaskResult, obsdoc.Document, *replayRunner) {
	t.Helper()
	fake := fakeTool(t, "clang-tidy")
	a, err := NewClangTidy(Config{Executable: fake, BatchSize: batch})
	if err != nil {
		t.Fatal(err)
	}
	r := &replayRunner{fake: fake, version: loadCapture(t, "version.clang-tidy"), checks: checks, mutate: mutate}
	fx := newFixture(t, files, scope)
	res := runTask(t, a, fx.Job, r)
	doc, _ := readEnvelope(t, fx.Job, res)
	return res, doc, r
}

// TC-105..TC-107 in CI: every non-real-only case of expected.json on the
// captured streams gives the expected outcome.
func TestReplayFixtureCases(t *testing.T) {
	e := loadExpected(t)
	for _, name := range caseNames(e) {
		c := e.Cases[name]
		if c.RealOnly {
			continue
		}
		t.Run(name, func(t *testing.T) {
			files := caseFiles(t, c)
			if c.Cppcheck != nil {
				checks := captures(t, name, CppcheckTool)
				if len(checks) == 0 && slices.Contains(c.Cppcheck.Executions, execCppcheckCheck) {
					t.Fatalf("no cppcheck capture for %s", name)
				}
				_, doc, _ := replayCppcheck(t, files, caseScope(c), checks, nil)
				compareTool(t, name+" cppcheck", doc, *c.Cppcheck)
			}
			if c.ClangTidy != nil {
				checks := captures(t, name, ClangTidyTool)
				_, doc, _ := replayClangTidy(t, files, caseScope(c), c.BatchSize, checks, nil)
				compareTool(t, name+" clang-tidy", doc, *c.ClangTidy)
			}
		})
	}
}

// The captured bytes hold no node path, no source snippet and no line of a
// file outside the fixture.
func TestCapturesHoldNoNodePaths(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	for _, en := range entries {
		b, err := os.ReadFile(filepath.Join("testdata", en.Name()))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, bad := range []string{"/home/", "/tmp/", "/Users/", home} {
			if bad != "" && strings.Contains(s, bad) {
				t.Fatalf("%s holds %q", en.Name(), bad)
			}
		}
		if strings.HasPrefix(en.Name(), "version.") {
			continue
		}
		for _, l := range strings.Split(s, "\n") {
			if ctSnippet.MatchString(l) {
				t.Fatalf("%s holds a source snippet line %q", en.Name(), l)
			}
		}
	}
}
