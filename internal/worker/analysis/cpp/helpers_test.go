package cpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	fixtureInput    = "../../../../fixtures/w16-cpp/input"
	fixtureExpected = "../../../../fixtures/w16-cpp/expected.json"
	// generatedHeader is what the fixture's configure step would write.
	generatedHeaderPath = "_build/gen/config.h"
	generatedHeader     = "#ifndef W16_CONFIG_H\n#define W16_CONFIG_H\n#define W16_VERSION \"1.0\"\n#endif\n"
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

// fixtureFiles reads fixtures/w16-cpp/input as a file map.
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

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func treeHash(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := filepath.ToSlash(rel) + "|" + info.Mode().String()
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

type fixture struct {
	SourceRoot string
	Job        analysis.Job
}

// newFixture acquires a W04 snapshot of files and clones an attempt
// workspace like the worker.
func newFixture(t *testing.T, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	base := t.TempDir()
	src, data, scratch := filepath.Join(base, "source"), filepath.Join(base, "data"), filepath.Join(base, "scratch")
	writeTree(t, src, files)
	for _, d := range []string{data, scratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	snap, err := source.AcquireLocal(ctx, src, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	loaded, err := source.Load(ctx, snap.Root, snap.Digest)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ws := filepath.Join(data, "attempt-workspace")
	if err := source.CloneWorkspace(ctx, loaded, ws); err != nil {
		t.Fatalf("clone: %v", err)
	}
	loaded.WorkspaceDir = ws
	if scope.Kind == "" {
		scope.Kind = "full"
	}
	return fixture{SourceRoot: src, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}}
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
	if len(res.Artifacts) != 1 || res.Artifacts[0].Role != analysis.RoleObservations || res.Artifacts[0].MediaType != analysis.MediaObservations {
		t.Fatalf("observations artifact missing: %+v", res)
	}
	raw, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Base(filepath.Dir(res.Artifacts[0].Path))
	doc, err := obsdoc.Decode(raw, pack.Expect(job, key))
	if err != nil {
		t.Fatalf("envelope invalid: %v\n%s", err, raw)
	}
	if dump := os.Getenv("CODEYE_W16_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
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

func assertNoNodePaths(t *testing.T, raw []byte, paths ...string) {
	t.Helper()
	s := string(raw)
	for _, p := range paths {
		if p == "" {
			continue
		}
		for _, f := range []string{p, filepath.ToSlash(p)} {
			if strings.Contains(s, f) {
				t.Fatalf("structured output contains node path %q", f)
			}
		}
	}
}

// captureStreams writes the check execution's streams to
// CODEYE_W16_CAPTURE/<name>.{stdout,stderr,exit} (docs/41 §9): the replay
// tests run the parsers on these committed bytes in CI.
func captureStreams(t *testing.T, name string, res analysis.TaskResult, exec string) {
	t.Helper()
	dir := os.Getenv("CODEYE_W16_CAPTURE")
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
