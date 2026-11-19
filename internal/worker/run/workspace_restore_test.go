//go:build linux

package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// leftoverHandler is a test handler of four tasks in one attempt workspace:
// t.mutate rewrites a manifest file and leaves a lock file, a build
// directory and a dependency directory behind; t.scan reads the whole tree
// and checks it against the manifest like the whole-tree analyzers do; t.self
// rewrites a manifest file during its own run and checks afterwards; t.after
// scans again.
type leftoverHandler struct{}

const leftoverOriginal = "print('original')\n"

func (leftoverHandler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "test", Version: "1", Capability: "analysis:deep-review"}
}

func (leftoverHandler) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "t.mutate", Required: true}, {Key: "t.scan", Required: true}, {Key: "t.self", Required: true}, {Key: "t.after", Required: true}}, nil
}

func (leftoverHandler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	script := map[string]string{
		"t.mutate": "printf 'changed' > src/app.py && mkdir -p target/debug && echo out > target/debug/out && echo lock > Cargo.lock && mkdir -p node_modules/dep && echo m > node_modules/dep/index.js",
		"t.scan":   "cat src/app.py && find . -type f | LC_ALL=C sort",
		"t.self":   "printf 'changed' > src/app.py",
		"t.after":  "cat src/app.py && find . -type f | LC_ALL=C sort",
	}[task.Key]
	res := r.Run(ctx, executor.Spec{Executable: "/bin/sh", Args: []string{"-c", script}, WorkspaceRoot: job.Snapshot.WorkspaceDir, Dir: job.Snapshot.WorkspaceDir,
		ProtectedRoots: []string{job.Snapshot.SnapshotDir}, Env: map[string]string{"PATH": "/usr/bin:/bin"}, Timeout: time.Minute, StdoutLimit: 1 << 20, StderrLimit: 1 << 20})
	ex, err := analysis.WriteExecution(filepath.Join(job.Scratch, task.Key), "sh", res)
	if err != nil || res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	}
	out := analysis.TaskResult{Status: domain.TaskSucceeded, Executions: []analysis.Execution{ex}}
	if task.Key == "t.mutate" {
		return out
	}
	// The whole-tree analyzers' rule: the selected manifest files must still
	// hold their manifest bytes after the tool ran.
	if drift := anypack.Drift(job.Snapshot.WorkspaceDir, anypack.Files(job, nil, false)); len(drift) > 0 {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: anypack.ReasonWorkspaceDrift, Executions: out.Executions, Limitations: drift}
	}
	text := string(res.Stdout.Data)
	if task.Key != "t.self" && (!strings.HasPrefix(text, leftoverOriginal) || strings.Contains(text, "Cargo.lock") || strings.Contains(text, "target/")) {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "scanned_leftovers", Executions: out.Executions}
	}
	return out
}

// TC-198: a task that rewrites a manifest file and leaves files behind does
// not fail the next task that reads the whole tree: the worker restores the
// workspace to the snapshot after every task and names the task that left
// changes. A manifest file a task changes during its own run is still that
// task's workspace drift, and the next task starts from the snapshot again.
// Unlisted data directories (prepared dependencies) stay.
func TestWorkspaceRestoredBetweenTasks(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Join(e.src, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.src, "src", "app.py"), []byte(leftoverOriginal), 0o600); err != nil {
		t.Fatal(err)
	}
	fa, in, done, _ := startWorker(t, leftoverHandler{})
	defer func() {
		in.Close()
		if code := <-done; code != 0 {
			t.Errorf("worker exit %d", code)
		}
	}()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-fa.started:
			case <-stop:
				return
			}
		}
	}()
	fa.hello(e)
	if err := fa.conn.Send(localJob(e)); err != nil {
		t.Fatal(err)
	}
	c := waitComplete(t, fa)
	if c.ResultManifestClientArtifact == nil {
		t.Fatalf("complete %+v", c)
	}
	fa.mu.Lock()
	body := fa.artifacts[*c.ResultManifestClientArtifact]
	fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	rows, lims := taskRows(m), map[string][]string{}
	for _, task := range m.Tasks {
		lims[task.Key] = task.Limitations
	}
	want := map[string]string{"t.mutate": "succeeded/", "t.scan": "succeeded/", "t.self": "failed/" + anypack.ReasonWorkspaceDrift, "t.after": "succeeded/"}
	for k, v := range want {
		if rows[k] != v {
			t.Fatalf("tasks %v, want %v", rows, want)
		}
	}
	if !slices.Contains(lims["t.mutate"], LimWorkspaceRestored) || !slices.Contains(lims["t.mutate"], LimWorkspaceLeftovers) {
		t.Fatalf("t.mutate limitations %v", lims["t.mutate"])
	}
	if !slices.Contains(lims["t.self"], LimWorkspaceRestored) || slices.Contains(lims["t.self"], LimWorkspaceLeftovers) {
		t.Fatalf("t.self limitations %v", lims["t.self"])
	}
	for _, k := range []string{"t.scan", "t.after"} {
		if len(lims[k]) != 0 {
			t.Fatalf("%s limitations %v", k, lims[k])
		}
	}
	// The original folder was never written.
	if b, _ := os.ReadFile(filepath.Join(e.src, "src", "app.py")); string(b) != leftoverOriginal {
		t.Fatalf("original changed: %q", b)
	}
}
