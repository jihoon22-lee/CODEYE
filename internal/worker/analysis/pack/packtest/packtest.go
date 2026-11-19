// Package packtest holds the test fixtures every analyzer pack's tests
// share: a snapshot with a cloned attempt workspace, one planned task run
// through a given runner, the observations envelope read back with the
// contract checks, tree digests and node-path leak checks. Only tests
// import it.
package packtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// WriteTree writes files (slash paths) below root.
func WriteTree(t testing.TB, root string, files map[string]string) {
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

// TreeHash digests every path, mode and regular file content below root.
func TreeHash(t testing.TB, root string) string {
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
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Fixture is a snapshot of files with a cloned attempt workspace.
type Fixture struct {
	Base, SourceRoot, DataRoot string
	Job                        analysis.Job
}

// NewFixture acquires a snapshot of files under base (a temp dir when
// empty) and clones an attempt workspace like the worker. An empty scope
// kind is "full".
func NewFixture(t testing.TB, base string, files map[string]string, scope domain.Scope) Fixture {
	t.Helper()
	if base == "" {
		base = t.TempDir()
	}
	src, data, scratch := filepath.Join(base, "source"), filepath.Join(base, "data"), filepath.Join(base, "scratch")
	WriteTree(t, src, files)
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
	return Fixture{Base: base, SourceRoot: src, DataRoot: data, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}}
}

// Planner plans and runs analyzer tasks (a pack.Analyzer without its
// descriptor).
type Planner interface {
	Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error)
	Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult
}

// RunTask plans the job with a and runs the task keyed key through runner.
func RunTask(t testing.TB, a Planner, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	return RunTaskCtx(t, context.Background(), a, job, key, runner)
}

// RunTaskCtx is RunTask with a run context (planning uses a background one).
func RunTaskCtx(t testing.TB, ctx context.Context, a Planner, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	tasks, err := a.Plan(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Spec.Key == key {
			return a.Run(ctx, job, task.Spec, runner)
		}
	}
	t.Fatalf("task %s not planned: %+v", key, tasks)
	return analysis.TaskResult{}
}

// ReadEnvelope reads the task's observations artifact and decodes it with
// the contract checks. With the environment variable dumpEnv naming a
// directory, the raw envelope is also written there.
func ReadEnvelope(t testing.TB, job analysis.Job, res analysis.TaskResult, dumpEnv string) (obsdoc.Document, []byte) {
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
	if dump := os.Getenv(dumpEnv); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
}

// AssertNoNodePaths fails when raw holds any of the node paths, native or
// slash separated.
func AssertNoNodePaths(t testing.TB, raw []byte, paths ...string) {
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

// FakeExecutable writes a file that stands for a tool path in replay tests
// (never executed).
func FakeExecutable(t testing.TB, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not executed: replayed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// GoExecutable is the go command of CODEYE_TEST_GO, else the toolchain that
// built the test (GOROOT/bin); the test is skipped when neither exists.
func GoExecutable(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("CODEYE_TEST_GO"); p != "" {
		return p
	}
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	//lint:ignore SA1019 the test needs the toolchain that built it, without PATH search
	p := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("go toolchain not found at GOROOT/bin (set CODEYE_TEST_GO): %v", err)
	}
	return p
}
