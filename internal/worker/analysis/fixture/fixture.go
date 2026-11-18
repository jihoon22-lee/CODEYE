//go:build codeye_fixture

// Package fixture is a TEST-ONLY analysis handler compiled only with
// -tags codeye_fixture. It exercises the worker/agent path with real
// processes; it performs no analysis and its results are not findings.
//
// Mode is the first line of "codeye-fixture-mode" at the snapshot root:
// succeed (default) | fail | partial | sleep | unsupported. Linux only
// (/bin/sh). No network, no user-provided strings reach a shell.
package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// ModeFile is read from the snapshot root.
const ModeFile = "codeye-fixture-mode"

type handler struct{}

// New returns the fixture handler (capability analysis:deep-review).
func New() analysis.Handler { return handler{} }

func (handler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "codeye-fixture", Version: "1", Capability: "analysis:deep-review"}
}

func mode(job analysis.Job) string {
	b, err := os.ReadFile(filepath.Join(job.Snapshot.SnapshotDir, ModeFile))
	if err != nil {
		return "succeed"
	}
	m := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	switch m {
	case "succeed", "fail", "partial", "sleep", "unsupported":
		return m
	}
	return "succeed"
}

func (handler) Plan(_ context.Context, job analysis.Job) ([]analysis.TaskSpec, error) {
	// The fixture's shell tasks stand in for tool runs (display stage only).
	tasks := []analysis.TaskSpec{{Key: "fixture.scan", Required: true, Title: "fixture scan", Stage: domain.StageStaticAnalysis}}
	if mode(job) == "partial" {
		tasks = append(tasks, analysis.TaskSpec{Key: "fixture.extra", Required: true, Title: "fixture extra", Stage: domain.StageStaticAnalysis})
	}
	return tasks, nil
}

func (handler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	m := mode(job)
	if m == "unsupported" || runtime.GOOS != "linux" {
		return analysis.TaskResult{Status: domain.TaskUnsupported, Reason: "analysis_unsupported", Limitations: []string{"fixture_mode_unsupported"}}
	}
	script := `find . -type f | wc -l`
	switch {
	case m == "fail", m == "partial" && task.Key == "fixture.extra":
		script = `echo fixture failure >&2; exit 3`
	case m == "sleep":
		// Background grandchild + foreground child: cancel must clean both.
		script = `sleep 300 & sleep 300; wait`
	}
	spec := executor.Spec{
		Executable:    "/bin/sh",
		Args:          []string{"-c", script},
		WorkspaceRoot: job.Snapshot.WorkspaceDir,
		Dir:           job.Snapshot.WorkspaceDir,
		Env:           map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"},
		Timeout:       10 * time.Minute,
	}
	res := runner.Run(ctx, spec)
	ex, err := analysis.WriteExecution(filepath.Join(job.Scratch, "exec", task.Key), "sh", res)
	out := analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	if err == nil {
		out.Executions = []analysis.Execution{ex}
	}
	switch {
	case res.Status == executor.StatusCancelled:
		out.Status, out.Reason = domain.TaskCancelled, "cancelled"
		return out
	case res.Status == executor.StatusTimedOut:
		out.Status, out.Reason = domain.TaskTimedOut, "timed_out"
		return out
	case res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0 || err != nil:
		return out
	}
	n, perr := strconv.Atoi(strings.TrimSpace(string(bytes.TrimSpace(res.Stdout.Data))))
	if perr != nil || res.Stdout.Truncated {
		return out
	}
	obs, _ := json.Marshal(map[string]any{"schema_version": "fixture-0", "file_count": n, "note": "fixture handler; not an analysis result"})
	path := filepath.Join(job.Scratch, "exec", task.Key, "observations.json")
	if os.WriteFile(path, obs, 0600) != nil {
		return out
	}
	out.Status, out.Reason = domain.TaskSucceeded, ""
	out.Artifacts = []analysis.ArtifactFile{{Path: path, Role: "observations", MediaType: "application/json"}}
	return out
}
