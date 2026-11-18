package discovery

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// testGo returns the go command that built this test binary, or skips. No
// PATH search; CODEYE_TEST_GO overrides it.
func testGo(t *testing.T) string {
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

// snapshotOf acquires src as a real local W04 snapshot and clones a workspace.
func snapshotOf(t *testing.T, src string, scope domain.Scope) analysis.Job {
	t.Helper()
	base := t.TempDir()
	data, scratch := filepath.Join(base, "data"), filepath.Join(base, "scratch")
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
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}
}

// jobOf writes files into a fresh source tree and snapshots it.
func jobOf(t *testing.T, files map[string]string, scope domain.Scope) analysis.Job {
	t.Helper()
	src := filepath.Join(t.TempDir(), "source")
	writeTree(t, src, files)
	return snapshotOf(t, src, scope)
}

// manifestOf builds an in-memory manifest of regular UTF-8 files for pure tests.
func manifestOf(paths ...string) source.Manifest {
	m := source.Manifest{Entries: []source.Entry{}}
	for _, p := range paths {
		size := int64(10)
		m.Entries = append(m.Entries, source.Entry{Path: p, Kind: "file", Size: &size, SHA256: domain.Digest([]byte(p)), Encoding: "utf-8"})
	}
	return m
}

// fakeRunner records specs and returns a scripted result.
type fakeRunner struct {
	specs  []executor.Spec
	result func(executor.Spec) executor.Result
}

func (f *fakeRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	f.specs = append(f.specs, s)
	return f.result(s)
}

func exited(code int, stdout string) executor.Result {
	return executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Stdout: executor.Stream{Data: []byte(stdout), TotalBytes: int64(len(stdout))}, Process: executor.ProcessIdentity{PID: 4242, StartTime: 7}}
}

type noopHandler struct{ planned []analysis.TaskSpec }

func (h noopHandler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "noop", Version: "1", Capability: "analysis:deep-review"}
}
func (h noopHandler) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return append([]analysis.TaskSpec(nil), h.planned...), nil
}
func (h noopHandler) Run(context.Context, analysis.Job, analysis.TaskSpec, executor.Runner) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}
